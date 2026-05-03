// Package database embeds the rider-service SQL schema and applies it on
// service startup. Mirrors the wallet-service / dating-service pattern.
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

// BootstrapSchema applies the rider-service schema. Idempotent: every CREATE
// uses IF NOT EXISTS (or DO blocks for enum types), so this can be run on
// every cold start.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(SetupSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, SetupSQL); err != nil {
		return fmt.Errorf("apply rider schema: %w", err)
	}
	return nil
}
