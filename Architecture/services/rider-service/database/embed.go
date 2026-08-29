// Package database embeds the rider-service SQL schema and applies it on
// service startup. Mirrors the wallet-service / dating-service pattern.
package database

import (
	"context"
	"embed"
	"fmt"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/*.sql
var Migrations embed.FS

// BootstrapSchema runs tracked versioned migrations via migrationrunner.
// Ensures migrations run in order and exactly once per database.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if err := migrationrunner.Run(ctx, db, "rider-service", Migrations, "migrations"); err != nil {
		return fmt.Errorf("apply rider migrations: %w", err)
	}
	return nil
}

