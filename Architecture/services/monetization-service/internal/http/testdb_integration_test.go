//go:build integration

package http

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// requireTestDSN is the same guard as internal/service/testdb_integration_test.go,
// duplicated here because a _test.go helper cannot be imported across
// packages. The test in this package bootstraps the whole schema into
// whatever database the DSN names, so the guard must run before the pool
// is even constructed. Keep the two copies in step.
func requireTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("MONETIZATION_POSTGRES_DSN does not parse: %v", err)
	}
	db := cfg.Database
	switch {
	case db == "":
		t.Fatalf("refusing to run integration tests: MONETIZATION_POSTGRES_DSN names no database; use a scratch database whose name ends in _test (e.g. monetization_it_test)")
	case db == "app":
		t.Fatalf("refusing to run integration tests against the live database %q; use a scratch database whose name ends in _test (e.g. monetization_it_test)", db)
	case !strings.HasSuffix(db, "_test"):
		t.Fatalf("refusing to run integration tests against database %q: the name must end in _test (e.g. monetization_it_test)", db)
	}
	return dsn
}
