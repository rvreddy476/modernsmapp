//go:build integration

package postgres

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Two replicas booting together, or two test packages sharing one fresh
// scratch database, both run BootstrapSchema at once. Before the advisory
// lock they raced setup.sql's CREATE TYPE / CREATE TABLE past `IF NOT
// EXISTS` and one of them died on `pg_type_typname_nsp_index` (observed
// 12 Sep 2026 with the http and service suites side by side). This creates
// a database nothing has touched, bootstraps it from four goroutines, and
// expects every one of them to return nil and the schema to be there once.
func TestBootstrapSchemaSurvivesConcurrentBooters(t *testing.T) {
	dsn := requireScratchDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh database on the same server, dropped at the end. The name keeps
	// the _test suffix so the DSN guard below still holds for it.
	fresh := fmt.Sprintf("%s_boot%d_test", strings.TrimSuffix(cfg.Database, "_test"), rand.Intn(1_000_000))
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{fresh}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		_, _ = admin.Exec(dropCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{fresh}.Sanitize()+` WITH (FORCE)`)
		_ = admin.Close(dropCtx)
	})

	freshCfg := cfg.Copy()
	freshCfg.Database = fresh
	poolCfg, err := pgxpool.ParseConfig(freshCfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.ConnConfig.Database = fresh
	const booters = 4
	poolCfg.MaxConns = booters * 2
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var wg sync.WaitGroup
	errs := make([]error, booters)
	start := make(chan struct{})
	for i := 0; i < booters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("booter %d: %v", i, err)
		}
	}

	var tables int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('creator_ledger', 'creator_fund_earnings', 'schema_migrations')`,
	).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 3 {
		t.Fatalf("expected the schema once, found %d of 3 marker tables", tables)
	}
	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE service = $1`, serviceName).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied == 0 {
		t.Fatal("no migration was recorded")
	}
}

// requireScratchDSN is the same guard the http and service packages carry:
// the database must be a *_test scratch database and never the live one.
func requireScratchDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("MONETIZATION_POSTGRES_DSN does not parse: %v", err)
	}
	switch db := cfg.Database; {
	case db == "" || db == "app" || !strings.HasSuffix(db, "_test"):
		t.Fatalf("refusing to run integration tests against database %q: use a scratch database whose name ends in _test", db)
	}
	return dsn
}
