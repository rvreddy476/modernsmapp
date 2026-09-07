package identityroles

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The queue tests need a real Postgres because the whole point of the queue is
// FOR UPDATE SKIP LOCKED and transactional enqueue, neither of which a fake
// can exercise honestly.
//
// Point TEST_PG_DSN at a SCRATCH database. Never at `app` or `commerce_db` —
// pointing integration fixtures at the live databases is exactly what put 4773
// junk sellers into commerce_db, which is the mess the backfill tool now has
// to work around.
//
//	docker exec atpost_stack-postgres-1 psql -U postgres -c "CREATE DATABASE identity_roles_it_test"
//	TEST_PG_DSN=postgres://postgres:postgres@127.0.0.1:5432/identity_roles_it_test?sslmode=disable go test ./identityroles/
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping queue integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), SchemaSQL("")); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE `+TableName("")+` RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

// mustStats fails the test rather than hiding a broken Stats query behind
// zeroed counters — which is exactly how the first draft of Stats shipped a
// SQL syntax error that every assertion silently agreed with.
func mustStats(t *testing.T, ob *Outbox, pool *pgxpool.Pool) Stats {
	t.Helper()
	st, err := ob.Stats(context.Background(), pool)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	return st
}

func TestSchemaSQLIsIdempotentAndSchemaAware(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	// Applying twice must be a no-op — food and rider re-run their whole
	// setup.sql on every boot.
	if _, err := pool.Exec(ctx, SchemaSQL("")); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS rider`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx, SchemaSQL("rider")); err != nil {
		t.Fatalf("schema-qualified apply: %v", err)
	}
	if TableName("rider") != "rider.identity_role_intents" {
		t.Fatalf("TableName(rider) = %s", TableName("rider"))
	}
	_, _ = pool.Exec(ctx, `DROP SCHEMA rider CASCADE`)
}

func TestEnqueueIsTransactional(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "commerce-service")

	// A rolled-back domain transaction must take the intent with it. This is
	// the whole durability argument: identity is never told about an approval
	// that did not happen.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.Enqueue(ctx, tx, Intent{Op: OpGrant, UserID: testUser, Role: RoleSeller, Reason: "approved"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := ob.Stats(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 0 {
		t.Fatalf("pending = %d after rollback, want 0", st.Pending)
	}

	// And a committed one must keep it.
	tx, _ = pool.Begin(ctx)
	if err := ob.Enqueue(ctx, tx, Intent{Op: OpGrant, UserID: testUser, Role: RoleSeller, Reason: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if st = mustStats(t, ob, pool); st.Pending != 1 {
		t.Fatalf("pending = %d after commit, want 1", st.Pending)
	}
}

func TestEnqueueRejectsBadIntents(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "commerce-service")

	if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: testUser, Role: "admin"}); !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("err = %v, want ErrNotGrantable", err)
	}
	if err := ob.EnqueuePool(ctx, pool, Intent{Op: "sideways", UserID: testUser, Role: RoleSeller}); err == nil {
		t.Fatal("want an error for a bad op")
	}
	if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: " ", Role: RoleSeller}); err == nil {
		t.Fatal("want an error for an empty user id")
	}
	if st := mustStats(t, ob, pool); st.Pending != 0 {
		t.Fatalf("pending = %d, want 0 — nothing invalid may reach the table", st.Pending)
	}
}

func TestWorkerDeliversAndIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "commerce-service")

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":{"status":"granted"}}`))
	}))
	defer srv.Close()

	for i := 0; i < 3; i++ {
		if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: testUser, Role: RoleSeller, Reason: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	w := NewWorker(newTestClient(srv), ob, pool, slog.Default(), WorkerConfig{})
	n, err := w.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 3 || calls.Load() != 3 {
		t.Fatalf("handled = %d, calls = %d, want 3 and 3", n, calls.Load())
	}
	// A second sweep must find nothing — delivered rows are not re-sent.
	if n, err = w.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("second sweep: n = %d, err = %v", n, err)
	}
	st := mustStats(t, ob, pool)
	if st.Pending != 0 || st.Delivered != 3 || st.DeadLetter != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestWorkerRetriesWhenIdentityIsDown(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "food-service")

	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"status":"granted"}}`))
	}))
	defer srv.Close()

	if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: testUser, Role: RoleRestaurantOwner}); err != nil {
		t.Fatal(err)
	}
	// BaseBackoff of 1ms so the retry is due immediately on the next sweep;
	// the backoff arithmetic itself is covered by TestBackoffSchedule.
	w := NewWorker(newTestClient(srv), ob, pool, slog.Default(),
		WorkerConfig{BaseBackoff: time.Millisecond, MaxAttempts: 5})

	if _, err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	st := mustStats(t, ob, pool)
	if st.Pending != 1 || st.DeadLetter != 0 {
		t.Fatalf("after failed sweep stats = %+v, want it still pending and NOT lost", st)
	}

	up.Store(true)
	time.Sleep(10 * time.Millisecond)
	if _, err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if st = mustStats(t, ob, pool); st.Delivered != 1 || st.Pending != 0 {
		t.Fatalf("after recovery stats = %+v, want delivered", st)
	}
}

func TestWorkerDeadLettersAfterMaxAttempts(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "rider-service")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: testUser, Role: RoleRiderPartner}); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(newTestClient(srv), ob, pool, slog.Default(),
		WorkerConfig{BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxAttempts: 3})

	for i := 0; i < 3; i++ {
		if _, err := w.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	st := mustStats(t, ob, pool)
	if st.DeadLetter != 1 || st.Pending != 0 {
		t.Fatalf("stats = %+v, want 1 dead-lettered and 0 pending", st)
	}
	// Dead-lettered rows must never be picked up again on their own.
	if n, _ := w.Sweep(ctx); n != 0 {
		t.Fatalf("dead-lettered intent was re-swept (%d)", n)
	}
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM `+TableName("")+` WHERE id=1`).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr == nil || *lastErr == "" {
		t.Fatal("a dead-lettered intent must record why — otherwise the role is silently lost")
	}
}

func TestWorkerDeadLettersPermanentErrorsImmediately(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "commerce-service")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN","message":"bad internal key"}}`))
	}))
	defer srv.Close()

	if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: testUser, Role: RoleSeller}); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(newTestClient(srv), ob, pool, slog.Default(), WorkerConfig{MaxAttempts: 10})
	if _, err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	st := mustStats(t, ob, pool)
	if st.DeadLetter != 1 {
		t.Fatalf("stats = %+v — a rejected internal key must fail loudly on the first attempt, not after ten", st)
	}
}

func TestClaimSkipsLockedRows(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("", "commerce-service")

	for i := 0; i < 2; i++ {
		if err := ob.EnqueuePool(ctx, pool, Intent{Op: OpGrant, UserID: testUser, Role: RoleSeller}); err != nil {
			t.Fatal(err)
		}
	}
	txA, gotA, err := ob.Claim(ctx, pool, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txA.Rollback(ctx) }()

	txB, gotB, err := ob.Claim(ctx, pool, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txB.Rollback(ctx) }()

	if len(gotA) != 1 || len(gotB) != 1 {
		t.Fatalf("A claimed %d, B claimed %d — two workers must not claim the same row", len(gotA), len(gotB))
	}
	if gotA[0].ID == gotB[0].ID {
		t.Fatalf("both workers claimed id %d", gotA[0].ID)
	}
}

func TestBackoffSchedule(t *testing.T) {
	w := NewWorker(NewClient("http://x", "k", "s"), NewOutbox("", "s"), &pgxpool.Pool{}, nil,
		WorkerConfig{BaseBackoff: time.Second, MaxBackoff: 8 * time.Second})
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, exp := range want {
		if got := w.backoff(i); got != exp {
			t.Errorf("backoff(%d) = %v, want %v", i, got, exp)
		}
	}
}

func TestNilWorkerIsSafe(t *testing.T) {
	// A service with identity unconfigured builds a nil Worker and must still
	// be able to Start it.
	var w *Worker = NewWorker(nil, nil, nil, nil, WorkerConfig{})
	if w != nil {
		t.Fatal("want a nil Worker when dependencies are missing")
	}
	w.Run(context.Background())
	if n, err := w.Sweep(context.Background()); n != 0 || err != nil {
		t.Fatalf("nil sweep = %d, %v", n, err)
	}
}
