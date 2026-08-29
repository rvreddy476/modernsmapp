package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PreviewRepairObligation is the durable record that a deleted message's
// denormalized inbox preview still needs repairing (MP-LB-1). It is written
// BEFORE the Scylla soft delete and removed only after the repair verifiably
// resolved, so no failure between those points can strand deleted plaintext
// on the inbox.
type PreviewRepairObligation struct {
	MessageID      uuid.UUID
	ConversationID uuid.UUID
	Bucket         string
	DeletedTs      time.Time
	AttemptCount   int
	CreatedAt      time.Time
	NextAttemptAt  time.Time
}

// CreatePreviewRepairObligation durably records the repair debt. Upsert on
// message_id: replaying the same deletion re-arms the existing obligation
// rather than duplicating it. The initial next_attempt_at is deferred by
// [initialRepairGrace] so the background worker does not race the deleting
// request's own inline repair.
func (s *ConversationStore) CreatePreviewRepairObligation(ctx context.Context, conversationID, messageID uuid.UUID, bucket string, deletedTs time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.preview_repair_obligations
			(message_id, conversation_id, bucket, deleted_ts, next_attempt_at)
		VALUES ($1, $2, $3, $4, now() + $5::interval)
		ON CONFLICT (message_id) DO UPDATE
			SET next_attempt_at = now() + $5::interval
	`, messageID, conversationID, bucket, deletedTs, initialRepairGrace.String())
	return err
}

// initialRepairGrace is how long the worker leaves a fresh obligation to the
// deleting request's inline repair before treating it as abandoned.
const initialRepairGrace = 30 * time.Second

// ClaimDuePreviewRepairObligations leases up to [limit] due obligations for
// this worker pass. FOR UPDATE SKIP LOCKED makes concurrent replicas skip
// rather than block on each other's rows, and pushing next_attempt_at forward
// by [lease] means a claim survives a worker crash only as long as the lease —
// after which any replica may re-claim it. attempt_count increments on claim,
// so retry backoff grows even when the worker dies mid-attempt.
func (s *ConversationStore) ClaimDuePreviewRepairObligations(ctx context.Context, limit int, lease time.Duration) ([]PreviewRepairObligation, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE chat.preview_repair_obligations
		SET next_attempt_at = now() + $2::interval,
		    attempt_count   = attempt_count + 1
		WHERE message_id IN (
			SELECT message_id FROM chat.preview_repair_obligations
			WHERE next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING message_id, conversation_id, bucket, deleted_ts,
		          attempt_count, created_at, next_attempt_at
	`, limit, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreviewRepairObligation
	for rows.Next() {
		var o PreviewRepairObligation
		if err := rows.Scan(
			&o.MessageID, &o.ConversationID, &o.Bucket, &o.DeletedTs,
			&o.AttemptCount, &o.CreatedAt, &o.NextAttemptAt,
		); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CompletePreviewRepairObligation retires a resolved obligation.
func (s *ConversationStore) CompletePreviewRepairObligation(ctx context.Context, messageID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.preview_repair_obligations WHERE message_id = $1`, messageID)
	return err
}

// DeferPreviewRepairObligation reschedules a failed attempt with the caller's
// backoff and records why, so a stuck obligation is diagnosable from the row.
func (s *ConversationStore) DeferPreviewRepairObligation(ctx context.Context, messageID uuid.UUID, retryIn time.Duration, lastErr string) error {
	if len(lastErr) > 500 {
		lastErr = lastErr[:500]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE chat.preview_repair_obligations
		SET next_attempt_at = now() + $2::interval, last_error = NULLIF($3, '')
		WHERE message_id = $1
	`, messageID, retryIn.String(), lastErr)
	return err
}
