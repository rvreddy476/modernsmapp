package postgres

// BootstrapSchema must survive several boots at once.
//
// setup.sql re-applies statements on every boot (trigger functions replaced,
// CHECK constraints dropped and re-added). Unserialised, concurrent sessions
// collide with `tuple concurrently updated` (XX000) — observed 13 Sep 2026
// when three integration packages bootstrapped food_it_test together, and
// the same failure waits for two replicas booting at once. BootstrapSchema
// now runs the script under food-service's advisory lock; this test fires
// several boots simultaneously, over a few rounds, and requires every one to
// succeed. Remove the lock and it fails.
//
// Run with TEST_PG_DSN pointing at a database whose name ends in `_test`.

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/food-service/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBootstrapSchemaSurvivesConcurrentBooters(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	if err := requireTestDatabase(dsn); err != nil {
		t.Fatalf("refusing to run against this database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	const booters = 6
	// Enough connections that every booter gets its own session at once; a
	// smaller pool would serialise them on Acquire and hide the race.
	cfg.MaxConns = booters + 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	const rounds = 3
	for round := 1; round <= rounds; round++ {
		start := make(chan struct{})
		errs := make(chan error, booters)
		var wg sync.WaitGroup
		for i := 0; i < booters; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- BootstrapSchema(ctx, pool, database.SetupSQL)
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: a concurrent boot failed: %v", round, err)
			}
		}
	}
}
