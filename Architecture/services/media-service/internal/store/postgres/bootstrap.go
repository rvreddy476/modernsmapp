package postgres

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"strings"
	"time"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bootstrapAttempts bounds the retries on a transient DDL conflict. Other
// services share this database and some declare overlapping tables, so a
// simultaneous restart can deadlock one bootstrap (SQLSTATE 40P01); the
// advisory lock below only serialises media-service's own two processes.
const bootstrapAttempts = 5

// isTransientDDLConflict: a deadlock or serialization failure, which a
// retry resolves once the other service's DDL has committed.
func isTransientDDLConflict(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" || pgErr.Code == "40001"
	}
	return false
}

// BootstrapSchema applies the schema, retrying a transient DDL conflict
// with a short backoff instead of letting the process exit on boot.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
	var err error
	for attempt := 1; attempt <= bootstrapAttempts; attempt++ {
		err = bootstrapSchemaOnce(ctx, db, schemaSQL, migrations)
		if err == nil || !isTransientDDLConflict(err) || attempt == bootstrapAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	return err
}

// BootstrapSchema applies the base media-service schema, then runs any migration
// files in `migrations` not yet recorded in `schema_migrations`.
//
// media-service runs two processes (server + worker) that both call this on
// boot. CREATE TABLE IF NOT EXISTS is not race-safe inside pg_type, so we
// serialise via a session advisory lock keyed to the service name. Whichever
// process gets the lock first applies the schema; the other waits, then runs
// the same statements as harmless no-ops via IF NOT EXISTS.
func bootstrapSchemaOnce(ctx context.Context, db *pgxpool.Pool, schemaSQL string, migrations fs.FS) error {
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
