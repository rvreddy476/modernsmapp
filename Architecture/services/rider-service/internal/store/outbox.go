package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OutboxEvent is one row in rider_outbox_events.
type OutboxEvent struct {
	ID            uuid.UUID       `json:"id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
}

// InsertOutboxEventTx inserts a transactional outbox row within an open transaction.
func InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, aggregateType, aggregateID string, payload []byte) error {
	const q = `
        INSERT INTO rider_outbox_events (event_type, aggregate_type, aggregate_id, payload)
        VALUES ($1, $2, $3, $4)`
	_, err := tx.Exec(ctx, q, eventType, aggregateType, aggregateID, payload)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// FetchUnpublishedOutboxEvents retrieves batches of unpublished events with SKIP LOCKED.
func (s *Store) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `
        SELECT id, event_type, aggregate_type, aggregate_id, payload, created_at, published_at
        FROM rider_outbox_events
        WHERE published_at IS NULL
        ORDER BY created_at ASC
        LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch unpublished outbox: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.AggregateType, &e.AggregateID, &e.Payload, &e.CreatedAt, &e.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// MarkOutboxPublished marks an event as successfully published after broker ACK.
func (s *Store) MarkOutboxPublished(ctx context.Context, eventID uuid.UUID) error {
	const q = `UPDATE rider_outbox_events SET published_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, eventID)
	return err
}
