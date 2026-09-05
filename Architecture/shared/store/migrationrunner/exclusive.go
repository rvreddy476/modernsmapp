package migrationrunner

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunExclusive applies migrations under a PostgreSQL advisory lock so that
// only one process in the fleet can be migrating a given service at a time.
//
// Amendment A7 / review §6. `Run` discovers pending migrations and starts
// executing them with no mutual exclusion, so two replicas booting together
// both see the same pending list and both run it. For DDL that is usually
// survivable — `IF NOT EXISTS` absorbs it — but the P0 migrations carry
// backfills (`UPDATE … SET amount_minor = …`) and constraint validation,
// where a concurrent second runner is not idempotent and can deadlock
// against the first.
//
// The mechanism the review insisted on, and the reason each part matters:
//
//   - The lock is taken on ONE DEDICATED CONNECTION, acquired from the pool
//     and held for the whole run. `pg_advisory_lock` is session-scoped, so
//     taking it via `pool.Exec` locks whichever pooled session happened to
//     serve that call, and the migrations then execute on a DIFFERENT
//     session that holds nothing. That is the trap A7 names, and it is why
//     every statement below runs on `conn`, never on `db`.
//
//   - The applied set is RE-READ AFTER the lock is granted. The list read
//     before waiting is stale by definition: the process we queued behind
//     was, at that moment, applying the very migrations we enumerated.
//
//   - The lock is released on cancellation and on process death. The
//     explicit unlock covers a clean exit; releasing the connection back to
//     the pool and PostgreSQL's own session teardown cover a crash, because
//     a session-scoped advisory lock dies with its session.
//
// `Run` is left exactly as it was. This is an additive API: services that
// have not opted in keep their current behaviour, so the blast radius of
// this change is limited to the callers that switch.
func RunExclusive(ctx context.Context, db *pgxpool.Pool, service string, fsys fs.FS, subdir string) error {
	if db == nil {
		return fmt.Errorf("migrationrunner: nil db pool")
	}

	names, err := listMigrationFiles(fsys, subdir)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}

	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrationrunner: acquire dedicated connection: %w", err)
	}
	// Release returns the session to the pool. If we died before the
	// explicit unlock below, PostgreSQL drops the advisory lock when the
	// session ends, so a crashed migrator cannot wedge the fleet forever.
	defer conn.Release()

	key := advisoryKey(service)

	// Bound the wait. A migrator that hangs should fail its pod's startup
	// loudly rather than block silently behind a lock that is never coming.
	lockCtx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()

	slog.Info("migrationrunner: waiting for migration lock", "service", service, "key", key)
	if _, err := conn.Exec(lockCtx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("migrationrunner: acquire advisory lock for %s: %w", service, err)
	}
	slog.Info("migrationrunner: migration lock held", "service", service, "key", key)

	defer func() {
		// Use a fresh context: the caller's may already be cancelled, and
		// an unlock that is skipped because of cancellation would leave the
		// lock held until the session is reaped.
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			slog.Warn("migrationrunner: advisory unlock failed; the session will release it on close",
				"service", service, "error", err)
			return
		}
		slog.Info("migrationrunner: migration lock released", "service", service)
	}()

	if _, err := conn.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("migrationrunner: ensure schema_migrations: %w", err)
	}

	// Re-read AFTER the lock. Anything the previous holder applied while we
	// were queued must not be applied again.
	applied, err := loadAppliedConn(ctx, conn, service)
	if err != nil {
		return fmt.Errorf("migrationrunner: load applied: %w", err)
	}

	baseline := strings.EqualFold(os.Getenv("SCHEMA_MIGRATIONS_BASELINE"), "true")

	for _, name := range names {
		if applied[name] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("migrationrunner: cancelled before %s: %w", name, err)
		}
		path := subdir + "/" + name

		if baseline {
			if _, err := conn.Exec(ctx,
				`INSERT INTO schema_migrations (service, filename) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				service, name); err != nil {
				return fmt.Errorf("migrationrunner: baseline-mark %s: %w", name, err)
			}
			slog.Info("migration baselined (not executed)", "service", service, "migration", name)
			continue
		}

		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("migrationrunner: read %s: %w", path, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			if _, err := conn.Exec(ctx,
				`INSERT INTO schema_migrations (service, filename) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				service, name); err != nil {
				return fmt.Errorf("migrationrunner: mark empty %s: %w", name, err)
			}
			continue
		}

		// The migration and its bookkeeping row commit together, on the
		// same locked session. A migration that half-applied and was still
		// recorded is the failure mode proof C10 exists to catch.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrationrunner: begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrationrunner: apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (service, filename) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			service, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrationrunner: record %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrationrunner: commit %s: %w", name, err)
		}
		slog.Info("migration applied", "service", service, "migration", name)
	}
	return nil
}

// lockWait bounds how long a booting replica queues behind another
// migrator before failing its own startup.
var lockWait = 5 * time.Minute

// advisoryKey derives a stable 63-bit lock key from the service name, so
// two services sharing a database migrate independently while every replica
// of one service contends on the same key.
//
// The top bit is cleared because pg_advisory_lock takes a signed bigint and
// a negative key, while legal, reads as a bug in `pg_locks`.
func advisoryKey(service string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("migrationrunner:" + service))
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}

func loadAppliedConn(ctx context.Context, conn *pgxpool.Conn, service string) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT filename FROM schema_migrations WHERE service = $1`, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out[f] = true
	}
	return out, rows.Err()
}

// ErrLockTimeout is returned (wrapped) when the advisory lock could not be
// taken within lockWait.
var ErrLockTimeout = errors.New("migrationrunner: timed out waiting for the migration lock")
