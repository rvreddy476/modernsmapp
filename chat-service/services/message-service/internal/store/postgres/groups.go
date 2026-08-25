package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Production chat pass (directive §3.4, §5.2): group governance, invitations,
// read cursors and the local privacy-policy projection.

// ErrOwnerRequired is returned by transactional role mutations that would
// leave the group without exactly one owner.
var ErrOwnerRequired = errors.New("group must keep exactly one owner")

// ErrGroupFull is returned when an add/accept would exceed the launch cap.
var ErrGroupFull = errors.New("group has reached the member cap")

// ErrRoleNotPermitted is returned by RemoveMemberGoverned when the roles —
// re-read under lock at commit time — do not allow the removal.
var ErrRoleNotPermitted = errors.New("role does not permit this group mutation")

// ErrNotAMember is returned when the removal target holds no active
// membership row.
var ErrNotAMember = errors.New("target user is not a conversation member")

// ErrOwnerMustTransferStore is returned by LeaveGoverned when the owner tries
// to leave a group that still has other active members.
var ErrOwnerMustTransferStore = errors.New("transfer ownership before leaving the group")

// GroupInvitation is one consent-required membership offer.
type GroupInvitation struct {
	ID             uuid.UUID  `json:"id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	InviterID      uuid.UUID  `json:"inviter_id"`
	InviteeID      uuid.UUID  `json:"invitee_id"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	RespondedAt    *time.Time `json:"responded_at,omitempty"`
}

// UserPolicy is the local hot-path projection of a user's chat-relevant
// privacy settings. Refreshed lazily and invalidated by user.settings_changed.
type UserPolicy struct {
	UserID                 uuid.UUID `json:"user_id"`
	ChatPaused             bool      `json:"chat_paused"`
	SendTypingIndicators   bool      `json:"send_typing_indicators"`
	ReadReceiptsVisibility string    `json:"read_receipts_visibility"`
	PrivacyVersion         int       `json:"privacy_version"`
	RefreshedAt            time.Time `json:"refreshed_at"`
}

// ReadCursor is a member's durable unread watermark for one conversation.
type ReadCursor struct {
	ConversationID    uuid.UUID `json:"conversation_id"`
	UserID            uuid.UUID `json:"user_id"`
	LastReadMessageID uuid.UUID `json:"last_read_message_id"`
	LastReadAt        time.Time `json:"last_read_at"`
}

// CountActiveMembers counts the live roster.
func (s *ConversationStore) CountActiveMembers(ctx context.Context, conversationID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat.conversation_members
		WHERE conversation_id = $1 AND left_at IS NULL
	`, conversationID).Scan(&n)
	return n, err
}

// AddMemberCapped inserts (or rejoins) a member inside one transaction that
// takes a row lock on the conversation, so two concurrent adds cannot both
// observe count = cap-1 and overshoot the launch cap.
func (s *ConversationStore) AddMemberCapped(ctx context.Context, conversationID, userID uuid.UUID, role string, cap int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// The conversation row is the cap's lock object.
	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		conversationID).Scan(&convType); err != nil {
		return err
	}

	var count int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat.conversation_members
		WHERE conversation_id = $1 AND left_at IS NULL
	`, conversationID).Scan(&count); err != nil {
		return err
	}
	if count >= cap {
		return ErrGroupFull
	}

	// join_gen is drawn from the membership sequence WHILE HOLDING the
	// conversation lock, so it is ordered by serialization, not by clock
	// (final-verification P0-4).
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen)
		VALUES ($1, $2, $3, NOW(), nextval('chat.membership_gen_seq'))
		ON CONFLICT (conversation_id, user_id) DO UPDATE
			SET role = EXCLUDED.role, joined_at = NOW(), left_at = NULL,
			    join_gen = nextval('chat.membership_gen_seq')
			WHERE chat.conversation_members.left_at IS NOT NULL
	`, conversationID, userID, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveMemberGoverned severs a member with the role ladder re-checked INSIDE
// the transaction (P0-5): the service's earlier reads are advisory only,
// because a concurrent ownership transfer can promote the target between the
// read and the sever. Lock order is conversation row first, then member rows
// — the same order every other governance transaction uses. Returns the
// sever GENERATION — drawn from the membership sequence while the
// conversation lock is held, so it is ordered by serialization rather than
// transaction-start time (final-verification P0-4) — which the caller must
// persist as the revocation marker before reporting success.
func (s *ConversationStore) RemoveMemberGoverned(ctx context.Context, conversationID, actorID, targetID uuid.UUID) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		conversationID).Scan(&convType); err != nil {
		return 0, err
	}

	var actorRole string
	err = tx.QueryRow(ctx, `
		SELECT role FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL FOR UPDATE
	`, conversationID, actorID).Scan(&actorRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrRoleNotPermitted
		}
		return 0, err
	}
	var targetRole string
	err = tx.QueryRow(ctx, `
		SELECT role FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL FOR UPDATE
	`, conversationID, targetID).Scan(&targetRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotAMember
		}
		return 0, err
	}

	// The ladder, against roles that now cannot change under us: nobody
	// severs an owner; admins sever ordinary members only.
	if targetRole == "owner" {
		return 0, ErrRoleNotPermitted
	}
	if actorRole != "owner" && !(actorRole == "admin" && targetRole == "member") {
		return 0, ErrRoleNotPermitted
	}

	var severGen int64
	err = tx.QueryRow(ctx, `
		UPDATE chat.conversation_members
		SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL AND role <> 'owner'
		RETURNING nextval('chat.membership_gen_seq')
	`, conversationID, targetID).Scan(&severGen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotAMember
		}
		return 0, err
	}
	if err := upsertRevocationIntentTx(ctx, tx, conversationID, targetID, severGen); err != nil {
		return 0, err
	}
	return severGen, tx.Commit(ctx)
}

// SeverMemberSystem is the SYSTEM-AUTHORITY sever every non-governed removal
// path must use (final-verification P0-4: managed-group removal, request
// block/report and graph-block reconciliation previously severed through
// legacy statements that knew nothing about revocation). Same lock order as
// every governance transaction; no role ladder — the calling system owns its
// own authorization. Returns (severed, generation).
func (s *ConversationStore) SeverMemberSystem(ctx context.Context, conversationID, userID uuid.UUID) (bool, int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		conversationID).Scan(&convType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, 0, nil
		}
		return false, 0, err
	}
	var severGen int64
	err = tx.QueryRow(ctx, `
		UPDATE chat.conversation_members
		SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
		RETURNING nextval('chat.membership_gen_seq')
	`, conversationID, userID).Scan(&severGen)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, tx.Commit(ctx) // already gone — idempotent
	}
	if err != nil {
		return false, 0, err
	}
	if err := upsertRevocationIntentTx(ctx, tx, conversationID, userID, severGen); err != nil {
		return false, 0, err
	}
	return true, severGen, tx.Commit(ctx)
}

// LeaveGoverned is transactional self-removal (P0-5): the owner-must-transfer
// rule is decided against a member count taken under the conversation row
// lock, so a concurrent join (which takes the same lock in AddMemberCapped)
// cannot slip in between the count and the sever and leave the group
// ownerless. Returns (false, 0) when the caller was already gone; on a
// sever, the returned value is the revocation generation (P0-4), sequence-
// ordered under the conversation lock.
func (s *ConversationStore) LeaveGoverned(ctx context.Context, conversationID, userID uuid.UUID) (bool, int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		conversationID).Scan(&convType); err != nil {
		return false, 0, err
	}

	var role string
	err = tx.QueryRow(ctx, `
		SELECT role FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL FOR UPDATE
	`, conversationID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, 0, nil // already gone — leaving twice is a success
		}
		return false, 0, err
	}
	if role == "owner" {
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM chat.conversation_members
			WHERE conversation_id = $1 AND left_at IS NULL
		`, conversationID).Scan(&count); err != nil {
			return false, 0, err
		}
		if count > 1 {
			return false, 0, ErrOwnerMustTransferStore
		}
	}
	var severGen int64
	if err := tx.QueryRow(ctx, `
		UPDATE chat.conversation_members SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
		RETURNING nextval('chat.membership_gen_seq')
	`, conversationID, userID).Scan(&severGen); err != nil {
		return false, 0, err
	}
	if err := upsertRevocationIntentTx(ctx, tx, conversationID, userID, severGen); err != nil {
		return false, 0, err
	}
	return true, severGen, tx.Commit(ctx)
}

// GetMemberGen returns the ACTIVE membership row's generation — what an
// entitlement token is issued under (P0-4). Rows written before the
// generation column existed are backfilled lazily; the guarded UPDATE blocks
// on any concurrent sever's row lock, so a backfilled generation can never
// be allocated after a sever that has already severed this row.
func (s *ConversationStore) GetMemberGen(ctx context.Context, conversationID, userID uuid.UUID) (int64, error) {
	var gen *int64
	err := s.db.QueryRow(ctx, `
		SELECT join_gen FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID).Scan(&gen)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotAMember
	}
	if err != nil {
		return 0, err
	}
	if gen != nil {
		return *gen, nil
	}
	var filled int64
	err = s.db.QueryRow(ctx, `
		UPDATE chat.conversation_members
		SET join_gen = nextval('chat.membership_gen_seq')
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL AND join_gen IS NULL
		RETURNING join_gen
	`, conversationID, userID).Scan(&filled)
	if errors.Is(err, pgx.ErrNoRows) {
		// Concurrently backfilled or severed — one re-read decides which.
		err = s.db.QueryRow(ctx, `
			SELECT join_gen FROM chat.conversation_members
			WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL AND join_gen IS NOT NULL
		`, conversationID, userID).Scan(&filled)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotAMember
		}
	}
	return filled, err
}

// NextMembershipGen allocates a revocation generation for the retry lanes
// (a sever that committed but whose marker write failed): strictly greater
// than every generation allocated before this call.
func (s *ConversationStore) NextMembershipGen(ctx context.Context) (int64, error) {
	var gen int64
	err := s.db.QueryRow(ctx, `SELECT nextval('chat.membership_gen_seq')`).Scan(&gen)
	return gen, err
}

// --- Durable revocation intents (Blocker-2 final correction) ---

// RevocationIntent is one durable "the deny marker for this sever is not yet
// known to be in Redis" record. Written in the sever transaction; deleted by
// the caller/worker once the marker write succeeded.
type RevocationIntent struct {
	ConversationID uuid.UUID
	UserID         uuid.UUID
	SeverGen       int64
	CreatedAt      time.Time
}

type intentExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const upsertRevocationIntentSQL = `
	INSERT INTO chat.revocation_intents (conversation_id, user_id, sever_gen)
	VALUES ($1, $2, $3)
	ON CONFLICT (conversation_id, user_id) DO UPDATE
		SET sever_gen = GREATEST(chat.revocation_intents.sever_gen, EXCLUDED.sever_gen),
		    created_at = NOW()
`

func upsertRevocationIntentTx(ctx context.Context, tx intentExecutor, conversationID, userID uuid.UUID, severGen int64) error {
	_, err := tx.Exec(ctx, upsertRevocationIntentSQL, conversationID, userID, severGen)
	return err
}

// UpsertRevocationIntent records the intent OUTSIDE a sever transaction —
// the defensive/already-gone lanes, where the sever committed earlier
// (possibly in a crashed process) and only the marker is owed.
func (s *ConversationStore) UpsertRevocationIntent(ctx context.Context, conversationID, userID uuid.UUID, severGen int64) error {
	_, err := s.db.Exec(ctx, upsertRevocationIntentSQL, conversationID, userID, severGen)
	return err
}

// FetchPendingRevocationIntents returns the oldest pending intents.
func (s *ConversationStore) FetchPendingRevocationIntents(ctx context.Context, limit int) ([]RevocationIntent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id, sever_gen, created_at
		FROM chat.revocation_intents
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RevocationIntent
	for rows.Next() {
		var ri RevocationIntent
		if err := rows.Scan(&ri.ConversationID, &ri.UserID, &ri.SeverGen, &ri.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ri)
	}
	return out, rows.Err()
}

// DeleteRevocationIntent clears an intent AFTER its marker write succeeded.
// Guarded by generation: an intent re-written with a NEWER sever generation
// (a second removal racing this delete) survives and is re-armed by the
// worker.
func (s *ConversationStore) DeleteRevocationIntent(ctx context.Context, conversationID, userID uuid.UUID, armedGen int64) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM chat.revocation_intents
		WHERE conversation_id = $1 AND user_id = $2 AND sever_gen <= $3
	`, conversationID, userID, armedGen)
	return err
}

// SetMemberRole flips an active member between admin and member. The owner
// row is never touched here — ownership moves only through TransferOwnership.
func (s *ConversationStore) SetMemberRole(ctx context.Context, conversationID, userID uuid.UUID, role string) (bool, error) {
	if role != "admin" && role != "member" {
		return false, errors.New("role must be admin or member")
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members SET role = $3
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL AND role <> 'owner'
	`, conversationID, userID, role)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// TransferOwnership atomically moves the owner role from -> to. The old owner
// becomes an admin. Fails when either side is not an active member or when
// `from` is not the current owner — the exactly-one-owner invariant is
// enforced INSIDE the transaction, not by the caller's earlier reads.
func (s *ConversationStore) TransferOwnership(ctx context.Context, conversationID, fromUserID, toUserID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Conversation row first (P0-5): every governance transaction takes the
	// same lock in the same order, so transfer, removal, leave and add
	// serialize instead of interleaving into an ownerless group.
	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		conversationID).Scan(&convType); err != nil {
		return err
	}

	var fromRole string
	err = tx.QueryRow(ctx, `
		SELECT role FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL FOR UPDATE
	`, conversationID, fromUserID).Scan(&fromRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOwnerRequired
		}
		return err
	}
	if fromRole != "owner" {
		return ErrOwnerRequired
	}

	var toRole string
	err = tx.QueryRow(ctx, `
		SELECT role FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL FOR UPDATE
	`, conversationID, toUserID).Scan(&toRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("new owner must be an active member")
		}
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat.conversation_members SET role = 'admin'
		WHERE conversation_id = $1 AND user_id = $2
	`, conversationID, fromUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE chat.conversation_members SET role = 'owner'
		WHERE conversation_id = $1 AND user_id = $2
	`, conversationID, toUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateGroupInfo writes title and/or avatar. Nil leaves a field untouched.
func (s *ConversationStore) UpdateGroupInfo(ctx context.Context, conversationID uuid.UUID, title *string, avatarMediaID *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET title = COALESCE($2, title),
		    avatar_media_id = COALESCE($3, avatar_media_id),
		    updated_at = NOW()
		WHERE id = $1
	`, conversationID, title, avatarMediaID)
	return err
}

// --- Group invitations ---

// CreateGroupInvitation inserts a pending invitation; a concurrent retry
// collapses onto the existing pending row (returned with created=false).
func (s *ConversationStore) CreateGroupInvitation(ctx context.Context, conversationID, inviterID, inviteeID uuid.UUID) (*GroupInvitation, bool, error) {
	var inv GroupInvitation
	err := s.db.QueryRow(ctx, `
		INSERT INTO chat.group_invitations (conversation_id, inviter_id, invitee_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (conversation_id, invitee_id) WHERE status = 'pending' DO NOTHING
		RETURNING id, conversation_id, inviter_id, invitee_id, status, created_at, responded_at
	`, conversationID, inviterID, inviteeID).Scan(
		&inv.ID, &inv.ConversationID, &inv.InviterID, &inv.InviteeID, &inv.Status, &inv.CreatedAt, &inv.RespondedAt)
	if err == nil {
		return &inv, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	// Conflict path: fetch the existing pending row.
	err = s.db.QueryRow(ctx, `
		SELECT id, conversation_id, inviter_id, invitee_id, status, created_at, responded_at
		FROM chat.group_invitations
		WHERE conversation_id = $1 AND invitee_id = $2 AND status = 'pending'
	`, conversationID, inviteeID).Scan(
		&inv.ID, &inv.ConversationID, &inv.InviterID, &inv.InviteeID, &inv.Status, &inv.CreatedAt, &inv.RespondedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, errors.New("invitation insert conflicted but no pending row exists")
		}
		return nil, false, err
	}
	return &inv, false, nil
}

func (s *ConversationStore) GetGroupInvitation(ctx context.Context, invitationID uuid.UUID) (*GroupInvitation, error) {
	var inv GroupInvitation
	err := s.db.QueryRow(ctx, `
		SELECT id, conversation_id, inviter_id, invitee_id, status, created_at, responded_at
		FROM chat.group_invitations WHERE id = $1
	`, invitationID).Scan(
		&inv.ID, &inv.ConversationID, &inv.InviterID, &inv.InviteeID, &inv.Status, &inv.CreatedAt, &inv.RespondedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// ListPendingInvitationsForUser feeds the invitee's invitations inbox.
func (s *ConversationStore) ListPendingInvitationsForUser(ctx context.Context, userID uuid.UUID, limit int) ([]GroupInvitation, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, conversation_id, inviter_id, invitee_id, status, created_at, responded_at
		FROM chat.group_invitations
		WHERE invitee_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupInvitation
	for rows.Next() {
		var inv GroupInvitation
		if err := rows.Scan(&inv.ID, &inv.ConversationID, &inv.InviterID, &inv.InviteeID,
			&inv.Status, &inv.CreatedAt, &inv.RespondedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// ResolveGroupInvitation transitions a PENDING invitation to a terminal
// status. Returns false when the invitation was already resolved — the
// caller treats that as an idempotent success for the SAME status.
func (s *ConversationStore) ResolveGroupInvitation(ctx context.Context, invitationID uuid.UUID, status string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.group_invitations
		SET status = $2, responded_at = NOW()
		WHERE id = $1 AND status = 'pending'
	`, invitationID, status)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// --- Message-request cooldown ---

// GetLatestRequestBetween returns the newest request row from sender to
// receiver regardless of conversation — the decline-cooldown lookup
// (directive §3.3: a declined request cannot be recreated by rotating keys).
func (s *ConversationStore) GetLatestRequestBetween(ctx context.Context, senderID, receiverID uuid.UUID) (*MessageRequest, error) {
	var mr MessageRequest
	err := s.db.QueryRow(ctx, `
		SELECT conversation_id, sender_id, receiver_id, preview, status, created_at, responded_at
		FROM chat.message_requests
		WHERE sender_id = $1 AND receiver_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, senderID, receiverID).Scan(
		&mr.ConversationID, &mr.SenderID, &mr.ReceiverID, &mr.Preview, &mr.Status, &mr.CreatedAt, &mr.RespondedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &mr, nil
}

// --- Read cursors ---

// UpsertReadCursor advances the member's unread watermark. Monotonic: an
// out-of-order or replayed mark-read never moves the cursor backwards.
func (s *ConversationStore) UpsertReadCursor(ctx context.Context, conversationID, userID, messageID uuid.UUID, readAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.read_cursors (conversation_id, user_id, last_read_message_id, last_read_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (conversation_id, user_id) DO UPDATE
			SET last_read_message_id = EXCLUDED.last_read_message_id,
			    last_read_at = EXCLUDED.last_read_at,
			    updated_at = NOW()
			WHERE chat.read_cursors.last_read_at < EXCLUDED.last_read_at
	`, conversationID, userID, messageID, readAt)
	return err
}

// GetReadCursors returns the user's cursors for a batch of conversations —
// one query for a whole inbox page.
func (s *ConversationStore) GetReadCursors(ctx context.Context, userID uuid.UUID, conversationIDs []uuid.UUID) (map[uuid.UUID]ReadCursor, error) {
	out := make(map[uuid.UUID]ReadCursor, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id, COALESCE(last_read_message_id, '00000000-0000-0000-0000-000000000000'::uuid), last_read_at
		FROM chat.read_cursors
		WHERE user_id = $1 AND conversation_id = ANY($2)
	`, userID, conversationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rc ReadCursor
		if err := rows.Scan(&rc.ConversationID, &rc.UserID, &rc.LastReadMessageID, &rc.LastReadAt); err != nil {
			return nil, err
		}
		out[rc.ConversationID] = rc
	}
	return out, rows.Err()
}

// --- User policy projection ---

// GetUserPolicy returns the projected policy row, or nil when the user has
// never been projected (caller then fetches from the identity authority).
func (s *ConversationStore) GetUserPolicy(ctx context.Context, userID uuid.UUID) (*UserPolicy, error) {
	var p UserPolicy
	err := s.db.QueryRow(ctx, `
		SELECT user_id, chat_paused, send_typing_indicators, read_receipts_visibility, privacy_version, refreshed_at
		FROM chat.user_policy WHERE user_id = $1
	`, userID).Scan(&p.UserID, &p.ChatPaused, &p.SendTypingIndicators, &p.ReadReceiptsVisibility, &p.PrivacyVersion, &p.RefreshedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpsertUserPolicy writes a projected policy snapshot. Version-guarded so a
// stale fetch racing a fresher one can never regress the projection.
func (s *ConversationStore) UpsertUserPolicy(ctx context.Context, p UserPolicy) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.user_policy (user_id, chat_paused, send_typing_indicators, read_receipts_visibility, privacy_version, refreshed_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id) DO UPDATE
			SET chat_paused = EXCLUDED.chat_paused,
			    send_typing_indicators = EXCLUDED.send_typing_indicators,
			    read_receipts_visibility = EXCLUDED.read_receipts_visibility,
			    privacy_version = EXCLUDED.privacy_version,
			    refreshed_at = NOW()
			WHERE chat.user_policy.privacy_version <= EXCLUDED.privacy_version
	`, p.UserID, p.ChatPaused, p.SendTypingIndicators, p.ReadReceiptsVisibility, p.PrivacyVersion)
	return err
}

// InvalidateUserPolicy drops the projection so the next hot-path read
// re-fetches the authoritative snapshot. Called by the identity-events
// consumer on user.settings_changed.
func (s *ConversationStore) InvalidateUserPolicy(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.user_policy WHERE user_id = $1`, userID)
	return err
}
