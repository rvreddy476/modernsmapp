package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OutboxEvent represents a transactional outbox record for reliable Kafka publishing.
type OutboxEvent struct {
	ID            uuid.UUID       `json:"id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	Published     bool            `json:"published"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// InsertOutboxEvent inserts a new outbox event within the given transaction.
func InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, aggregateType string, aggregateID uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, eventType, aggregateType, aggregateID, payloadBytes)
	return err
}

// InsertOutboxEvent inserts a new outbox event (non-transactional convenience).
func (s *Store) InsertOutboxEvent(ctx context.Context, eventType, aggregateType string, aggregateID uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, eventType, aggregateType, aggregateID, payloadBytes)
	return err
}

// GetUnpublishedOutboxEvents returns unpublished outbox events ordered by creation time.
func (s *Store) GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, aggregate_type, aggregate_id, payload, published, published_at, created_at
		FROM post_outbox_events
		WHERE published = FALSE
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.AggregateType, &e.AggregateID,
			&e.Payload, &e.Published, &e.PublishedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// MarkOutboxEventPublished marks an outbox event as successfully published.
func (s *Store) MarkOutboxEventPublished(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE post_outbox_events SET published = TRUE, published_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

// CleanupOldOutboxEvents removes published outbox events older than the retention period.
func (s *Store) CleanupOldOutboxEvents(ctx context.Context, retentionHours int) (int64, error) {
	// make_interval(hours => $1) takes an int directly — avoids the
	// ($1 || ' hours') text concatenation that errored on the int arg
	// ("cannot encode 48 into text").
	tag, err := s.db.Exec(ctx, `
		DELETE FROM post_outbox_events
		WHERE published = TRUE AND published_at < NOW() - make_interval(hours => $1)
	`, retentionHours)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
