package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// DLQEntry is one Kafka event the consumer failed to process. Capturing it
// (instead of dropping it with a log line) makes failures inspectable and
// replayable after the underlying bug is fixed.
type DLQEntry struct {
	ID         int64      `json:"id"`
	Topic      string     `json:"topic"`
	EventType  string     `json:"event_type"`
	Payload    string     `json:"payload"`
	Error      string     `json:"error"`
	FailedAt   time.Time  `json:"failed_at"`
	ReplayedAt *time.Time `json:"replayed_at,omitempty"`
}

// InsertDLQ records a failed event. payload is the raw message bytes as text
// (it may not be valid JSON, which is itself a reason for the failure).
func (s *Store) InsertDLQ(ctx context.Context, topic, eventType, payload, errMsg string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO consumer_dlq (topic, event_type, payload, error)
		VALUES ($1, $2, $3, $4)`, topic, eventType, payload, errMsg)
	return err
}

// ListDLQ returns DLQ entries newest-first. When onlyUnreplayed is true only
// entries not yet successfully replayed are returned.
func (s *Store) ListDLQ(ctx context.Context, onlyUnreplayed bool, limit int) ([]DLQEntry, error) {
	q := `SELECT id, topic, event_type, payload, error, failed_at, replayed_at
	      FROM consumer_dlq`
	if onlyUnreplayed {
		q += ` WHERE replayed_at IS NULL`
	}
	q += ` ORDER BY id DESC LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DLQEntry
	for rows.Next() {
		var e DLQEntry
		if err := rows.Scan(&e.ID, &e.Topic, &e.EventType, &e.Payload, &e.Error, &e.FailedAt, &e.ReplayedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetDLQ returns one entry, or (nil, nil) when it does not exist.
func (s *Store) GetDLQ(ctx context.Context, id int64) (*DLQEntry, error) {
	var e DLQEntry
	err := s.db.QueryRow(ctx, `
		SELECT id, topic, event_type, payload, error, failed_at, replayed_at
		FROM consumer_dlq WHERE id = $1`, id).
		Scan(&e.ID, &e.Topic, &e.EventType, &e.Payload, &e.Error, &e.FailedAt, &e.ReplayedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// MarkDLQReplayed stamps an entry as replayed.
func (s *Store) MarkDLQReplayed(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE consumer_dlq SET replayed_at = now() WHERE id = $1`, id)
	return err
}

// CountDLQ returns the number of not-yet-replayed DLQ entries — a health
// signal: anything above zero means events failed and need attention.
func (s *Store) CountDLQ(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM consumer_dlq WHERE replayed_at IS NULL`).Scan(&n)
	return n, err
}
