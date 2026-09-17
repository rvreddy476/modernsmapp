package database

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RequireTestDatabase refuses a DSN whose database name does not end in
// "_test". The integration suites truncate every rider table before each run,
// so pointing them at the shared `app` database would erase live dev data.
// Same rule as food-service's testhelpers guard.
func RequireTestDatabase(dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse test dsn: %w", err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		return fmt.Errorf("refusing to run integration tests against database %q: the name must end in _test", cfg.Database)
	}
	return nil
}
