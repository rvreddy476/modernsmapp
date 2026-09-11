package migrationrunner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WithLock runs fn while holding the same per-service advisory lock that
// RunExclusive takes, on one dedicated connection, and releases it whether
// fn returns, fails or is cancelled.
//
// It exists for the DDL a service applies OUTSIDE its migration files —
// monetization-service's setup.sql, which it runs on every boot before the
// migrations. That file is idempotent once applied, but `IF NOT EXISTS` is
// not a mutual exclusion: two sessions racing the same CREATE TYPE or CREATE
// TABLE both pass the existence check and the loser dies on
// `pg_type_typname_nsp_index`. Observed 12 Sep 2026 with two test packages
// bootstrapping one fresh database; the same race waits for two replicas
// booting together. RunExclusive already serialised the migrations; this
// serialises the step before them, on the same key, so a boot is one
// critical section per service, not two.
//
// fn receives the locked connection and MUST do its work on it (or on
// transactions opened from it); work sent through the pool would run on a
// session that holds nothing, which is the trap RunExclusive's comment
// describes.
func WithLock(ctx context.Context, db *pgxpool.Pool, service string, fn func(ctx context.Context, conn *pgxpool.Conn) error) error {
	if db == nil {
		return fmt.Errorf("migrationrunner: nil db pool")
	}
	if fn == nil {
		return fmt.Errorf("migrationrunner: nil fn")
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrationrunner: acquire dedicated connection: %w", err)
	}
	defer conn.Release()

	key := advisoryKey(service)
	lockCtx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	slog.Info("migrationrunner: waiting for schema lock", "service", service, "key", key)
	if _, err := conn.Exec(lockCtx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("migrationrunner: acquire advisory lock for %s: %w", service, err)
	}
	slog.Info("migrationrunner: schema lock held", "service", service, "key", key)
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			slog.Warn("migrationrunner: advisory unlock failed; the session will release it on close",
				"service", service, "error", err)
			return
		}
		slog.Info("migrationrunner: schema lock released", "service", service)
	}()

	return fn(ctx, conn)
}
