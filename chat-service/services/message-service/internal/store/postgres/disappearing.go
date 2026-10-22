package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Disappearing messages (migration 013).
//
// The timer lives on chat.conversations (conversation-wide). Every message
// sent while it is on gets one chat.message_expiries row whose expires_at is
// fixed at send time; the expiry worker claims due rows from that table and
// never scans Scylla.

// ErrExpiryDeliveryPending is returned when a due message's durable delivery
// has not completed yet: expiring it now would let the delivery repair write
// the message back. The worker defers and retries.
var ErrExpiryDeliveryPending = errors.New("message delivery is still pending")

// MessageExpiry is one scheduled disappearance.
type MessageExpiry struct {
	MessageID      uuid.UUID
	ConversationID uuid.UUID
	Bucket         string
	MessageTS      time.Time
	SenderID       uuid.UUID
	ExpiresAt      time.Time
	AttemptCount   int
	// DeletedAt is set once the Postgres side of the delete-for-everyone ran.
	DeletedAt *time.Time
}

// DisappearTimer is the conversation's current setting.
type DisappearTimer struct {
	AfterMs   *int64
	UpdatedBy *uuid.UUID
	UpdatedAt *time.Time
}

// GetDisappearTimer reads the conversation-wide timer (AfterMs nil = off).
func (s *ConversationStore) GetDisappearTimer(ctx context.Context, conversationID uuid.UUID) (*DisappearTimer, error) {
	var t DisappearTimer
	err := s.db.QueryRow(ctx, `
		SELECT disappear_after_ms, disappear_updated_by, disappear_updated_at
		FROM chat.conversations WHERE id = $1
	`, conversationID).Scan(&t.AfterMs, &t.UpdatedBy, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &DisappearTimer{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SetDisappearTimer writes the conversation-wide timer (nil turns it off).
// Messages already sent keep the expiry they were given.
func (s *ConversationStore) SetDisappearTimer(ctx context.Context, conversationID uuid.UUID, afterMs *int64, updatedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET disappear_after_ms = $2, disappear_updated_by = $3, disappear_updated_at = NOW()
		WHERE id = $1
	`, conversationID, afterMs, updatedBy)
	return err
}

// ScheduleMessageExpiry records a message's fixed expiry. Replay-safe: the
// delivery repair re-runs it and the first row wins, so a replay can never
// move an expiry.
func (s *ConversationStore) ScheduleMessageExpiry(ctx context.Context, e MessageExpiry) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.message_expiries
			(message_id, conversation_id, bucket, message_ts, sender_id, expires_at, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (message_id) DO NOTHING
	`, e.MessageID, e.ConversationID, e.Bucket, e.MessageTS, e.SenderID, e.ExpiresAt)
	return err
}

// ClaimDueMessageExpiries leases up to limit rows whose next attempt is due.
// next_attempt_at starts at expires_at, so only expired messages are ever
// claimed. FOR UPDATE SKIP LOCKED lets replicas run side by side without
// blocking; pushing next_attempt_at forward by lease means a crashed
// worker's claim is picked up again once the lease runs out.
//
// The claim set is a MATERIALIZED CTE, not `WHERE message_id IN
// (subquery)`: the planner may rescan an IN subquery once per outer row,
// and each rescan re-runs LIMIT + SKIP LOCKED, skips the rows this
// statement just updated and picks fresh ones — leasing more rows than the
// limit (8 for a limit of 5 under a nested-loop plan).
func (s *ConversationStore) ClaimDueMessageExpiries(ctx context.Context, limit int, lease time.Duration) ([]MessageExpiry, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		WITH due AS MATERIALIZED (
			SELECT message_id FROM chat.message_expiries
			WHERE next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE chat.message_expiries e
		SET next_attempt_at = now() + $2::interval,
		    attempt_count   = e.attempt_count + 1
		FROM due
		WHERE e.message_id = due.message_id
		RETURNING e.message_id, e.conversation_id, e.bucket, e.message_ts, e.sender_id,
		          e.expires_at, e.attempt_count, e.deleted_at
	`, limit, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageExpiry
	for rows.Next() {
		var e MessageExpiry
		if err := rows.Scan(&e.MessageID, &e.ConversationID, &e.Bucket, &e.MessageTS, &e.SenderID,
			&e.ExpiresAt, &e.AttemptCount, &e.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompleteMessageExpiry removes a fully expired message's row.
func (s *ConversationStore) CompleteMessageExpiry(ctx context.Context, messageID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.message_expiries WHERE message_id = $1`, messageID)
	return err
}

// DeferMessageExpiry reschedules a failed attempt and records why.
func (s *ConversationStore) DeferMessageExpiry(ctx context.Context, messageID uuid.UUID, retryIn time.Duration, lastErr string) error {
	if len(lastErr) > 500 {
		lastErr = lastErr[:500]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE chat.message_expiries
		SET next_attempt_at = now() + $2::interval, last_error = NULLIF($3, '')
		WHERE message_id = $1
	`, messageID, retryIn.String(), lastErr)
	return err
}

// ExpireMessageRecords runs the Postgres side of a disappearing message's
// delete-for-everyone in ONE transaction, exactly once per message:
//
//   - the unread release unsend uses (same SQL) and the MessageDeleted
//     outbox event (deduped per message) — both skipped when alreadyGone
//     (a member unsent it first, so unsend already did them);
//   - the shared posts/media tab row, the canonical media reference (members
//     can no longer fetch the attachment through this message), stars, pins
//     and translations of it;
//   - the plaintext kept beside the message: the completed delivery intent's
//     text, quote and rich payload, the cached idempotent response, and the
//     text/preview inside the already-published MessageCreated outbox row;
//   - deleted_at on the expiry row, so a retry skips all of the above.
//
// It returns the message's recipients at send time (for the Scylla inbox
// projection clean-up). ErrExpiryDeliveryPending means the delivery has not
// completed and nothing was changed.
func (s *ConversationStore) ExpireMessageRecords(ctx context.Context, e MessageExpiry, alreadyGone bool, eventType string, eventPayload interface{}) ([]uuid.UUID, error) {
	expiredKey := "message-expired:" + e.MessageID.String()
	payload, err := s.sealOutboxPayload(ctx, eventType, expiredKey, eventPayload)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var deletedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT deleted_at FROM chat.message_expiries WHERE message_id = $1 FOR UPDATE`, e.MessageID).Scan(&deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Completed by another worker in the meantime.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var idempotencyKey string
	var completedAt *time.Time
	var memberIDs []uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT idempotency_key, completed_at, member_ids
		FROM chat.message_delivery_intents WHERE message_id = $1
	`, e.MessageID).Scan(&idempotencyKey, &completedAt, &memberIDs)
	hasIntent := true
	if errors.Is(err, pgx.ErrNoRows) {
		hasIntent = false
	} else if err != nil {
		return nil, err
	}
	if hasIntent && completedAt == nil {
		return nil, ErrExpiryDeliveryPending
	}
	if deletedAt != nil {
		return memberIDs, nil
	}

	type statement struct {
		sql  string
		args []any
	}
	var statements []statement
	if !alreadyGone {
		statements = append(statements,
			statement{releaseUnreadForUnsentSQL, []any{e.ConversationID, e.MessageID, e.SenderID, e.MessageTS}},
			statement{`INSERT INTO chat.outbox_events (dedupe_key, event_type, payload) VALUES ($1, $2, $3)
			  ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`,
				[]any{expiredKey, eventType, payload}},
		)
	}
	statements = append(statements,
		statement{`DELETE FROM chat.conversation_shared_items WHERE conversation_id = $1 AND message_id = $2`, []any{e.ConversationID, e.MessageID}},
		statement{`DELETE FROM chat.message_media_references WHERE message_id = $1 AND conversation_id = $2`, []any{e.MessageID, e.ConversationID}},
		statement{`DELETE FROM chat.starred_messages WHERE message_id = $1 AND conversation_id = $2`, []any{e.MessageID, e.ConversationID}},
		statement{`DELETE FROM chat.conversation_pins WHERE message_id = $1 AND conversation_id = $2`, []any{e.MessageID, e.ConversationID}},
		statement{`DELETE FROM chat.message_translations WHERE message_id = $1`, []any{e.MessageID}},
		statement{blankCompletedIntentSQL, []any{e.MessageID}},
		statement{stripPublishedMessageCreatedSQL, []any{"message-created:" + e.MessageID.String()}},
		statement{`UPDATE chat.message_expiries SET deleted_at = NOW() WHERE message_id = $1`, []any{e.MessageID}},
	)
	if hasIntent {
		// A NULL response makes a late client retry of the same key report
		// "in progress" instead of returning the vanished text.
		statements = append(statements, statement{`UPDATE chat.idempotency_keys SET response = NULL WHERE key = $1`, []any{idempotencyKey}})
	}
	for _, st := range statements {
		if _, err := tx.Exec(ctx, st.sql, st.args...); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return memberIDs, nil
}

// blankCompletedIntentSQL drops the content a completed delivery intent
// still carries ($1 = message id). An incomplete intent keeps it: repair
// still has to write the message.
const blankCompletedIntentSQL = `UPDATE chat.message_delivery_intents
		  SET message_text = '', reply_to_preview = '', extra = NULL
		  WHERE message_id = $1 AND completed_at IS NOT NULL`

// stripPublishedMessageCreatedSQL removes the text and preview from an
// already-published MessageCreated outbox row ($1 = its dedupe key). A legacy
// (plaintext JSON object) payload loses the two keys; a sealed payload is a
// JSON string that cannot be edited in SQL, and a published row is never
// read again, so it is replaced by an empty object.
const stripPublishedMessageCreatedSQL = `UPDATE chat.outbox_events
		  SET payload = CASE WHEN jsonb_typeof(payload) = 'object'
		                     THEN payload - 'text' - 'preview'
		                     ELSE '{}'::jsonb END
		  WHERE dedupe_key = $1 AND published_at IS NOT NULL`

// RedactUnsentMessageRecords is the Postgres side of unsend's content
// removal (22 Oct 2026; Scylla's is SoftDeleteMessage), mirroring what the
// disappearing-message path already did: the completed delivery intent's
// text, quote and rich payload, the cached send response (a NULL response
// makes a late retry of that key report "in progress" instead of returning
// the text), the text and preview inside the published MessageCreated outbox
// row, any translation of the message and the preview kept on stars of it
// (the stars themselves stay). Idempotent; one transaction.
func (s *ConversationStore) RedactUnsentMessageRecords(ctx context.Context, conversationID, messageID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var idempotencyKey string
	err = tx.QueryRow(ctx, `
		SELECT idempotency_key FROM chat.message_delivery_intents
		WHERE message_id = $1 AND conversation_id = $2 AND completed_at IS NOT NULL
	`, messageID, conversationID).Scan(&idempotencyKey)
	hasIntent := true
	if errors.Is(err, pgx.ErrNoRows) {
		hasIntent = false
	} else if err != nil {
		return err
	}
	type statement struct {
		sql  string
		args []any
	}
	statements := []statement{
		{blankCompletedIntentSQL, []any{messageID}},
		{stripPublishedMessageCreatedSQL, []any{"message-created:" + messageID.String()}},
		{`DELETE FROM chat.message_translations WHERE message_id = $1`, []any{messageID}},
		{`UPDATE chat.starred_messages SET message_preview = NULL WHERE message_id = $1 AND conversation_id = $2`, []any{messageID, conversationID}},
	}
	if hasIntent {
		statements = append(statements, statement{`UPDATE chat.idempotency_keys SET response = NULL WHERE key = $1`, []any{idempotencyKey}})
	}
	for _, st := range statements {
		if _, err := tx.Exec(ctx, st.sql, st.args...); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
