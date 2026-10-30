package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// compactionIndex is the partial index (migration 020) that makes each
// compaction batch an index range scan. Until it is built and valid,
// compaction does nothing: without it every batch would sort the whole
// intents table.
const compactionIndex = "idx_delivery_intents_uncompacted"

// indexReady reports whether chat.<name> exists and is valid.
func (s *ConversationStore) indexReady(ctx context.Context, name string) (bool, error) {
	var valid bool
	err := s.db.QueryRow(ctx, `
		SELECT i.indisvalid FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'chat' AND c.relname = $1`, name).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return valid, err
}

// CompactCompletedDeliveryIntents empties member_ids on up to limit delivery
// intents completed before `before` and returns how many it changed (audit
// M8, 30 Oct 2026). Every group message stored up to 1024 member UUIDs
// forever; after completion nothing needs them except a disappearing
// message's expiry, which reads them to clean the recipients' Scylla inbox
// projections (ExpireMessageRecords). So an intent is left alone while its
// message has an expiry that has not run yet, or ran after `before` (a retry
// of the inbox clean-up may still read the list).
//
// The row itself stays: unsend, the idempotency tombstone check and a
// replayed send all key on it. Returns 0 until compactionIndex is valid.
func (s *ConversationStore) CompactCompletedDeliveryIntents(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	if ready, err := s.indexReady(ctx, compactionIndex); err != nil || !ready {
		return 0, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.message_delivery_intents
		SET member_ids = '{}'::uuid[]
		WHERE idempotency_key IN (
			SELECT d.idempotency_key FROM chat.message_delivery_intents d
			WHERE d.completed_at IS NOT NULL AND d.completed_at < $1
			  AND d.member_ids <> '{}'::uuid[]
			  AND NOT EXISTS (
				SELECT 1 FROM chat.message_expiries e
				WHERE e.message_id = d.message_id
				  AND (e.deleted_at IS NULL OR e.deleted_at >= $1))
			ORDER BY d.completed_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)`, before, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
