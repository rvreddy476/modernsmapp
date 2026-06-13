package store

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base user-service schema on startup, then runs
// any migration files in `migrations` that haven't already been applied
// (tracked in `schema_migrations`). Pass nil for `migrations` to skip the
// migration phase.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply user schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "user-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply user migrations: %w", err)
		}
	}
	return nil
}
