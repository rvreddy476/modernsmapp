package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the live-service-v2 schema on startup so a
// fresh database is usable without a manual SQL step. Statements are
// split on `;` and applied one at a time so the failure log points at
// the offending statement.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
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
	return nil
}
