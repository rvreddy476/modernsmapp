// North-star integration tests. These need TEST_PG_DSN to seed the
// dating_safety_events table; they skip when the env var is not set.
package telemetry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping north-star tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return store.New(pool)
}

func TestNorthStar_Compute_NoEvents(t *testing.T) {
	st := setupStore(t)
	c := NewNorthStarComputer(st, nil, nil)
	snap, err := c.Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if snap.WindowDays != 30 {
		t.Fatalf("expected 30-day window, got %d", snap.WindowDays)
	}
	// With no events the rate is 0.
	if snap.ScheduledMeetsCount == 0 && snap.OffAppMeetRate != 0 {
		t.Fatalf("expected zero rate with zero scheduled meets, got %f", snap.OffAppMeetRate)
	}
}

func TestNorthStar_Compute_WithEvents(t *testing.T) {
	st := setupStore(t)
	ctx := context.Background()

	// Seed three meet_scheduled and two safe meet_check_in rows.
	user := uuid.New()
	for i := 0; i < 3; i++ {
		if err := st.RecordSafetyEvent(ctx, user, "meet_scheduled", map[string]any{"i": i}); err != nil {
			t.Fatalf("seed scheduled: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := st.RecordSafetyEvent(ctx, user, "meet_check_in", map[string]any{"status": "safe"}); err != nil {
			t.Fatalf("seed checkin: %v", err)
		}
	}
	c := NewNorthStarComputer(st, nil, nil)
	snap, err := c.Compute(ctx)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if snap.ScheduledMeetsCount < 3 {
		t.Fatalf("expected >=3 scheduled, got %d", snap.ScheduledMeetsCount)
	}
	if snap.SafeCheckInsCount < 2 {
		t.Fatalf("expected >=2 safe check-ins, got %d", snap.SafeCheckInsCount)
	}
	if snap.OffAppMeetRate <= 0 {
		t.Fatalf("expected positive rate, got %f", snap.OffAppMeetRate)
	}
}

func TestNorthStar_NilStoreFails(t *testing.T) {
	c := NewNorthStarComputer(nil, nil, nil)
	if _, err := c.Compute(context.Background()); err == nil {
		t.Fatalf("expected error with nil store")
	}
}

func TestNorthStar_Snapshot_Timestamps(t *testing.T) {
	st := setupStore(t)
	c := NewNorthStarComputer(st, nil, nil)
	snap, err := c.Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if snap.GeneratedAt.IsZero() {
		t.Fatalf("generated_at must be set")
	}
	if time.Since(snap.GeneratedAt) > time.Minute {
		t.Fatalf("generated_at should be recent: %v", snap.GeneratedAt)
	}
}

func TestMetrics_Default_Singleton(t *testing.T) {
	m := Default()
	m2 := Default()
	if m != m2 {
		t.Fatalf("Default must return singleton")
	}
	if m.DAU == nil || m.PulseTodayLatency == nil {
		t.Fatalf("metrics not initialised")
	}
}
