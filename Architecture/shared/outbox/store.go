package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ClaimColumns are the outbox_events columns the leased relay needs. Each
// service adds them with an additive migration (nullable or constant
// default, IF NOT EXISTS):
//
//	claimed_until  TIMESTAMPTZ          lease of the claiming relay; also the
//	                                    back-off of a refused row
//	attempts       INT NOT NULL DEFAULT 0  counted failed publishes
//	last_error     TEXT                 why the last publish failed (error
//	                                    text, never the payload)
//	failed_at      TIMESTAMPTZ          dead letter: never published, skipped
var ClaimColumns = []string{"claimed_until", "attempts", "last_error", "failed_at"}

// MigrationSQL returns the additive DDL that gives table (as Publisher names
// it: "outbox_events" or "<schema>.outbox_events") the ClaimColumns. Each
// service ships it as a numbered migration of its own.
func MigrationSQL(table string) string {
	return `ALTER TABLE ` + table + `
    ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempts      INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error    TEXT,
    ADD COLUMN IF NOT EXISTS failed_at     TIMESTAMPTZ;`
}

// pgStore is the relayStore over a service's outbox_events table.
type pgStore struct {
	db      *pgxpool.Pool
	schema  string // "" = the connection's current schema
	table   string
	lockKey int64

	mu        sync.Mutex
	leased    bool      // the table has ClaimColumns (final once true)
	checkedAt time.Time // last lookup; zero = never
}

// legacyRecheck is how often a table found without ClaimColumns is looked
// up again, so a migration applied while the pod runs is picked up.
const legacyRecheck = time.Minute

func newPGStore(db *pgxpool.Pool, schema, table string, lockKey int64) *pgStore {
	return &pgStore{db: db, schema: schema, table: table, lockKey: lockKey}
}

// leasedMode reports whether the table has ClaimColumns. Once found they are
// assumed to stay (the migration is additive); until then the lookup repeats
// every legacyRecheck. A table without them is relayed in legacy mode: plain
// SELECT, no claims, no dead letters; the advisory lock still keeps it to one
// relay at a time.
func (s *pgStore) leasedMode(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leased || (!s.checkedAt.IsZero() && time.Since(s.checkedAt) < legacyRecheck) {
		return s.leased, nil
	}
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = COALESCE(NULLIF($1, ''), current_schema())
		  AND table_name = 'outbox_events'
		  AND column_name = ANY($2)`, s.schema, ClaimColumns).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("look up outbox claim columns: %w", err)
	}
	leased := n == len(ClaimColumns)
	first := s.checkedAt.IsZero()
	s.leased, s.checkedAt = leased, time.Now()
	if !leased && first {
		slog.Error("outbox table has no claim columns; relaying in legacy mode (no leases, no dead letters) until the service's outbox migration runs",
			"table", s.table, "found", n, "want", len(ClaimColumns))
	}
	return leased, nil
}

// tryLock takes the table's relay advisory lock on a dedicated connection.
// If the process dies, the connection closes and Postgres drops the lock.
func (s *pgStore) tryLock(ctx context.Context) (func(), bool, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, s.lockKey).Scan(&got); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), detachedWriteTimeout)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, s.lockKey); err != nil {
			// The lock is session-scoped: close the connection rather
			// than return a pooled session that still holds it.
			_ = conn.Conn().Close(unlockCtx)
		}
		conn.Release()
	}, true, nil
}

func (s *pgStore) claimSQL() string {
	return `
	UPDATE ` + s.table + ` o
	SET claimed_until = NOW() + make_interval(secs => $2::float8)
	WHERE o.id IN (
		SELECT id FROM ` + s.table + `
		WHERE published_at IS NULL AND failed_at IS NULL
		  AND (claimed_until IS NULL OR claimed_until <= NOW())
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	)
	RETURNING o.id, o.event_type, o.partition_key, o.payload`
}

func (s *pgStore) legacySelectSQL() string {
	return `SELECT id, event_type, partition_key, payload
		FROM ` + s.table + `
		WHERE published_at IS NULL
		ORDER BY id ASC
		LIMIT $1`
}

func (s *pgStore) claim(ctx context.Context, limit int, lease time.Duration) ([]outboxRow, error) {
	leased, err := s.leasedMode(ctx)
	if err != nil {
		return nil, err
	}
	var rows pgx.Rows
	if leased {
		rows, err = s.db.Query(ctx, s.claimSQL(), limit, lease.Seconds())
	} else {
		rows, err = s.db.Query(ctx, s.legacySelectSQL(), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.EventType, &r.PartitionKey, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *pgStore) markPublished(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	leased, err := s.leasedMode(ctx)
	if err != nil {
		return err
	}
	q := `UPDATE ` + s.table + ` SET published_at = NOW() WHERE id = ANY($1) AND published_at IS NULL`
	if leased {
		q = `UPDATE ` + s.table + ` SET published_at = NOW(), claimed_until = NULL WHERE id = ANY($1) AND published_at IS NULL`
	}
	_, err = s.db.Exec(ctx, q, ids)
	return err
}

func (s *pgStore) release(ctx context.Context, ids []int64, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	leased, err := s.leasedMode(ctx)
	if err != nil || !leased {
		return err // legacy rows carry no claim
	}
	_, err = s.db.Exec(ctx, `
		UPDATE `+s.table+` SET claimed_until = NULL, last_error = $2
		WHERE id = ANY($1) AND published_at IS NULL`, ids, truncate(reason))
	return err
}

// recordFailureSQL: $1 id, $2 reason, $3 permanent, $4 max attempts. Every
// call is a counted or permanent failure. Returns whether the row is now
// dead-lettered.
func (s *pgStore) recordFailureSQL() string {
	return `
	UPDATE ` + s.table + `
	SET attempts = attempts + 1,
	    last_error = $2,
	    failed_at = COALESCE(failed_at,
	        CASE WHEN $3::bool OR attempts + 1 >= $4 THEN NOW() END),
	    claimed_until = CASE
	        WHEN failed_at IS NOT NULL OR $3::bool OR attempts + 1 >= $4 THEN NULL
	        ELSE NOW() + LEAST(INTERVAL '5 minutes', INTERVAL '1 second' * power(2, LEAST(attempts, 16)))
	    END
	WHERE id = $1 AND published_at IS NULL
	RETURNING failed_at IS NOT NULL`
}

func (s *pgStore) recordFailure(ctx context.Context, id int64, f failure, maxAttempts int) (bool, error) {
	leased, err := s.leasedMode(ctx)
	if err != nil {
		return false, err
	}
	if !leased {
		return false, errors.New("outbox table has no claim columns; cannot record the failure")
	}
	var dead bool
	err = s.db.QueryRow(ctx, s.recordFailureSQL(), id, truncate(f.Reason), f.Permanent, maxAttempts).Scan(&dead)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // published meanwhile
	}
	return dead, err
}

func truncate(s string) string {
	if len(s) > maxErrorText {
		return s[:maxErrorText]
	}
	return s
}
