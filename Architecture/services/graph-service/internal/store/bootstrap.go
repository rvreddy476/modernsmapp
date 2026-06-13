package store

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base graph-service schema (setup.sql), then
// runs any migration files in `migrations` not yet recorded in
// `schema_migrations`. graph-service previously relied on the Postgres
// init scripts in Architecture/docker/database/02-app-db.sql; this hooks
// it into the same drift-protection story every other service now uses.
// Set SCHEMA_MIGRATIONS_BASELINE=true on first run against an existing
// database to mark all migrations as applied without re-executing.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply graph schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "graph-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply graph migrations: %w", err)
		}
	}
	return nil
}
