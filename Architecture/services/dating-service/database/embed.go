package database

import (
	"context"
	"fmt"
	"strings"

	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed setup.sql
var SetupSQL string

// BootstrapSchema applies the dating-service schema on startup so a fresh
// database has the Pulse tables before runtime consumers start.
//
// Mirrors the qa-service pattern (see qa-service/internal/store/postgres/bootstrap.go).
// NOTE: no migration runner yet — `database/migrations/` is empty. Wire the
// migrationrunner here when the first migration file is added.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(SetupSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, SetupSQL); err != nil {
		return fmt.Errorf("apply dating schema: %w", err)
	}
	return nil
}
