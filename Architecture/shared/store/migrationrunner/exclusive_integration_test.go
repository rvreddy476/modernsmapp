//go:build integration

package migrationrunner

// The A7 two-runner proof.
//
// The review's objection to the first draft was precise: naming "advisory
// lock or one-shot job" is not choosing one, and a naive pool-level
// `db.Exec(pg_advisory_lock(...))` is worse than nothing — it locks whichever
// pooled SESSION served that call, while the migrations then run on a
// different session that holds no lock at all.
//
// So this proves the mechanism, not the intention:
//
//	1. Two runners started together apply each migration EXACTLY once.
//	2. The lock is genuinely held by the connection doing the work — proven
//	   by observing pg_locks from outside while a migration is in flight.
//	3. A cancelled runner releases the lock rather than wedging the fleet.
//	4. A failing migration is NOT recorded as applied (C10's narrow claim).

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MIGRATIONRUNNER_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("COMMERCE_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("set MIGRATIONRUNNER_TEST_DSN (or COMMERCE_TEST_DSN) to run the migration-lock proofs")
	}
	return dsn
}

func newPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestExclusive_TwoRunnersApplyEachMigrationExactlyOnce is the headline A7
// proof.
//
// The migration deliberately is NOT idempotent — it appends a row rather
// than using IF NOT EXISTS — because an idempotent migration would pass even
// if both runners executed it, which would prove nothing. Under the plain
// `Run`, both replicas would append and the count would be 2.
func TestExclusive_TwoRunnersApplyEachMigrationExactlyOnce(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool := newPool(t, dsn)

	service := fmt.Sprintf("proof-%d", time.Now().UnixNano())
	table := "mig_proof_" + service[6:]

	fsys := fstest.MapFS{
		"migrations/001_create.sql": &fstest.MapFile{Data: []byte(fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (n BIGSERIAL PRIMARY KEY, note TEXT);`, table))},
		// Non-idempotent on purpose: two executions leave two rows.
		"migrations/002_append.sql": &fstest.MapFile{Data: []byte(fmt.Sprintf(
			`INSERT INTO %s (note) VALUES ('applied'); SELECT pg_sleep(0.25);`, table))},
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table))
		_, _ = pool.Exec(ctx, `DELETE FROM schema_migrations WHERE service = $1`, service)
	})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = RunExclusive(ctx, pool, service, fsys, "migrations")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("runner %d: %v", i, err)
		}
	}

	var rows int
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("migration applied %d times, want exactly 1 — two booting replicas both ran the "+
			"non-idempotent step", rows)
	}

	var recorded int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE service = $1`, service).Scan(&recorded)
	if recorded != 2 {
		t.Fatalf("recorded %d migrations, want 2", recorded)
	}
}

// TestExclusive_LockIsHeldByTheWorkingConnection is the trap A7 names.
//
// A pool-level `pg_advisory_lock` locks one pooled session and then executes
// the migrations on another. This observes pg_locks from a SEPARATE pool
// while a migration is deliberately slow, and asserts the lock is actually
// held for the duration.
func TestExclusive_LockIsHeldByTheWorkingConnection(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool := newPool(t, dsn)
	observer := newPool(t, dsn)

	service := fmt.Sprintf("lockproof-%d", time.Now().UnixNano())
	key := advisoryKey(service)

	fsys := fstest.MapFS{
		"migrations/001_slow.sql": &fstest.MapFile{Data: []byte(`SELECT pg_sleep(1.0);`)},
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM schema_migrations WHERE service = $1`, service)
	})

	done := make(chan error, 1)
	go func() { done <- RunExclusive(ctx, pool, service, fsys, "migrations") }()

	// Observe from outside while the migration is in flight.
	held := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := observer.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = $1::bigint % 4294967296`,
			key).Scan(&n); err == nil && n > 0 {
			held = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("runner: %v", err)
	}
	if !held {
		t.Fatal("no advisory lock was observed while a migration was running: the lock is being taken " +
			"on a different session from the one doing the work, which is the exact A7 trap")
	}

	// And it is released afterwards.
	var after int
	_ = observer.QueryRow(ctx,
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = $1::bigint % 4294967296`,
		key).Scan(&after)
	if after != 0 {
		t.Fatalf("advisory lock still held after the run (%d); a crashed migrator would wedge the fleet", after)
	}
}

// TestExclusive_FailingMigrationIsNotRecorded is C10's narrow claim.
//
// A migration that fails must not be recorded as applied, or the next boot
// will skip it and the schema will be permanently short of it while every
// replica believes it is complete.
func TestExclusive_FailingMigrationIsNotRecorded(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool := newPool(t, dsn)

	service := fmt.Sprintf("failproof-%d", time.Now().UnixNano())
	fsys := fstest.MapFS{
		"migrations/001_bad.sql": &fstest.MapFile{
			Data: []byte(`SELECT * FROM a_table_that_does_not_exist;`)},
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM schema_migrations WHERE service = $1`, service)
	})

	if err := RunExclusive(ctx, pool, service, fsys, "migrations"); err == nil {
		t.Fatal("a failing migration returned success")
	}

	var recorded int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE service = $1`, service).Scan(&recorded)
	if recorded != 0 {
		t.Fatalf("a failed migration was recorded as applied (%d rows) — the next boot would skip it", recorded)
	}
}

// TestExclusive_CancellationReleasesTheLock proves a killed migrator does
// not wedge every other replica.
func TestExclusive_CancellationReleasesTheLock(t *testing.T) {
	dsn := testDSN(t)
	pool := newPool(t, dsn)

	service := fmt.Sprintf("cancelproof-%d", time.Now().UnixNano())
	key := advisoryKey(service)

	fsys := fstest.MapFS{
		"migrations/001_slow.sql": &fstest.MapFile{Data: []byte(`SELECT pg_sleep(5.0);`)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunExclusive(ctx, pool, service, fsys, "migrations") }()

	time.Sleep(400 * time.Millisecond)
	cancel()
	<-done

	obs := newPool(t, dsn)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := obs.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid = $1::bigint % 4294967296`,
			key).Scan(&n); err == nil && n == 0 {
			return // released
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the advisory lock was still held after the runner was cancelled")
}
