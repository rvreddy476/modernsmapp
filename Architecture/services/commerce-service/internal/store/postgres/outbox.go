// Package postgres — transactional outbox for commerce-service.
//
// HP1 from the commerce audit: replaced the request-path
// kafka.Writer.WriteMessages call with an insert into outbox_events.
// shared/outbox.Publisher polls this table and fans out to Kafka so
// the request goroutine never blocks on a Kafka outage / slow broker.
//
// Schema lives in migrations/005_outbox_and_perf.sql.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// EnqueueOutboxEvent inserts one row inside an existing transaction.
// Use this when the caller already runs a domain-write tx — that way the
// domain row and the outbox row commit atomically (or both roll back).
//
// idempotencyKey may be empty; the column is NULLABLE and only enforced
// unique when populated (idx_outbox_idempotency_key partial-unique).
func (s *Store) EnqueueOutboxEvent(ctx context.Context, tx pgx.Tx, eventType, partitionKey, idempotencyKey string, payload []byte) error {
	var idemp interface{}
	if idempotencyKey != "" {
		idemp = idempotencyKey
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_type, partition_key, payload, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		eventType, partitionKey, payload, idemp,
	)
	return err
}

// EnqueueOutboxEventPool is the non-tx variant for the existing fire-and-
// forget publish() call sites. Same idempotency_key semantics. Slightly
// less safe than the tx version (the domain write and the enqueue are
// separate statements) but still strictly better than the old synchronous
// kafka.Writer.WriteMessages, because:
//
//   - the request goroutine is no longer blocked on Kafka,
//   - retries / outages no longer drop events on the floor,
//   - the publisher batches + acks via at-least-once delivery.
func (s *Store) EnqueueOutboxEventPool(ctx context.Context, eventType, partitionKey, idempotencyKey string, payload []byte) error {
	var idemp interface{}
	if idempotencyKey != "" {
		idemp = idempotencyKey
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO outbox_events (event_type, partition_key, payload, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		eventType, partitionKey, payload, idemp,
	)
	return err
}
