package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base trust-safety schema on startup, then runs
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
		return fmt.Errorf("apply trust-safety schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "trust-safety-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply trust-safety migrations: %w", err)
		}
	}
	return nil
}
