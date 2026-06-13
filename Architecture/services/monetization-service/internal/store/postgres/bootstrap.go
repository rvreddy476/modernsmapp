package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base monetization-service schema, then runs any
// migration files in `migrations` not yet recorded in `schema_migrations`.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply monetization schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "monetization-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply monetization migrations: %w", err)
		}
	}
	return nil
}
