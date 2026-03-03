package inbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides Postgres-backed consumer deduplication.
// It ensures each (consumer_name, event_id) pair is processed at most once.
type Store struct {
	db    *pgxpool.Pool
	table string
}

// New creates an inbox Store.
// schemaPrefix is the Postgres schema prefix (e.g. "" for public, "orders" for orders.inbox_events).
func New(db *pgxpool.Pool, schemaPrefix string) *Store {
	table := "inbox_events"
	if schemaPrefix != "" {
		table = schemaPrefix + ".inbox_events"
	}
	return &Store{db: db, table: table}
}

// ErrAlreadyProcessed is returned when an event has already been processed.
var ErrAlreadyProcessed = errors.New("event already processed")

// TryProcess atomically checks dedup and marks the event as processed.
// Returns ErrAlreadyProcessed if the event was already seen.
// Call this at the start of your consumer handler; only proceed if no error.
func (s *Store) TryProcess(ctx context.Context, consumerName, eventID string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO `+s.table+` (consumer_name, event_id) VALUES ($1, $2)
		 ON CONFLICT (consumer_name, event_id) DO NOTHING`,
		consumerName, eventID,
	)
	if err != nil {
		return err
	}
	// Check if the row was actually inserted (not a duplicate)
	var exists bool
	err = s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM `+s.table+` WHERE consumer_name=$1 AND event_id=$2 AND processed_at >= NOW() - INTERVAL '1 second')`,
		consumerName, eventID,
	).Scan(&exists)
	if err != nil {
		// If check fails, assume it was a duplicate to be safe
		return err
	}
	if !exists {
		return ErrAlreadyProcessed
	}
	return nil
}

// IsProcessed checks (without inserting) whether an event has been processed.
func (s *Store) IsProcessed(ctx context.Context, consumerName, eventID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM `+s.table+` WHERE consumer_name=$1 AND event_id=$2)`,
		consumerName, eventID,
	).Scan(&exists)
	return exists, err
}

// MarkProcessed inserts the event as processed. Returns ErrAlreadyProcessed if duplicate.
func (s *Store) MarkProcessed(ctx context.Context, consumerName, eventID string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO `+s.table+` (consumer_name, event_id) VALUES ($1, $2)`,
		consumerName, eventID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyProcessed
		}
		return err
	}
	return nil
}

// MarkProcessedTx inserts the event as processed within an existing transaction.
func (s *Store) MarkProcessedTx(ctx context.Context, tx pgx.Tx, consumerName, eventID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO `+s.table+` (consumer_name, event_id) VALUES ($1, $2)`,
		consumerName, eventID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyProcessed
		}
		return err
	}
	return nil
}

// Cleanup deletes old processed events to prevent table bloat.
// Call this periodically (e.g., daily) with a retention duration like 7*24*time.Hour.
func (s *Store) Cleanup(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM `+s.table+` WHERE processed_at < NOW() - $1::interval`,
		olderThan.String(),
	)
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if n > 0 {
		slog.Info("inbox cleanup", "table", s.table, "deleted", n)
	}
	return n, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps pgconn.PgError; check error code 23505
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
