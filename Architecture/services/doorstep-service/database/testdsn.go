package database

import (
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TestDatabaseName is the only database the integration suites may touch.
// They truncate every doorstep table before each run, so pointing them at
// the shared `app` database would erase live dev data.
const TestDatabaseName = "doorstep_it_test"

// RequireTestDatabase refuses a DSN whose database is not exactly
// doorstep_it_test (stricter than the repo-wide "_test" suffix rule).
func RequireTestDatabase(dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse test dsn: %w", err)
	}
	if cfg.Database != TestDatabaseName {
		return fmt.Errorf("refusing to run integration tests against database %q: only %s is allowed", cfg.Database, TestDatabaseName)
	}
	return nil
}
