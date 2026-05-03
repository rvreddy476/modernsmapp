// Package database embeds the bill-pay-service SQL schema and applies it on
// service startup. Mirrors wallet-service / dating-service.
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

// BootstrapSchema applies the bill-pay-service schema. Idempotent: every
// CREATE uses IF NOT EXISTS so the bootstrap can be re-run on every cold
// start. The seed INSERT uses ON CONFLICT (id) DO UPDATE so categories stay
// in sync across restarts.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(SetupSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if _, err := db.Exec(ctx, SetupSQL); err != nil {
		return fmt.Errorf("apply billpay schema: %w", err)
	}
	return nil
}
