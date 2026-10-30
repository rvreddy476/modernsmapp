//go:build integration

package outbox

// Postgres proofs for the M4 relay (commerce backend audit, 30 Oct 2026):
// the claim is exclusive and leased, the relay lock admits one replica, two
// publishers running together write every row exactly once, failures back
// off and dead-letter, and a table without the claim columns still relays.
//
// Point TEST_PG_DSN at a SCRATCH database whose name ends in _test. Each test
// creates and drops its own schema.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafka "github.com/segmentio/kafka-go"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set TEST_PG_DSN to a scratch _test database to run the outbox relay proofs")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// scratchOutbox creates <schema>.outbox_events in the shape every service
// shipped before M4, optionally with the claim columns, and n rows.
func scratchOutbox(t *testing.T, pool *pgxpool.Pool, withClaims bool, n int) string {
	t.Helper()
	ctx := context.Background()
	schema := fmt.Sprintf("outbox_it_%d", time.Now().UnixNano())
	table := schema + ".outbox_events"
	stmts := []string{
		`CREATE SCHEMA ` + schema,
		`CREATE TABLE ` + table + ` (
			id BIGSERIAL PRIMARY KEY,
			event_type TEXT NOT NULL,
			partition_key TEXT NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at TIMESTAMPTZ)`,
	}
	if withClaims {
		stmts = append(stmts, MigrationSQL(table), MigrationSQL(table)) // re-runnable
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	})
	q := NewQueuer(schema)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("order-%d", i%4)
		if err := q.EnqueuePool(ctx, pool, "commerce.order.paid", key, []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	return schema
}

func testStore(pool *pgxpool.Pool, schema string) *pgStore {
	table := schema + ".outbox_events"
	return newPGStore(pool, schema, table, relayLockKey(table))
}

func TestPGStore_ClaimIsExclusiveAndLeased(t *testing.T) {
	pool := testPool(t)
	schema := scratchOutbox(t, pool, true, 10)
	ctx := context.Background()
	a, b := testStore(pool, schema), testStore(pool, schema)

	var wg sync.WaitGroup
	var ra, rb []outboxRow
	var ea, eb error
	wg.Add(2)
	go func() { defer wg.Done(); ra, ea = a.claim(ctx, 5, time.Second) }()
	go func() { defer wg.Done(); rb, eb = b.claim(ctx, 5, time.Second) }()
	wg.Wait()
	if ea != nil || eb != nil {
		t.Fatal(ea, eb)
	}
	seen := map[int64]bool{}
	for _, r := range append(ra, rb...) {
		if seen[r.ID] {
			t.Fatalf("row %d claimed by both relays", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != 10 {
		t.Fatalf("claimed %d rows, want 10", len(seen))
	}
	if again, _ := a.claim(ctx, 10, time.Second); len(again) != 0 {
		t.Fatalf("claimed %d rows under a live lease", len(again))
	}

	if err := a.markPublished(ctx, []int64{ra[0].ID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond) // the lease runs out
	back, err := b.claim(ctx, 100, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 9 {
		t.Fatalf("reclaimed %d rows after the lease, want 9 (one published)", len(back))
	}
}

func TestPGStore_FailuresBackOffAndDeadLetter(t *testing.T) {
	pool := testPool(t)
	schema := scratchOutbox(t, pool, true, 3)
	ctx := context.Background()
	s := testStore(pool, schema)
	rows, err := s.claim(ctx, 3, time.Minute)
	if err != nil || len(rows) != 3 {
		t.Fatalf("claim: %v %d", err, len(rows))
	}
	id1, id2, id3 := rows[0].ID, rows[1].ID, rows[2].ID

	dead, err := s.recordFailure(ctx, id1, failure{Reason: "invalid record", Counted: true}, 2)
	if err != nil || dead {
		t.Fatalf("first counted failure: dead=%v err=%v", dead, err)
	}
	var attempts int
	var backedOff bool
	var lastErr string
	if err := pool.QueryRow(ctx, `SELECT attempts, claimed_until > NOW(), last_error FROM `+s.table+` WHERE id=$1`, id1).
		Scan(&attempts, &backedOff, &lastErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !backedOff || lastErr != "invalid record" {
		t.Fatalf("after one failure attempts=%d backedOff=%v last_error=%q", attempts, backedOff, lastErr)
	}
	if dead, err = s.recordFailure(ctx, id1, failure{Reason: "invalid record", Counted: true}, 2); err != nil || !dead {
		t.Fatalf("second counted failure at max 2: dead=%v err=%v", dead, err)
	}
	if dead, err = s.recordFailure(ctx, id2, failure{Reason: "too large", Permanent: true}, 10); err != nil || !dead {
		t.Fatalf("permanent failure: dead=%v err=%v", dead, err)
	}
	if err := s.release(ctx, []int64{id3}, "broker down"); err != nil {
		t.Fatal(err)
	}
	due, err := s.claim(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != id3 {
		t.Fatalf("due after failures = %+v, want only the released row (dead rows skipped)", due)
	}
	var lastErr3 string
	var attempts3 int
	_ = pool.QueryRow(ctx, `SELECT attempts, last_error FROM `+s.table+` WHERE id=$1`, id3).Scan(&attempts3, &lastErr3)
	if attempts3 != 0 || lastErr3 != "broker down" {
		t.Fatalf("a transient release counted an attempt (%d) or lost the reason (%q)", attempts3, lastErr3)
	}
}

func TestPGStore_RelayLockAdmitsOneReplica(t *testing.T) {
	pool := testPool(t)
	schema := scratchOutbox(t, pool, true, 0)
	ctx := context.Background()
	a, b := testStore(pool, schema), testStore(pool, schema)
	release, ok, err := a.tryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	if _, ok2, err := b.tryLock(ctx); err != nil || ok2 {
		t.Fatalf("second replica got the lock while held: ok=%v err=%v", ok2, err)
	}
	release()
	release2, ok, err := b.tryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("lock not free after release: ok=%v err=%v", ok, err)
	}
	release2()
}

// syncWriter is a goroutine-safe fake Kafka writer.
type syncWriter struct {
	mu     sync.Mutex
	counts map[string]int
	order  map[string][]string
}

func (w *syncWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range msgs {
		id := header(m, HeaderOutboxID)
		w.counts[id]++
		w.order[string(m.Key)] = append(w.order[string(m.Key)], id)
	}
	return nil
}

func TestPublisher_TwoReplicasPublishEachRowOnceInKeyOrder(t *testing.T) {
	pool := testPool(t)
	const n = 250
	schema := scratchOutbox(t, pool, true, n)
	w := &syncWriter{counts: map[string]int{}, order: map[string][]string{}}
	ctx := context.Background()
	p1 := New(pool, Config{DBSchema: schema, BatchSize: 20})
	p2 := New(pool, Config{DBSchema: schema, BatchSize: 20})

	deadline := time.Now().Add(30 * time.Second)
	for {
		var wg sync.WaitGroup
		for _, p := range []*Publisher{p1, p2} {
			wg.Add(1)
			go func(p *Publisher) { defer wg.Done(); p.pass(ctx, w) }(p)
		}
		wg.Wait()
		var left int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+schema+`.outbox_events WHERE published_at IS NULL`).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d rows still unpublished", left)
		}
	}
	if len(w.counts) != n {
		t.Fatalf("published %d distinct rows, want %d", len(w.counts), n)
	}
	for id, c := range w.counts {
		if c != 1 {
			t.Fatalf("row %s published %d times", id, c)
		}
	}
	for key, ids := range w.order {
		for i := 1; i < len(ids); i++ {
			prev, err1 := strconv.Atoi(ids[i-1])
			cur, err2 := strconv.Atoi(ids[i])
			if err1 != nil || err2 != nil || cur <= prev {
				t.Fatalf("key %s published out of order: %v", key, ids)
			}
		}
	}
}

func TestPGStore_LegacyTableStillRelays(t *testing.T) {
	pool := testPool(t)
	schema := scratchOutbox(t, pool, false, 3)
	ctx := context.Background()
	s := testStore(pool, schema)
	leased, err := s.leasedMode(ctx)
	if err != nil || leased {
		t.Fatalf("leasedMode on a pre-M4 table = %v, %v; want false", leased, err)
	}
	w := &syncWriter{counts: map[string]int{}, order: map[string][]string{}}
	p := New(pool, Config{DBSchema: schema})
	p.pass(ctx, w)
	if len(w.counts) != 3 {
		t.Fatalf("legacy relay published %d rows, want 3", len(w.counts))
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM `+s.table+` WHERE published_at IS NULL`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d rows left unpublished", left)
	}
}
