package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base trust-safety schema on startup so a fresh
// database has the reports tables before moderation traffic arrives.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply trust-safety schema: %w", err)
	}
	return nil
}

// RunMigrations applies each incremental migration SQL in order. Every migration
// uses IF NOT EXISTS / IF EXISTS guards so re-runs on an already-migrated
// database are safe.
func RunMigrations(ctx context.Context, db *pgxpool.Pool, migrations []string) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	for i, m := range migrations {
		if strings.TrimSpace(m) == "" {
			continue
		}
		if _, err := db.Exec(ctx, m); err != nil {
			return fmt.Errorf("trust-safety migration %d failed: %w", i+1, err)
		}
	}
	return nil
}
