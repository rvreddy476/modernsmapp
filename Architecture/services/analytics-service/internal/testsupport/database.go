//go:build integration

package testsupport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Why this exists.
//
// `go test -tags=integration ./...` runs one binary per package, and Go
// runs those binaries concurrently. Four analytics packages —
// aggregation, consumers, http and service — each opened
// ANALYTICS_POSTGRES_DSN, TRUNCATEd analytics.events_raw /
// ingest_receipts / content_ownership / content_hourly_agg, and then
// asserted against them. Two of the service tests count the whole table
// (`SELECT count(*) FROM analytics.events_raw`), and the aggregation
// tests recompute content_hourly_agg for every content id in an hour
// bucket, not just their own. So one package's truncate deleted
// another's fixtures mid-run and one package's fixtures broke another's
// counts. The suite only passed under `-p 1`.
//
// `-p 1` is a flag someone has to remember, on a suite that only gets
// slower. The isolation belongs in the fixture, so this hands each test
// package its own PostgreSQL database, created and schema-bootstrapped
// on first use and reused thereafter. Nothing is shared, so nothing one
// package truncates can be another package's data, and the global counts
// are honest again — they are global over a database only this package
// writes to.
//
// A database rather than a schema: every query in the service, and every
// line of setup.sql, names `analytics.<table>` explicitly, so a
// per-package schema would mean rewriting the production SQL to chase a
// search_path. A per-package database keeps the `analytics` schema
// exactly where the service expects it.
//
// Deliberately not an advisory lock. An earlier attempt at one deadlocked
// on a lock left behind by a killed run, and even a correct one
// serialises the very thing that should run in parallel. Two packages
// that never touch the same rows do not need to take turns.

// ErrNoDSN is returned when ANALYTICS_POSTGRES_DSN is unset. Callers skip.
var ErrNoDSN = errors.New("ANALYTICS_POSTGRES_DSN is required")

// dbNameSuffix restricts the caller-supplied package name to something
// that is safe to interpolate into CREATE DATABASE, which takes no
// parameters.
var dbNameSuffix = regexp.MustCompile(`^[a-z][a-z0-9_]{0,32}$`)

var (
	poolsMu sync.Mutex
	pools   = map[string]*pgxpool.Pool{}
)

// Pool returns a pool on the database private to this test package,
// creating and bootstrapping it the first time it is asked for. `pkg` is
// the calling package's name and becomes part of the database name, so
// it must be unique across the service and stable across runs.
//
// The pool is shared by every test in the package and is deliberately
// never closed: the package's tests all want the same connection, and
// the process exiting closes it.
func Pool(t *testing.T, pkg string) *pgxpool.Pool {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("ANALYTICS_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip(ErrNoDSN.Error())
	}
	if !dbNameSuffix.MatchString(pkg) {
		t.Fatalf("testsupport.Pool: %q is not a usable database-name suffix", pkg)
	}

	poolsMu.Lock()
	defer poolsMu.Unlock()
	if pool, ok := pools[pkg]; ok {
		return pool
	}

	ctx := context.Background()
	pool, err := openIsolated(ctx, dsn, "analytics_it_"+pkg)
	if err != nil {
		t.Fatalf("testsupport.Pool(%s): %v", pkg, err)
	}
	pools[pkg] = pool
	return pool
}

// openIsolated creates `dbName` beside the database the DSN points at,
// then returns a bootstrapped pool on it.
func openIsolated(ctx context.Context, dsn, dbName string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse ANALYTICS_POSTGRES_DSN: %w", err)
	}
	// The guard commerce-service learned to keep: the suite truncates,
	// so it must never be pointed at a database anything else uses. The
	// analytics_it_ prefix makes that structural rather than a promise,
	// and this catches a DSN that already names one of these.
	if cfg.ConnConfig.Database == dbName {
		return nil, fmt.Errorf("ANALYTICS_POSTGRES_DSN already points at %q; "+
			"it must name the ordinary database so the suite can create its own beside it", dbName)
	}

	if err := createDatabase(ctx, cfg, dbName); err != nil {
		return nil, err
	}

	target := cfg.Copy()
	target.ConnConfig.Database = dbName
	pool, err := pgxpool.NewWithConfig(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", dbName, err)
	}
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		pool.Close()
		return nil, fmt.Errorf("bootstrap %s: %w", dbName, err)
	}
	return pool, nil
}

// createDatabase issues CREATE DATABASE on a maintenance connection.
// Already existing is success — the database is reused between runs, and
// each test still truncates what it owns.
//
// The retry is for the one way concurrent creation can collide: several
// packages start at once and PostgreSQL refuses to copy template1 while
// another CREATE DATABASE is reading it ("source database is being
// accessed by other users", 55006). The names differ, so this resolves
// by waiting rather than by locking.
func createDatabase(ctx context.Context, cfg *pgxpool.Config, dbName string) error {
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		return fmt.Errorf("connect for CREATE DATABASE: %w", err)
	}
	defer admin.Close()

	var lastErr error
	for attempt := range 20 {
		_, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`)
		if err == nil {
			return nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "42P04": // duplicate_database — someone got there first, or a previous run did
				return nil
			case "55006", "23505", "42710": // template busy / racing catalog insert
				lastErr = err
				time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
				continue
			}
		}
		return fmt.Errorf("create %s: %w", dbName, err)
	}
	return fmt.Errorf("create %s: gave up after retries: %w", dbName, lastErr)
}
