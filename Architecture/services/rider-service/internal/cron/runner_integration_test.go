package cron

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationStoreAdapter returns a real *StoreAdapter against TEST_PG_DSN.
// Skips the test when the env var is unset.
func integrationStoreAdapter(t *testing.T) (*StoreAdapter, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping cron-runner integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	return NewStoreAdapter(st), st, func() { pool.Close() }
}

// TestStoreAdapter_RoundTrip verifies a Start + Finish writes a real row
// the runner can find via HasRunningCronRun in between.
func TestStoreAdapter_RoundTrip(t *testing.T) {
	adapter, _, cleanup := integrationStoreAdapter(t)
	defer cleanup()
	ctx := context.Background()

	id, err := adapter.StartCronRun(ctx, "integ-runner-test")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	busy, err := adapter.HasRunningCronRun(ctx, "integ-runner-test", time.Hour)
	if err != nil || !busy {
		t.Fatalf("expected busy=true, got %v err=%v", busy, err)
	}
	if err := adapter.FinishCronRun(ctx, id, 5, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

// TestRunner_Integration_HappyPath wires the real Postgres-backed
// adapter into the runner and asserts a single tick fires.
func TestRunner_Integration_HappyPath(t *testing.T) {
	adapter, _, cleanup := integrationStoreAdapter(t)
	defer cleanup()

	r := NewRunner(adapter, nil)
	var calls atomic.Int32
	r.RegisterJob("integ-happy", JobOptions{
		Interval: 30 * time.Millisecond, RunImmediately: true,
	}, func(_ context.Context) (int, error) {
		calls.Add(1)
		return 3, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	if calls.Load() == 0 {
		t.Errorf("integ job never ran")
	}
}

// TestRunner_Integration_FailureLogged verifies the failure-summary
// roundtrips through the real DB.
func TestRunner_Integration_FailureLogged(t *testing.T) {
	adapter, st, cleanup := integrationStoreAdapter(t)
	defer cleanup()

	r := NewRunner(adapter, nil)
	r.RegisterJob("integ-fail", JobOptions{
		Interval: 30 * time.Millisecond, RunImmediately: true,
	}, func(_ context.Context) (int, error) {
		return 0, errors.New("simulated rdb timeout")
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)

	rows, err := st.ListCronRuns(context.Background(), store.CronRunFilter{Job: "integ-fail", Limit: 5})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no cron-runs row written")
	}
	if rows[0].Status != "failed" {
		t.Errorf("status = %q, want failed", rows[0].Status)
	}
}
