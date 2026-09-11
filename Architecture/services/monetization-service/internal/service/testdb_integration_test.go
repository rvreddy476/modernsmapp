//go:build integration

package service

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bootstrapOnce applies the monetization schema to the scratch database the
// first time a test in this package asks for the DSN, the same way the
// http package's tests and the server itself do (BootstrapSchema: setup.sql,
// then the migrations not yet recorded in schema_migrations). Until 12 Sep
// 2026 this package assumed the schema was already there, which was true of
// the developer's long-lived scratch database and false of every fresh one —
// CI included, where these suites had never run.
//
// The analytics schema (content_daily_summary, the v1 contract view) is NOT
// applied here: it belongs to analytics-service and is applied by whoever
// prepares the database, exactly as in a deploy.
var bootstrapOnce sync.Once

// requireTestDSN returns MONETIZATION_POSTGRES_DSN, skipping the test when
// it is unset, and refusing to proceed unless the database it names is a
// scratch database.
//
// Every integration test in this package writes fixtures through the real
// store: rate rows, quality bands, daily summaries, earnings, ledger legs.
// On 2026-09-11 the dev stack's live `app` database was found carrying 25
// rate rows and 25 band rows tagged 'integration test window', plus 19
// settled creator_fund_earnings rows priced by them, because a test run
// had once been pointed at it. This is the guard the plan (Phase 0 item 3)
// asks for: the database name must end in "_test" and must never be "app".
//
// It runs BEFORE any connection is opened. Nothing is touched when it
// fires, not even a schema bootstrap. Keep it as the first statement of
// every test that reads the DSN. A copy lives in internal/http for the
// same reason; keep the two in step.
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
	bootstrapOnce.Do(func() {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("bootstrap: connect to the scratch database: %v", err)
		}
		defer pool.Close()
		if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
			t.Fatalf("bootstrap: apply the monetization schema to the scratch database: %v", err)
		}
	})
	return dsn
}
