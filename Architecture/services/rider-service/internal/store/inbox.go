package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ClaimInboxEvent attempts to claim an incoming event for idempotent execution.
func (s *Store) ClaimInboxEvent(ctx context.Context, consumerName, eventID string, revision int64) (bool, error) {
	const q = `
        INSERT INTO rider_consumer_inbox (consumer_name, event_id, aggregate_revision)
        VALUES ($1, $2, $3)
        ON CONFLICT (consumer_name, event_id) DO NOTHING`
	tag, err := s.db.Exec(ctx, q, consumerName, eventID, revision)
	if err != nil {
		return false, fmt.Errorf("claim inbox event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ClaimInboxEventTx attempts to claim an incoming event inside an active pgx.Tx.
func (s *Store) ClaimInboxEventTx(ctx context.Context, tx pgx.Tx, consumerName, eventID string, revision int64) (bool, error) {
	return ClaimInboxEventTx(ctx, tx, consumerName, eventID, revision)
}

// ClaimInboxEventTx attempts to claim an incoming event inside an active pgx.Tx.
func ClaimInboxEventTx(ctx context.Context, tx pgx.Tx, consumerName, eventID string, revision int64) (bool, error) {
	const q = `
        INSERT INTO rider_consumer_inbox (consumer_name, event_id, aggregate_revision)
        VALUES ($1, $2, $3)
        ON CONFLICT (consumer_name, event_id) DO NOTHING`
	tag, err := tx.Exec(ctx, q, consumerName, eventID, revision)
	if err != nil {
		return false, fmt.Errorf("claim inbox event tx: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

