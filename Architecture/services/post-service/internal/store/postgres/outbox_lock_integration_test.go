//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// feed-07 against a real Postgres: the outbox relay's advisory lock and the
// order the relay reads its rows in (migration 085).
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run OutboxRelay -v
//
// The lock is session-scoped, so "another replica" here is a second pool:
// its connections are other Postgres sessions, as another pod's would be.

// secondPool is a pool of its own on the same scratch database.
func secondPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), requireTestDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// mustLock takes the relay lock or fails the test.
func mustLock(t *testing.T, st *postgres.Store, ctx context.Context, who string) func() {
	t.Helper()
	release, ok, err := st.TryOutboxRelayLock(ctx)
	if err != nil {
		t.Fatalf("%s: lock: %v", who, err)
	}
	if !ok {
		t.Fatalf("%s: lock refused, want it taken", who)
	}
	return release
}

// mustBeRefused asserts the relay lock is held by another session.
func mustBeRefused(t *testing.T, st *postgres.Store, who string) {
	t.Helper()
	release, ok, err := st.TryOutboxRelayLock(context.Background())
	if err != nil {
		t.Fatalf("%s: lock: %v", who, err)
	}
	if ok {
		release()
		t.Fatalf("%s: took the relay lock while another session held it", who)
	}
}

func TestOutboxRelayLockIT_SecondStoreIsRefusedUntilRelease(t *testing.T) {
	poolA, poolB := openSubscriptionDB(t), secondPool(t)
	a, b := postgres.New(poolA), postgres.New(poolB)
	ctx := context.Background()

	release := mustLock(t, a, ctx, "first store")
	mustBeRefused(t, b, "second store")
	// The holder's own pool is other sessions too: a second pass in the same
	// pod does not get in either.
	mustBeRefused(t, a, "first store, second pass")
	// A refused try returns its connection.
	mustBeIdle(t, poolB, "second pool after a refused try")

	release()
	mustBeIdle(t, poolA, "first pool after release")
	releaseB := mustLock(t, b, ctx, "second store after release")
	releaseB()
	// And back again: the lock is not left on a pooled connection.
	mustLock(t, a, ctx, "first store again")()
}

// A pass whose context is cancelled (shutdown) still frees the lock: the
// unlock runs on a context of its own.
func TestOutboxRelayLockIT_ReleaseAfterTheContextIsCancelled(t *testing.T) {
	poolA, poolB := openSubscriptionDB(t), secondPool(t)
	a, b := postgres.New(poolA), postgres.New(poolB)

	ctx, cancel := context.WithCancel(context.Background())
	release := mustLock(t, a, ctx, "first store")
	cancel()
	mustBeRefused(t, b, "second store, before release")
	release()

	mustBeIdle(t, poolA, "first pool after release")
	mustLock(t, b, context.Background(), "second store after a cancelled pass released")()
}

// Migration 085: events one transaction writes for one post share
// created_at; the relay reads them in the order they were written.
func TestOutboxRelayIT_SameTransactionRowsAreReadInWriteOrder(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	post := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, post)
	})

	const n = 24 // random ids would land in this order once in 24! runs
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for i := 0; i < n; i++ {
		if err := postgres.InsertOutboxEventTx(ctx, tx, fmt.Sprintf("it.order.%02d", i), "post", post, map[string]int{"n": i}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	// A row from before 085 has no seq: it sorts ahead of numbered rows with
	// the same created_at.
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at, seq)
		VALUES ('it.order.legacy', 'post', $1, '{}', NOW(), NULL)`, post); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var distinct int
	if err := pool.QueryRow(ctx, `SELECT COUNT(DISTINCT created_at) FROM post_outbox_events WHERE aggregate_id = $1`, post).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct != 1 {
		t.Fatalf("precondition: rows of one transaction carry %d created_at values, want 1", distinct)
	}

	rows, err := st.GetUnpublishedOutboxEvents(ctx, 100000)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		if r.AggregateID == post {
			got = append(got, r.EventType)
		}
	}
	want := []string{"it.order.legacy"}
	for i := 0; i < n; i++ {
		want = append(want, fmt.Sprintf("it.order.%02d", i))
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("read order:\n got %v\nwant %v", got, want)
	}
}

// mustBeIdle asserts the pool got every connection back. The pool may hand
// a released connection back from a goroutine of its own, so this waits.
func mustBeIdle(t *testing.T, pool *pgxpool.Pool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pool.Stat().AcquiredConns() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d connection(s) still out", what, pool.Stat().AcquiredConns())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
