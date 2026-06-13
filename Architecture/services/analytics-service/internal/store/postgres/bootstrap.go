package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base analytics-service schema (setup.sql),
// then runs any migration files in `migrations` not yet recorded in
// `schema_migrations`. Replaces the inline `ensureSchema` in main.go which
// duplicated DDL the migration files already encode. Set
// SCHEMA_MIGRATIONS_BASELINE=true on first run against an existing
// database to mark all migrations as applied without re-executing.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply analytics schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "analytics-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply analytics migrations: %w", err)
		}
	}
	return nil
}
