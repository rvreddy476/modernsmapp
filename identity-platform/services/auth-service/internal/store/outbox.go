// auth.outbox_events: the transactional outbox insert, fetching unpublished
// events, marking them published, and the backlog gauge. Pure move out of
// users.go; behaviour is unchanged.

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- auth.outbox_events ---

func (s *Store) InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth.outbox_events (event_type, partition_key, payload)
		VALUES ($1, $2, $3)
	`, eventType, partitionKey, data)
	return err
}

func (s *Store) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, payload, created_at
		FROM auth.outbox_events
		WHERE published_at IS NULL
		ORDER BY id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.outbox_events SET published_at = NOW() WHERE id = $1
	`, id)
	return err
}

// OutboxBacklog returns the count of unpublished outbox events and the
// creation time of the oldest. A growing backlog, or an old oldest-event,
// means the relay (or Kafka) has stalled — user.registered events are not
// reaching downstream projections, and drift will accumulate.
func (s *Store) OutboxBacklog(ctx context.Context) (count int, oldest time.Time, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(MIN(created_at), NOW())
		FROM auth.outbox_events WHERE published_at IS NULL
	`).Scan(&count, &oldest)
	return count, oldest, err
}
