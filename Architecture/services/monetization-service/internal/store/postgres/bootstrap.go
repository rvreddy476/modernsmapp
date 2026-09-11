package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// serviceName keys the advisory lock every replica of this service contends
// on while it applies schema; a second service sharing the database gets its
// own key from its own name.
const serviceName = "monetization-service"

// BootstrapSchema applies the base monetization-service schema, then runs any
// migration files in `migrations` not yet recorded in `schema_migrations`.
//
// Both steps run under the service's advisory lock, so two processes
// bootstrapping the same database — two replicas booting together, or two
// test packages sharing one fresh scratch database — take turns. Before
// 12 Sep 2026 setup.sql ran through the pool with no lock and the migrations
// through the non-exclusive runner: `IF NOT EXISTS` let both racers past the
// existence check and the loser died on `pg_type_typname_nsp_index`
// (observed on a fresh database with the http and service test packages
// running side by side). The second holder finds everything already applied
// and does nothing, which is the whole point.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	err := migrationrunner.WithLock(ctx, db, serviceName, func(ctx context.Context, conn *pgxpool.Conn) error {
		// On the LOCKED connection: the pool would hand the statement to a
		// session that holds nothing.
		_, err := conn.Exec(ctx, schemaSQL)
		return err
	})
	if err != nil {
		return fmt.Errorf("apply monetization schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.RunExclusive(ctx, db, serviceName, migrations, "migrations"); err != nil {
			return fmt.Errorf("apply monetization migrations: %w", err)
		}
	}
	return nil
}
