package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestUpsertDailyRevenue_Idempotent_OverallRow verifies a re-run for the
// same (date, NULL, NULL) overwrites in place rather than duplicating.
func TestUpsertDailyRevenue_Idempotent_OverallRow(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	day := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	in := DailyRevenueRow{Date: day, SubscriptionsCount: 10, SubscriptionsRevenuePaise: 200000}
	if err := st.UpsertDailyRevenue(ctx, in); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	in.SubscriptionsCount = 50
	in.SubscriptionsRevenuePaise = 400000
	if err := st.UpsertDailyRevenue(ctx, in); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	rows, err := st.SumRevenueByPlan(ctx, RevenueQueryFilter{
		Since: ptrTime(day), Until: ptrTime(day.AddDate(0, 0, 1)),
	})
	if err != nil {
		t.Fatalf("sum by plan: %v", err)
	}
	// The plan-only branch filters plan_id IS NOT NULL, so the all-up row
	// is NOT in the result. A separate query against city is fine too.
	_ = rows
	// Direct DB readback: we must have exactly ONE row for (day, NULL, NULL).
	var count int
	if err := st.DB().QueryRow(ctx, `SELECT COUNT(*) FROM rider_daily_revenue WHERE date = $1 AND city_id IS NULL AND plan_id IS NULL`, day).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("after two upserts, want 1 row, got %d (idempotency broken)", count)
	}
}

// TestUpsertDailyRevenue_PerPlan ensures the per-plan slice is keyed
// properly and doesn't collide with the overall row.
func TestUpsertDailyRevenue_PerPlan(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	day := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	planID := uuid.New()
	if err := st.UpsertDailyRevenue(ctx, DailyRevenueRow{Date: day, SubscriptionsCount: 1, SubscriptionsRevenuePaise: 100}); err != nil {
		t.Fatalf("overall: %v", err)
	}
	if err := st.UpsertDailyRevenue(ctx, DailyRevenueRow{Date: day, PlanID: &planID, SubscriptionsCount: 1, SubscriptionsRevenuePaise: 100}); err != nil {
		t.Fatalf("per-plan: %v", err)
	}
	var count int
	if err := st.DB().QueryRow(ctx, `SELECT COUNT(*) FROM rider_daily_revenue WHERE date = $1`, day).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("overall + 1 plan should produce 2 rows, got %d", count)
	}
}

// TestComputeDailyRevenueRaw_EmptyDay returns zero counts when no
// payments / rides land in the window.
func TestComputeDailyRevenueRaw_EmptyDay(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Use a far-future window so there is definitely no data.
	start := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	out, err := st.ComputeDailyRevenueRaw(ctx, start, end)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if out.SubscriptionsCount != 0 || out.RidesCount != 0 {
		t.Errorf("expected zero counts; got %+v", out)
	}
}

// TestSumRevenueByPlan_EmptyData returns zero rows when no rollups exist.
func TestSumRevenueByPlan_EmptyData(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	since := ptrTime(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	rows, err := st.SumRevenueByPlan(ctx, RevenueQueryFilter{Since: since})
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("future date should produce no rows, got %d", len(rows))
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
