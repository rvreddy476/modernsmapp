// Package database embeds the rider-service SQL schema and applies it on
// service startup. Mirrors the wallet-service / dating-service pattern.
package database

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/*.sql
var Migrations embed.FS

// migrationService keys both the schema_migrations rows and the advisory
// lock; the server and the cron binary share it.
const migrationService = "rider-service"

// BootstrapSchema applies the idempotent baseline (setup.sql), then the
// tracked versioned migrations, in that order and every boot.
//
// The baseline is not optional: migrations/001 onwards ALTER and reference
// tables only setup.sql creates (rider_cities, rider_rides, ...). The Mopedu
// port moved bootstrap to the runner alone, which worked only on databases
// that had already run setup.sql; an empty database failed on
// `relation "rider_cities" does not exist`. setup.sql stays re-runnable
// against a migrated schema (IF NOT EXISTS, guarded DO blocks, seeds on
// conflict), so running it every boot is a no-op once applied.
//
// The server and the cron binary both bootstrap, so both steps hold the
// per-service advisory lock: the baseline under WithLock (IF NOT EXISTS is
// not a mutual exclusion; two sessions racing a CREATE TYPE lose on
// pg_type_typname_nsp_index), the migrations under RunExclusive. They are
// taken one after the other, never nested: RunExclusive acquires its own
// connection and would wait forever behind a lock this boot already holds.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(SetupSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	err := migrationrunner.WithLock(ctx, db, migrationService, func(ctx context.Context, conn *pgxpool.Conn) error {
		// On the LOCKED connection: the pool would hand the statement to a
		// session that holds nothing.
		_, err := conn.Exec(ctx, SetupSQL)
		return err
	})
	if err != nil {
		return fmt.Errorf("apply rider schema: %w", err)
	}
	if err := migrationrunner.RunExclusive(ctx, db, migrationService, Migrations, "migrations"); err != nil {
		return fmt.Errorf("apply rider migrations: %w", err)
	}
	return nil
}
