package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the chat schema (database/setup.sql) so message-service
// starts cleanly against a fresh — or partially migrated — Postgres database.
//
// The whole script is executed in a single call via the simple query protocol:
// Postgres parses it, so multi-statement SQL, line comments (including ones
// that contain a ';'), and dollar-quoted blocks are all handled correctly. The
// previous implementation split on ';' by hand, which mis-parsed any ';' inside
// a comment or string literal. Every statement in setup.sql is idempotent
// (CREATE TABLE/INDEX IF NOT EXISTS, ADD COLUMN IF NOT EXISTS), so this is safe
// to run on every boot.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}

	if _, err := db.Exec(ctx, schemaSQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("apply chat schema: %w", err)
	}
	return nil
}
