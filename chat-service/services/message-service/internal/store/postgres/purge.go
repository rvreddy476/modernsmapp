package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── Account control: hide / purge (acks as "message") ───────────────────────

// ConversationRef is what the Scylla redaction pass needs per conversation.
type ConversationRef struct {
	ID        uuid.UUID
	CreatedAt time.Time
}

// UserConversations lists every conversation the user is or was a member of,
// with the conversation's creation time (lower bound for month buckets).
func (s *ConversationStore) UserConversations(ctx context.Context, userID uuid.UUID) ([]ConversationRef, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.created_at
		FROM chat.conversation_members m
		JOIN chat.conversations c ON c.id = m.conversation_id
		WHERE m.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationRef
	for rows.Next() {
		var r ConversationRef
		if err := rows.Scan(&r.ID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetUserHidden flags chat.user_profiles.hidden. Presence reports a hidden
// user as offline and typing indicators from them are dropped. Idempotent.
func (s *ConversationStore) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.user_profiles (user_id, display_name, hidden)
		VALUES ($1, '', $2)
		ON CONFLICT (user_id) DO UPDATE SET hidden = EXCLUDED.hidden, updated_at = NOW()`,
		userID, hidden)
	return err
}

// HiddenUsers returns the subset of ids currently hidden.
func (s *ConversationStore) HiddenUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT user_id FROM chat.user_profiles WHERE hidden AND user_id = ANY($1)`, ids)
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

// PurgeUser erases every chat.* row keyed by the user in ONE transaction.
// Conversations themselves stay (other members keep their history); the
// Scylla message bodies authored by the user are redacted by the caller
// BEFORE this runs. Idempotent.
func (s *ConversationStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stmts := []string{
		`DELETE FROM chat.chat_folder_conversations WHERE folder_id IN (SELECT id FROM chat.chat_folders WHERE user_id = $1)`,
		`DELETE FROM chat.chat_folders WHERE user_id = $1`,
		`DELETE FROM chat.conversation_settings WHERE user_id = $1`,
		`DELETE FROM chat.conversation_pins WHERE pinned_by = $1`,
		`DELETE FROM chat.message_request_settings WHERE user_id = $1`,
		`DELETE FROM chat.starred_messages WHERE user_id = $1`,
		`DELETE FROM chat.chat_backups WHERE user_id = $1`,
		`DELETE FROM chat.scheduled_messages WHERE sender_id = $1`,
		`DELETE FROM chat.message_requests WHERE sender_id = $1 OR receiver_id = $1`,
		`DELETE FROM chat.group_invitations WHERE inviter_id = $1 OR invitee_id = $1`,
		`DELETE FROM chat.read_cursors WHERE user_id = $1`,
		`DELETE FROM chat.user_policy WHERE user_id = $1`,
		`DELETE FROM chat.revocation_intents WHERE user_id = $1`,
		`DELETE FROM chat.direct_conversation_keys WHERE user_a = $1 OR user_b = $1`,
		`DELETE FROM chat.message_delivery_intents WHERE sender_id = $1`,
		`UPDATE chat.conversations SET last_message_sender = NULL WHERE last_message_sender = $1`,
		`DELETE FROM chat.conversation_members WHERE user_id = $1`,
		`DELETE FROM chat.user_profiles WHERE user_id = $1`,
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st, userID); err != nil {
			return fmt.Errorf("purge: %.40s: %w", st, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}
