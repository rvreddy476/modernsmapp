package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// DatingFirstMessageNotification is the durable obligation to tell
// dating-service that a dating-match conversation received its first message
// (Dating lane D4). Keyed by conversation: at most one per conversation, ever.
type DatingFirstMessageNotification struct {
	ConversationID uuid.UUID
	MatchID        uuid.UUID
	ActorID        uuid.UUID
	MessageID      uuid.UUID
	AttemptCount   int
	CreatedAt      time.Time
}

// EnqueueDatingFirstMessage records the obligation for the conversation's
// first delivered message. Later messages (and replays of the same delivery)
// hit the primary key and change nothing — in particular they never re-arm a
// row that was already delivered or given up on. Reports whether this call
// created the row.
func (s *ConversationStore) EnqueueDatingFirstMessage(ctx context.Context, n DatingFirstMessageNotification) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO chat.dating_first_message_notifications
			(conversation_id, match_id, actor_id, message_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (conversation_id) DO NOTHING
	`, n.ConversationID, n.MatchID, n.ActorID, n.MessageID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimDueDatingFirstMessages leases up to [limit] pending obligations whose
// next attempt is due. FOR UPDATE SKIP LOCKED keeps replicas off each other's
// rows; pushing next_attempt_at out by [lease] means a crashed worker's claim
// comes back on its own. attempt_count increments on claim so backoff grows
// even when a worker dies mid-call.
func (s *ConversationStore) ClaimDueDatingFirstMessages(ctx context.Context, limit int, lease time.Duration) ([]DatingFirstMessageNotification, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE chat.dating_first_message_notifications
		SET next_attempt_at = now() + make_interval(secs => $2),
		    attempt_count   = attempt_count + 1
		WHERE conversation_id IN (
			SELECT conversation_id FROM chat.dating_first_message_notifications
			WHERE delivered_at IS NULL AND terminal_at IS NULL
			  AND next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING conversation_id, match_id, actor_id, message_id, attempt_count, created_at
	`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatingFirstMessageNotification
	for rows.Next() {
		var n DatingFirstMessageNotification
		if err := rows.Scan(&n.ConversationID, &n.MatchID, &n.ActorID, &n.MessageID, &n.AttemptCount, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkDatingFirstMessageDelivered retires the obligation after dating-service
// accepted it.
func (s *ConversationStore) MarkDatingFirstMessageDelivered(ctx context.Context, conversationID uuid.UUID, status int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.dating_first_message_notifications
		SET delivered_at = COALESCE(delivered_at, now()), last_status = $2, last_error = NULL
		WHERE conversation_id = $1
	`, conversationID, status)
	return err
}

// MarkDatingFirstMessageTerminal retires the obligation after a permanent
// refusal (the match is gone, the path moved, the request is malformed).
// The row stays for diagnosis and is never claimed again.
func (s *ConversationStore) MarkDatingFirstMessageTerminal(ctx context.Context, conversationID uuid.UUID, status int, lastErr string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.dating_first_message_notifications
		SET terminal_at = COALESCE(terminal_at, now()), last_status = $2, last_error = NULLIF($3, '')
		WHERE conversation_id = $1
	`, conversationID, status, truncateError(lastErr))
	return err
}

// DeferDatingFirstMessage reschedules a failed attempt with the caller's
// backoff. status is 0 for a transport error.
func (s *ConversationStore) DeferDatingFirstMessage(ctx context.Context, conversationID uuid.UUID, retryIn time.Duration, status int, lastErr string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.dating_first_message_notifications
		SET next_attempt_at = now() + make_interval(secs => $2),
		    last_status = NULLIF($3, 0), last_error = NULLIF($4, '')
		WHERE conversation_id = $1
	`, conversationID, retryIn.Seconds(), status, truncateError(lastErr))
	return err
}

func truncateError(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
