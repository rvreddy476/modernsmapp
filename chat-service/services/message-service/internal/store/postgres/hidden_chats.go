package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Hidden chats (contract pinned 24 Oct 2026; migration 018).
//
// conversation_members.locked is the per-member "hidden chat" flag. Every
// query that lists a member's conversations — or anything derived from them
// (search, requests, starred messages, notes peers, badges) — must exclude
// the member's locked rows. hidden_chats_inventory_test.go scans this package
// so a new list query cannot forget the filter.

// memberNotLocked is the filter for a query that joins the viewer's own
// membership row as `m`.
const memberNotLocked = `NOT m.locked`

// notLockedFor is the filter for a query that has no `m` join: true when
// userExpr has NOT locked the conversation convExpr.
func notLockedFor(convExpr, userExpr string) string {
	return `NOT EXISTS (SELECT 1 FROM chat.conversation_members lk WHERE lk.conversation_id = ` +
		convExpr + ` AND lk.user_id = ` + userExpr + ` AND lk.locked)`
}

// ErrHiddenChatsNotConfigured: the account has not set its secrets.
var ErrHiddenChatsNotConfigured = errors.New("hidden chats are not set up")

// HiddenChatSettings is one chat.hidden_chat_settings row. The hashes never
// leave the service layer.
type HiddenChatSettings struct {
	UserID           uuid.UUID
	SecretKeyHash    string
	ChatPasswordHash string
	Strict           bool
	Shortcut         *string
	UpdatedAt        time.Time
}

// GetHiddenChatSettings returns the row, or nil when the account has none.
func (s *ConversationStore) GetHiddenChatSettings(ctx context.Context, userID uuid.UUID) (*HiddenChatSettings, error) {
	var hs HiddenChatSettings
	err := s.db.QueryRow(ctx, `
		SELECT user_id, secret_key_hash, chat_password_hash, strict, shortcut, updated_at
		FROM chat.hidden_chat_settings WHERE user_id = $1
	`, userID).Scan(&hs.UserID, &hs.SecretKeyHash, &hs.ChatPasswordHash, &hs.Strict, &hs.Shortcut, &hs.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &hs, nil
}

// SaveHiddenChatSettings writes the full row (insert or replace). Callers
// pass hashes only; the table's CHECK refuses anything else.
func (s *ConversationStore) SaveHiddenChatSettings(ctx context.Context, hs HiddenChatSettings) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.hidden_chat_settings (user_id, secret_key_hash, chat_password_hash, strict, shortcut, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
		    secret_key_hash = EXCLUDED.secret_key_hash,
		    chat_password_hash = EXCLUDED.chat_password_hash,
		    strict = EXCLUDED.strict,
		    shortcut = EXCLUDED.shortcut,
		    updated_at = NOW()
	`, hs.UserID, hs.SecretKeyHash, hs.ChatPasswordHash, hs.Strict, hs.Shortcut)
	return err
}

// --- wrong-attempt lockouts (Postgres, so a restart cannot reset them) ---

// Secret kinds in chat.hidden_chat_attempts.
const (
	SecretKindKey      = "secret_key"
	SecretKindPassword = "chat_password"
)

// AttemptState is a secret's current lockout state.
type AttemptState struct {
	Failures     int
	LockoutLevel int
	LockedUntil  *time.Time
}

// GetHiddenChatAttempts returns the state (zero value when no row).
func (s *ConversationStore) GetHiddenChatAttempts(ctx context.Context, userID uuid.UUID, kind string) (AttemptState, error) {
	var st AttemptState
	err := s.db.QueryRow(ctx, `
		SELECT failures, lockout_level, locked_until FROM chat.hidden_chat_attempts
		WHERE user_id = $1 AND secret_kind = $2
	`, userID, kind).Scan(&st.Failures, &st.LockoutLevel, &st.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptState{}, nil
	}
	return st, err
}

// RecordHiddenChatFailure counts one wrong attempt atomically. When the count
// reaches threshold the secret locks for ladder[level] (the last step
// repeats), the level rises and the count restarts. Returns the state after
// the write.
func (s *ConversationStore) RecordHiddenChatFailure(ctx context.Context, userID uuid.UUID, kind string, now time.Time, threshold int, ladder []time.Duration) (AttemptState, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return AttemptState{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.hidden_chat_attempts (user_id, secret_kind) VALUES ($1, $2)
		ON CONFLICT (user_id, secret_kind) DO NOTHING
	`, userID, kind); err != nil {
		return AttemptState{}, err
	}
	var st AttemptState
	if err := tx.QueryRow(ctx, `
		SELECT failures, lockout_level, locked_until FROM chat.hidden_chat_attempts
		WHERE user_id = $1 AND secret_kind = $2 FOR UPDATE
	`, userID, kind).Scan(&st.Failures, &st.LockoutLevel, &st.LockedUntil); err != nil {
		return AttemptState{}, err
	}
	st = NextAttemptState(st, now, threshold, ladder)
	if _, err := tx.Exec(ctx, `
		UPDATE chat.hidden_chat_attempts
		SET failures = $3, lockout_level = $4, locked_until = $5, last_failure_at = $6, updated_at = NOW()
		WHERE user_id = $1 AND secret_kind = $2
	`, userID, kind, st.Failures, st.LockoutLevel, st.LockedUntil, now); err != nil {
		return AttemptState{}, err
	}
	return st, tx.Commit(ctx)
}

// NextAttemptState is the pure lockout rule RecordHiddenChatFailure applies.
func NextAttemptState(st AttemptState, now time.Time, threshold int, ladder []time.Duration) AttemptState {
	st.Failures++
	if st.Failures >= threshold && len(ladder) > 0 {
		step := st.LockoutLevel
		if step >= len(ladder) {
			step = len(ladder) - 1
		}
		until := now.Add(ladder[step])
		st.LockedUntil = &until
		st.LockoutLevel++
		st.Failures = 0
	}
	return st
}

// ClearHiddenChatFailures ends the count after a correct secret. The lockout
// level is kept: escalation is per account, and a correct guess after a
// lockout does not buy a cheaper next lockout. A reset clears everything
// (ResetHiddenChats).
func (s *ConversationStore) ClearHiddenChatFailures(ctx context.Context, userID uuid.UUID, kind string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.hidden_chat_attempts SET failures = 0, updated_at = NOW()
		WHERE user_id = $1 AND secret_kind = $2 AND failures <> 0
	`, userID, kind)
	return err
}

// --- reveal / open tokens ---

// Token kinds in chat.hidden_chat_tokens.
const (
	TokenKindReveal = "reveal"
	TokenKindOpen   = "open"
)

// InsertHiddenChatToken stores a token hash (and drops the user's expired
// tokens in the same statement batch).
func (s *ConversationStore) InsertHiddenChatToken(ctx context.Context, tokenHash []byte, userID uuid.UUID, deviceKey, kind string, expiresAt time.Time) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM chat.hidden_chat_tokens WHERE user_id = $1 AND expires_at <= NOW()`, userID); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.hidden_chat_tokens (token_hash, user_id, device_key, kind, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tokenHash, userID, deviceKey, kind, expiresAt)
	return err
}

// TouchHiddenChatToken validates a token for (user, device, kind) and slides
// its expiry to newExpiry in one statement. ok=false for an unknown, expired,
// foreign-user or foreign-device token.
func (s *ConversationStore) TouchHiddenChatToken(ctx context.Context, tokenHash []byte, userID uuid.UUID, deviceKey, kind string, now, newExpiry time.Time) (time.Time, bool, error) {
	var exp time.Time
	err := s.db.QueryRow(ctx, `
		UPDATE chat.hidden_chat_tokens
		SET expires_at = $6, last_used_at = $5
		WHERE token_hash = $1 AND user_id = $2 AND device_key = $3 AND kind = $4 AND expires_at > $5
		RETURNING expires_at
	`, tokenHash, userID, deviceKey, kind, now, newExpiry).Scan(&exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return exp, true, nil
}

// DeleteHiddenChatTokens drops the user's tokens: one device's when
// deviceKey is non-empty, every device's otherwise.
func (s *ConversationStore) DeleteHiddenChatTokens(ctx context.Context, userID uuid.UUID, deviceKey string) error {
	if deviceKey != "" {
		_, err := s.db.Exec(ctx, `DELETE FROM chat.hidden_chat_tokens WHERE user_id = $1 AND device_key = $2`, userID, deviceKey)
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM chat.hidden_chat_tokens WHERE user_id = $1`, userID)
	return err
}

// --- the per-member flag ---

// LockConversationForMember sets locked for the member's ACTIVE row. When it
// changes the row it also moves the membership to a fresh generation and
// records a revocation intent for the old one, in the same transaction, so
// every conversation-room entitlement issued before the hide dies (the caller
// arms the marker; the repair worker covers a crash). changed=false when the
// row was already locked. ErrNotActiveMember when there is no active row.
func (s *ConversationStore) LockConversationForMember(ctx context.Context, conversationID, userID uuid.UUID) (changed bool, oldGen int64, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)
	// Conversation row lock first: the order every membership write uses.
	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`, conversationID).Scan(&convType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, 0, ErrNotActiveMember
		}
		return false, 0, err
	}
	var locked bool
	var gen *int64
	err = tx.QueryRow(ctx, `
		SELECT locked, join_gen FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL FOR UPDATE
	`, conversationID, userID).Scan(&locked, &gen)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, ErrNotActiveMember
	}
	if err != nil {
		return false, 0, err
	}
	if locked {
		return false, 0, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE chat.conversation_members
		SET locked = TRUE, locked_at = NOW(), join_gen = nextval('chat.membership_gen_seq')
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID); err != nil {
		return false, 0, err
	}
	if gen != nil {
		oldGen = *gen
		if err := upsertRevocationIntentTx(ctx, tx, conversationID, userID, oldGen); err != nil {
			return false, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return true, oldGen, nil
}

// UnlockConversationForMember clears locked on the member's active row.
// ErrNotActiveMember when there is none; idempotent otherwise.
func (s *ConversationStore) UnlockConversationForMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members SET locked = FALSE, locked_at = NULL
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotActiveMember
	}
	return nil
}

// IsConversationLockedFor reports whether userID has locked conversationID
// (false for a non-member: the route's own membership check answers that).
func (s *ConversationStore) IsConversationLockedFor(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	var locked bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM chat.conversation_members
		               WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL AND locked)
	`, conversationID, userID).Scan(&locked)
	return locked, err
}

// LockedConversationsAmong returns which of convIDs userID has locked.
func (s *ConversationStore) LockedConversationsAmong(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id FROM chat.conversation_members
		WHERE user_id = $1 AND conversation_id = ANY($2::uuid[]) AND locked
	`, userID, convIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// LockedMemberIDs lists the active members who have locked conversationID
// (MessageCreated carries them so notification-service blanks their push).
func (s *ConversationStore) LockedMemberIDs(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id FROM chat.conversation_members
		WHERE conversation_id = $1 AND left_at IS NULL AND locked
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ViewerMayAccessLockedChatMedia: the media rides a message in a conversation
// the viewer is an active member of and HAS hidden. The caller grants it only
// with a valid open token.
func (s *ConversationStore) ViewerMayAccessLockedChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	var allowed bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat.message_media_references r
			JOIN chat.conversation_members m ON m.conversation_id = r.conversation_id
			WHERE r.media_id = $1 AND m.user_id = $2 AND m.left_at IS NULL AND m.locked
		)
	`, mediaID, viewerID).Scan(&allowed)
	return allowed, err
}

// ListHiddenConversations pages the member's LOCKED conversations, newest
// activity first — the hidden list (GET /v1/chat/hidden/conversations). A
// locked conversation the member also deleted-for-me stays out until a newer
// message arrives, exactly like the normal list.
func (s *ConversationStore) ListHiddenConversations(ctx context.Context, userID uuid.UUID, limit int, cursorTs *time.Time, cursorID *uuid.UUID) ([]Conversation, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	args := []any{userID}
	q := `
		SELECT c.id, c.type, c.title, c.created_by, c.is_request, c.created_at, c.updated_at,
		       c.avatar_media_id, c.last_message_at, c.last_message_preview, c.last_message_sender, c.description,
		       COALESCE(c.source_app, ''), c.match_id, COALESCE(c.last_message_at, c.created_at)
		FROM chat.conversations c
		JOIN chat.conversation_members m ON m.conversation_id = c.id AND m.user_id = $1 AND m.left_at IS NULL
		WHERE m.locked AND ` + visibleToMember
	if cursorTs != nil && cursorID != nil {
		args = append(args, *cursorTs, *cursorID)
		q += ` AND (COALESCE(c.last_message_at, c.created_at), c.id) < ($2, $3)`
	}
	args = append(args, limit)
	q += ` ORDER BY COALESCE(c.last_message_at, c.created_at) DESC, c.id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.IsRequest, &c.CreatedAt, &c.UpdatedAt,
			&c.AvatarMediaID, &c.LastMessageAt, &c.LastMessagePreview, &c.LastMessageSender, &c.Description,
			&c.SourceApp, &c.MatchID, &c.SortAt); err != nil {
			return nil, err
		}
		s.openConversation(ctx, &c)
		out = append(out, c)
	}
	return out, rows.Err()
}

// HiddenChatsReset is one reset after sign-in.
type HiddenChatsReset struct {
	Settings HiddenChatSettings
	// Wipe clears (delete-for-me, history cut at At) and unlocks every locked
	// conversation of the user — strict mode.
	Wipe bool
	At   time.Time
}

// ResetHiddenChats applies a reset in ONE transaction: new secrets, every
// lockout and token of the user dropped, and — in strict mode — the user's
// locked conversations wiped for them alone (other members untouched).
// Returns the wiped conversation ids.
func (s *ConversationStore) ResetHiddenChats(ctx context.Context, r HiddenChatsReset) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	userID := r.Settings.UserID
	var wiped []uuid.UUID
	if r.Wipe {
		rows, err := tx.Query(ctx, `
			UPDATE chat.conversation_members
			SET cleared_before = GREATEST(COALESCE(cleared_before, $2), $2),
			    unread_count = 0,
			    marked_unread = FALSE,
			    hidden = TRUE,
			    locked = FALSE,
			    locked_at = NULL
			WHERE user_id = $1 AND locked AND left_at IS NULL
			RETURNING conversation_id
		`, userID, r.At)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			wiped = append(wiped, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.hidden_chat_settings (user_id, secret_key_hash, chat_password_hash, strict, shortcut, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
		    secret_key_hash = EXCLUDED.secret_key_hash,
		    chat_password_hash = EXCLUDED.chat_password_hash,
		    strict = EXCLUDED.strict,
		    shortcut = EXCLUDED.shortcut,
		    updated_at = NOW()
	`, userID, r.Settings.SecretKeyHash, r.Settings.ChatPasswordHash, r.Settings.Strict, r.Settings.Shortcut); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chat.hidden_chat_attempts WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chat.hidden_chat_tokens WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return wiped, nil
}
