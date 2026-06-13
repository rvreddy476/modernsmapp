package postgres

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema runs the embedded setup.sql idempotently, then applies any
// migration files in `migrations` not yet recorded in `schema_migrations`.
func BootstrapSchema(ctx context.Context, pool *pgxpool.Pool, sql string, migrations fs.FS) error {
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("bootstrap commerce schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, pool, "commerce-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply commerce migrations: %w", err)
		}
	}
	return nil
}
