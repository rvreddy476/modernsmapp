package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Conversation struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	Title     *string    `json:"title,omitempty"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	IsRequest bool       `json:"is_request"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`

	// Production chat pass: group avatar + inbox last-message metadata.
	AvatarMediaID      *uuid.UUID `json:"avatar_media_id,omitempty"`
	LastMessageAt      *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview string     `json:"last_message_preview,omitempty"`
	LastMessageSender  *uuid.UUID `json:"last_message_sender,omitempty"`
}

type Member struct {
	UserID   uuid.UUID `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type OutboxEvent struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type IdempotencyResult struct {
	RequestHash string          `json:"request_hash"`
	Response    json.RawMessage `json:"response"`
}

type ConversationStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *ConversationStore {
	return &ConversationStore{db: db}
}

// CreateDirectConversation idempotently creates a 1:1 conversation. The
// second return value is true only when a new conversation row was inserted
// (false when an existing one was returned) — the DM-gating caller uses it to
// decide whether to mark a freshly created conversation as a message request.
func (s *ConversationStore) CreateDirectConversation(ctx context.Context, userA, userB, createdBy uuid.UUID) (uuid.UUID, bool, error) {
	if userA.String() > userB.String() {
		userA, userB = userB, userA
	}
	pairKey := userA.String() + ":" + userB.String()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	// Serialize direct-conversation creation per pair to avoid duplicate rows under race.
	lockRows, err := tx.Query(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, pairKey)
	if err != nil {
		return uuid.Nil, false, err
	}
	lockRows.Close()

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT conversation_id FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`, userA, userB).Scan(&conversationID)
	if err == nil {
		return conversationID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	conversationID = uuid.New()
	now := time.Now()

	_, err = tx.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by, created_at, updated_at) VALUES ($1, 'direct', $2, $3, $3)`, conversationID, createdBy, now)
	if err != nil {
		return uuid.Nil, false, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen) VALUES ($1, $2, 'member', $3, nextval('chat.membership_gen_seq')), ($1, $4, 'member', $3, nextval('chat.membership_gen_seq'))`, conversationID, userA, now, userB)
	if err != nil {
		return uuid.Nil, false, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)`, userA, userB, conversationID)
	if err != nil {
		return uuid.Nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return conversationID, true, nil
}

// MarkConversationAsRequest flags a freshly created direct conversation as a
// pending message request (spec §3.3).
func (s *ConversationStore) MarkConversationAsRequest(ctx context.Context, conversationID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.conversations SET is_request = TRUE WHERE id = $1`, conversationID)
	return err
}

// CreateDatingMatchConversation idempotently creates a 1:1 conversation
// tagged source_app='dating' + match_id. The matched pair bypasses the
// usual DM-gate (which requires mutual-circle / message-request flow)
// because the match itself is the consent signal. Idempotency keyed on
// match_id via the partial unique index — concurrent saga retries
// receive the same conversation_id. P0-3 in
// dating/PRODUCTION_GAP_ANALYSIS.md.
func (s *ConversationStore) CreateDatingMatchConversation(ctx context.Context, userA, userB, matchID uuid.UUID) (uuid.UUID, bool, error) {
	if userA == userB {
		return uuid.Nil, false, errors.New("dating-match conversation requires two distinct users")
	}
	userA, userB = normalizePair(userA, userB)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	// Lock on the match_id so a concurrent retry can't insert twice.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "dating-match:"+matchID.String()); err != nil {
		return uuid.Nil, false, err
	}

	// Existing row check (the partial unique index would also reject
	// duplicates, but we return the prior id rather than 23505).
	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM chat.conversations
		WHERE source_app = 'dating' AND match_id = $1
		LIMIT 1`, matchID).Scan(&conversationID)
	if err == nil {
		return conversationID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	conversationID = uuid.New()
	now := time.Now()

	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.conversations
		    (id, type, created_by, created_at, updated_at, source_app, match_id)
		VALUES ($1, 'direct', $2, $3, $3, 'dating', $4)
	`, conversationID, userA, now, matchID); err != nil {
		return uuid.Nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen)
		VALUES ($1, $2, 'member', $3, nextval('chat.membership_gen_seq')),
		       ($1, $4, 'member', $3, nextval('chat.membership_gen_seq'))
	`, conversationID, userA, now, userB); err != nil {
		return uuid.Nil, false, err
	}
	// Mirror into direct_conversation_keys so a future legacy DM
	// lookup for the same pair returns the dating conversation
	// rather than spawning a parallel one. ON CONFLICT preserves an
	// existing chat-side direct conversation if the pair already had
	// one — both rows then exist (legacy DM + dating-match) and the
	// caller picks by source_app filter.
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_a, user_b) DO NOTHING
	`, userA, userB, conversationID); err != nil {
		return uuid.Nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return conversationID, true, nil
}

// MarkConversationClosed sets closed_at on a conversation. Dating-side
// match.closed / match.expired events trigger this so the send-path
// gate can refuse new messages after a match ends. Idempotent — a
// re-close preserves the original closed_at.
func (s *ConversationStore) MarkConversationClosed(ctx context.Context, conversationID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE chat.conversations SET closed_at = COALESCE(closed_at, NOW()) WHERE id = $1`,
		conversationID)
	return err
}

// MarkConversationClosedByMatch closes a conversation by its dating
// match_id rather than conversation_id. Used by the dating-event
// consumer so the lookup is one hop instead of two.
func (s *ConversationStore) MarkConversationClosedByMatch(ctx context.Context, matchID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET closed_at = COALESCE(closed_at, NOW())
		WHERE source_app = 'dating' AND match_id = $1
	`, matchID)
	return err
}

// MarkConversationsClosedByPair closes every source_app='dating'
// conversation that has both userA and userB as (non-left) members.
// Used by the dating block consumer (Phase 1 §4) to sever existing
// match chats the moment either side blocks the other — the
// dating-event consumer handles the dating_match conversations
// because the user could have multiple distinct matches with the
// same person over time (rare but real after an unmatch + rematch).
// Idempotent — re-running preserves closed_at.
func (s *ConversationStore) MarkConversationsClosedByPair(ctx context.Context, userA, userB uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations c
		SET closed_at = COALESCE(closed_at, NOW())
		WHERE c.source_app = 'dating'
		  AND c.closed_at IS NULL
		  AND EXISTS (
		      SELECT 1 FROM chat.conversation_members ma
		      WHERE ma.conversation_id = c.id AND ma.user_id = $1 AND ma.left_at IS NULL
		  )
		  AND EXISTS (
		      SELECT 1 FROM chat.conversation_members mb
		      WHERE mb.conversation_id = c.id AND mb.user_id = $2 AND mb.left_at IS NULL
		  )
	`, userA, userB)
	return err
}

// ConversationMeta holds the fields the send-path gate inspects.
type ConversationMeta struct {
	SourceApp string
	MatchID   *uuid.UUID
	ClosedAt  *time.Time
}

// GetConversationMeta returns the source_app + match_id + closed_at for
// the conversation. Used by SendMessage to enforce dating-specific
// authz. Returns a zero-value ConversationMeta + nil error when the row
// is missing so callers can branch cleanly.
func (s *ConversationStore) GetConversationMeta(ctx context.Context, conversationID uuid.UUID) (*ConversationMeta, error) {
	var m ConversationMeta
	err := s.db.QueryRow(ctx, `
		SELECT source_app, match_id, closed_at
		FROM chat.conversations
		WHERE id = $1
	`, conversationID).Scan(&m.SourceApp, &m.MatchID, &m.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// normalizePair orders a user pair so the smaller (string compare) is first,
// matching the chat.direct_conversation_keys (user_a < user_b) convention.
func normalizePair(userA, userB uuid.UUID) (uuid.UUID, uuid.UUID) {
	if userA.String() > userB.String() {
		return userB, userA
	}
	return userA, userB
}

// PromoteRequestConversationByPair auto-promotes the direct conversation
// between two users from a pending message request to a normal conversation
// (messaging/privacy spec §16.6). It is a no-op when the pair has no direct
// conversation, when that conversation is not a request, or when its
// message_requests row is not pending. Returns true when a promotion happened.
func (s *ConversationStore) PromoteRequestConversationByPair(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	userA, userB = normalizePair(userA, userB)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT conversation_id FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`, userA, userB).Scan(&conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE chat.conversations
		SET is_request = FALSE, request_accepted_at = NOW()
		WHERE id = $1 AND is_request = TRUE
	`, conversationID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// Not a request conversation (or already promoted) — nothing to do.
		return false, nil
	}

	_, err = tx.Exec(ctx, `
		UPDATE chat.message_requests
		SET status = 'accepted', responded_at = NOW()
		WHERE conversation_id = $1 AND status = 'pending'
	`, conversationID)
	if err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// SeveredMembership reports one sever this statement performed, with the
// revocation generation the caller MUST persist (final-verification P0-4).
type SeveredMembership struct {
	ConversationID uuid.UUID
	UserID         uuid.UUID
	Gen            int64
}

// SeverDirectConversation severs the blocker from the direct conversation it
// shares with the blocked user (messaging/privacy spec §16.1). The blocker's
// conversation_members.left_at is set so the conversation disappears from
// their inbox and they can no longer send into it; any pending
// message_requests row for that conversation is moved to 'blocked'. No-op
// when the pair has no direct conversation. Takes the conversation row lock
// first — the same order as every other membership write, so the returned
// generation is serialization-ordered — and returns every sever performed so
// the caller can arm revocation markers before acknowledging the block.
// The first return is the pair's direct conversation id (uuid.Nil when the
// pair has none) — callers use it to re-arm defensively on retries where the
// sever already happened and the list comes back empty.
func (s *ConversationStore) SeverDirectConversation(ctx context.Context, blockerID, blockedID uuid.UUID) (uuid.UUID, []SeveredMembership, error) {
	userA, userB := normalizePair(blockerID, blockedID)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer tx.Rollback(ctx)

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT conversation_id FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`, userA, userB).Scan(&conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, nil
	}
	if err != nil {
		return uuid.Nil, nil, err
	}

	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		conversationID).Scan(&convType); err != nil {
		return uuid.Nil, nil, err
	}

	rows, err := tx.Query(ctx, `
		UPDATE chat.conversation_members
		SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = ANY($2::uuid[]) AND left_at IS NULL
		RETURNING user_id, nextval('chat.membership_gen_seq')
	`, conversationID, []uuid.UUID{blockerID, blockedID})
	if err != nil {
		return uuid.Nil, nil, err
	}
	var severed []SeveredMembership
	for rows.Next() {
		sm := SeveredMembership{ConversationID: conversationID}
		if err := rows.Scan(&sm.UserID, &sm.Gen); err != nil {
			rows.Close()
			return uuid.Nil, nil, err
		}
		severed = append(severed, sm)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, nil, err
	}
	// Durable revocation intents ride the sever transaction (Blocker-2
	// final correction): a committed sever always leaves the marker debt on
	// record for the repair worker.
	for _, sm := range severed {
		if err := upsertRevocationIntentTx(ctx, tx, conversationID, sm.UserID, sm.Gen); err != nil {
			return uuid.Nil, nil, err
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE chat.message_requests
		SET status = 'blocked', responded_at = NOW()
		WHERE conversation_id = $1 AND status = 'pending'
	`, conversationID)
	if err != nil {
		return uuid.Nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, err
	}
	return conversationID, severed, nil
}

// CreateGroupConversation creates a group conversation with the creator as admin.
func (s *ConversationStore) CreateGroupConversation(ctx context.Context, creatorID uuid.UUID, title string, memberIDs []uuid.UUID) (uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	conversationID := uuid.New()
	now := time.Now()

	_, err = tx.Exec(ctx, `INSERT INTO chat.conversations (id, type, title, created_by, created_at, updated_at) VALUES ($1, 'group', $2, $3, $4, $4)`, conversationID, title, creatorID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// Creator is the OWNER (directive §3.4: exactly one owner per group).
	_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen) VALUES ($1, $2, 'owner', $3, nextval('chat.membership_gen_seq'))`, conversationID, creatorID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// Other members
	for _, memberID := range memberIDs {
		if memberID == creatorID {
			continue
		}
		_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen) VALUES ($1, $2, 'member', $3, nextval('chat.membership_gen_seq')) ON CONFLICT DO NOTHING`, conversationID, memberID, now)
		if err != nil {
			return uuid.Nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return conversationID, nil
}

func (s *ConversationStore) GetConversation(ctx context.Context, conversationID uuid.UUID) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRow(ctx, `
		SELECT id, type, title, created_by, is_request, created_at, updated_at,
		       avatar_media_id, last_message_at, last_message_preview, last_message_sender
		FROM chat.conversations WHERE id = $1
	`, conversationID).Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.IsRequest, &c.CreatedAt, &c.UpdatedAt,
		&c.AvatarMediaID, &c.LastMessageAt, &c.LastMessagePreview, &c.LastMessageSender)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *ConversationStore) TouchConversation(ctx context.Context, conversationID uuid.UUID, ts time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.conversations SET updated_at = $2 WHERE id = $1`, conversationID, ts)
	return err
}

// SetLastMessage denormalizes the newest message onto the conversation row
// for the inbox list. The ts guard makes delivery-repair REPLAY idempotent:
// an older intent re-run never overwrites a newer preview.
func (s *ConversationStore) SetLastMessage(ctx context.Context, conversationID, senderID uuid.UUID, preview string, ts time.Time) error {
	if len(preview) > 140 {
		preview = preview[:140]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET last_message_at = $2, last_message_preview = $3, last_message_sender = $4, updated_at = $2
		WHERE id = $1 AND (last_message_at IS NULL OR last_message_at <= $2)
	`, conversationID, ts, preview, senderID)
	return err
}

// ReplaceLastMessage rewrites the denormalized inbox preview after the message
// it was taken from was deleted.
//
// Guarded on the CURRENT preview still pointing at deletedTs, which makes it a
// no-op when a newer message has already replaced it — so a delete racing a
// send can never resurrect an older preview. Unlike SetLastMessage this must
// accept an OLDER timestamp: the replacement is by definition the message
// before the deleted one.
//
// updated_at is deliberately untouched: deleting a message must not reorder
// the inbox.
func (s *ConversationStore) ReplaceLastMessage(
	ctx context.Context,
	conversationID uuid.UUID,
	deletedTs time.Time,
	preview string,
	senderID *uuid.UUID,
	ts *time.Time,
) error {
	if len(preview) > 140 {
		preview = preview[:140]
	}
	// The guard is a ONE-MILLISECOND WINDOW, not equality. last_message_at is
	// written from the delivery intent at microsecond precision, while the
	// same instant read back from Scylla is truncated to milliseconds — an
	// `=` comparison silently never matched, and the deleted text stayed on
	// the inbox. The window covers the truncation from either direction; two
	// messages inside one millisecond would at worst trigger a recompute
	// that lands on the same newest survivor anyway.
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET last_message_at = $3, last_message_preview = $4, last_message_sender = $5
		WHERE id = $1
		  AND last_message_at >= $2
		  AND last_message_at < $2 + interval '1 millisecond'
	`, conversationID, deletedTs, ts, preview, senderID)
	return err
}

func (s *ConversationStore) ListConversationsByUser(ctx context.Context, userID uuid.UUID, limit int, cursorUpdatedAt *time.Time, cursorID *uuid.UUID) ([]Conversation, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// m.left_at IS NULL excludes conversations the user has been severed from
	// (e.g. by a block — spec §16.1) so they no longer appear in the inbox.
	var rows pgx.Rows
	var err error
	const listColumns = `c.id, c.type, c.title, c.created_by, c.is_request, c.created_at, c.updated_at,
		c.avatar_media_id, c.last_message_at, c.last_message_preview, c.last_message_sender`
	if cursorUpdatedAt != nil && cursorID != nil {
		rows, err = s.db.Query(ctx, `
			SELECT `+listColumns+`
			FROM chat.conversations c
			JOIN chat.conversation_members m ON m.conversation_id = c.id
			WHERE m.user_id = $1 AND m.left_at IS NULL AND (c.updated_at, c.id) < ($2, $3)
			ORDER BY c.updated_at DESC, c.id DESC
			LIMIT $4
		`, userID, *cursorUpdatedAt, *cursorID, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+listColumns+`
			FROM chat.conversations c
			JOIN chat.conversation_members m ON m.conversation_id = c.id
			WHERE m.user_id = $1 AND m.left_at IS NULL
			ORDER BY c.updated_at DESC, c.id DESC
			LIMIT $2
		`, userID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.IsRequest, &c.CreatedAt, &c.UpdatedAt,
			&c.AvatarMediaID, &c.LastMessageAt, &c.LastMessagePreview, &c.LastMessageSender); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CheckMembership reports whether the user is an active member of the
// conversation. A member whose left_at is set (e.g. severed by a block —
// spec §16.1) is treated as a non-member so they can no longer read or
// send into the conversation.
func (s *ConversationStore) CheckMembership(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL)`, conversationID, userID).Scan(&exists)
	return exists, err
}

func (s *ConversationStore) GetMembers(ctx context.Context, conversationID uuid.UUID) ([]Member, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, role, joined_at
		FROM chat.conversation_members
		WHERE conversation_id = $1 AND left_at IS NULL
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *ConversationStore) GetMemberRole(ctx context.Context, conversationID, userID uuid.UUID) (string, error) {
	var role string
	// left_at IS NULL: a severed/removed member has no role. Without the
	// filter, a removed admin could still pass role checks.
	err := s.db.QueryRow(ctx, `SELECT role FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL`, conversationID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

// AddMember inserts an active membership. A former member (left_at set) is
// REJOINED by clearing left_at with a fresh joined_at — the previous ON
// CONFLICT DO NOTHING silently left a re-added member severed, which made
// every remove permanent. An already-active member is left untouched
// (idempotent re-add).
func (s *ConversationStore) AddMember(ctx context.Context, conversationID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen)
		VALUES ($1, $2, $3, NOW(), nextval('chat.membership_gen_seq'))
		ON CONFLICT (conversation_id, user_id) DO UPDATE
			SET role = EXCLUDED.role, joined_at = NOW(), left_at = NULL,
			    join_gen = nextval('chat.membership_gen_seq')
			WHERE chat.conversation_members.left_at IS NOT NULL
	`, conversationID, userID, role)
	return err
}

// RemoveMember severs the membership NON-DESTRUCTIVELY by setting left_at —
// the same model the block sever uses (spec §16.1). History and the other
// members' view survive; the removed member fails CheckMembership from this
// statement on. Returns false when the user was not an active member.
func (s *ConversationStore) RemoveMember(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *ConversationStore) UpdateTitle(ctx context.Context, conversationID uuid.UUID, title string) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.conversations SET title = $2, updated_at = NOW() WHERE id = $1`, conversationID, title)
	return err
}

// --- Outbox ---

func (s *ConversationStore) InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO chat.outbox_events (event_type, payload) VALUES ($1, $2)`, eventType, data)
	return err
}

func (s *ConversationStore) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `SELECT id, event_type, payload, created_at FROM chat.outbox_events WHERE published_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *ConversationStore) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.outbox_events SET published_at = NOW() WHERE id = $1`, id)
	return err
}

// --- Idempotency ---

func (s *ConversationStore) CheckIdempotencyKey(ctx context.Context, key string) (*IdempotencyResult, error) {
	var requestHash string
	var response json.RawMessage
	err := s.db.QueryRow(ctx, `SELECT request_hash, response FROM chat.idempotency_keys WHERE key = $1 AND expires_at > NOW()`, key).Scan(&requestHash, &response)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &IdempotencyResult{RequestHash: requestHash, Response: response}, nil
}

func (s *ConversationStore) CreateIdempotencyKey(ctx context.Context, key, requestHash string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO chat.idempotency_keys (key, request_hash, response)
		VALUES ($1, $2, NULL)
		ON CONFLICT (key) DO NOTHING
	`, key, requestHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *ConversationStore) SaveIdempotencyResponse(ctx context.Context, key, requestHash string, response interface{}) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.idempotency_keys
		SET response = $3
		WHERE key = $1 AND request_hash = $2
	`, key, requestHash, data)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("idempotency key not found or request hash mismatch")
	}
	return nil
}

func (s *ConversationStore) ReleaseIdempotencyKey(ctx context.Context, key, requestHash string) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM chat.idempotency_keys
		WHERE key = $1 AND request_hash = $2 AND response IS NULL
	`, key, requestHash)
	return err
}
