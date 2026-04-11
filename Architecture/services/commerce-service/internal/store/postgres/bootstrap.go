package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema runs the embedded setup.sql idempotently.
func BootstrapSchema(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("bootstrap commerce schema: %w", err)
	}
	return nil
}

// RunMigrations runs each migration SQL idempotently (best-effort, logged on error).
// Each migration uses IF NOT EXISTS / IF EXISTS guards so re-runs are safe.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrations []string) error {
	for i, m := range migrations {
		if _, err := pool.Exec(ctx, m); err != nil {
			return fmt.Errorf("migration %03d failed: %w", i+1, err)
		}
	}
	return nil
}
