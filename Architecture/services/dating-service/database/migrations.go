package database

import (
	"context"
	"embed"
	"fmt"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Numbered migrations (dating backend audit H2: the first is the outbox).
//
// setup.sql stays the every-boot schema script; files under migrations/ run
// once per database through shared/store/migrationrunner, recorded in
// schema_migrations under MigrationService, each in its own transaction and
// under dating-service's advisory lock (RunExclusive), after setup.sql.

// MigrationService is the schema_migrations.service value for dating-service.
const MigrationService = "dating-service"

//go:embed migrations/*.sql
var Migrations embed.FS

// ApplyMigrations applies every migration not yet recorded for dating-service.
func ApplyMigrations(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if err := migrationrunner.RunExclusive(ctx, db, MigrationService, Migrations, "migrations"); err != nil {
		return fmt.Errorf("apply dating migrations: %w", err)
	}
	return nil
}
