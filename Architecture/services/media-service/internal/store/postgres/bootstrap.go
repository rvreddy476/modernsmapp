package postgres

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the base media-service schema, then runs any migration
// files in `migrations` not yet recorded in `schema_migrations`.
//
// media-service runs two processes (server + worker) that both call this on
// boot. CREATE TABLE IF NOT EXISTS is not race-safe inside pg_type, so we
// serialise via a session advisory lock keyed to the service name. Whichever
// process gets the lock first applies the schema; the other waits, then runs
// the same statements as harmless no-ops via IF NOT EXISTS.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}

	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire bootstrap conn: %w", err)
	}
	defer conn.Release()

	lockKey := advisoryLockKey("media-service-bootstrap")
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("acquire bootstrap advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
	}()

	if _, err := conn.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply media schema: %w", err)
	}
	if migrations != nil {
		if err := migrationrunner.Run(ctx, db, "media-service", migrations, "migrations"); err != nil {
			return fmt.Errorf("apply media migrations: %w", err)
		}
	}
	return nil
}

func advisoryLockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}
