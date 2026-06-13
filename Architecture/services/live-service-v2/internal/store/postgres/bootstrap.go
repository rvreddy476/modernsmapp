package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the live-service-v2 schema on startup so a fresh
// database is usable without a manual SQL step, then runs any migration files
// in `migrations` not yet recorded in `schema_migrations`. Statements in
// setup.sql are split on `;` and applied one at a time so failure logs point
// at the offending statement.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	for idx, statement := range strings.Split(schemaSQL, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply live-v2 schema statement %d: %w", idx+1, err)
		}
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "live-service-v2", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply live-v2 migrations: %w", err)
		}
	}
	return nil
}
