// Daily revenue rollup. Runs once per day at 01:00 IST. Computes
// yesterday's totals and writes one row per (date, city_id, plan_id)
// dimension into rider_daily_revenue:
//
//	(date, NULL, NULL)         — overall day rollup
//	(date, city_id, NULL)      — per-city slice
//	(date, NULL, plan_id)      — per-plan slice
//
// The unique index on (date, COALESCE(city_id), COALESCE(plan_id)) makes
// every UPSERT idempotent: a re-run a minute later overwrites the same
// rows in place.
//
// The job emits a single EventRiderDailyRevenueReport with the all-up
// totals at the end so notification-service can email the ops team.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15 + §17 (admin reports).
package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
)

// RevenuePublisher is the publisher contract for the daily revenue event.
type RevenuePublisher interface {
	PublishDailyRevenueReport(ctx context.Context, payload events.DailyRevenueReportPayload) error
}

// dayBoundsIST returns [startUTC, endUTC) for the IST day containing the
// reference time. Mopedu launches in India only; this hard-coded IST
// rollup point is fine for v1.
func dayBoundsIST(ref time.Time) (time.Time, time.Time) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		// Fallback: fixed +05:30 offset.
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	t := ref.In(loc)
	startIST := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	endIST := startIST.AddDate(0, 0, 1)
	return startIST.UTC(), endIST.UTC()
}

// RunDailyRevenueReport rolls up yesterday's verified subscription
// payments + ride completions into rider_daily_revenue. Returns the
// count of rollup rows written (overall + per-city + per-plan).
func RunDailyRevenueReport(ctx context.Context, st *store.Store, pub RevenuePublisher) (int, error) {
	return runDailyRevenueReportFor(ctx, st, pub, time.Now().UTC().AddDate(0, 0, -1))
}

// runDailyRevenueReportFor runs the rollup for the IST day containing
// `ref`. Exposed so tests can pin the date.
func runDailyRevenueReportFor(ctx context.Context, st *store.Store, pub RevenuePublisher, ref time.Time) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("daily revenue: store required")
	}
	dayStartUTC, dayEndUTC := dayBoundsIST(ref)
	dayDate := time.Date(dayStartUTC.Year(), dayStartUTC.Month(), dayStartUTC.Day(), 0, 0, 0, 0, time.UTC)

	overall, err := st.ComputeDailyRevenueRaw(ctx, dayStartUTC, dayEndUTC)
	if err != nil {
		return 0, err
	}
	rowsWritten := 0
	if err := st.UpsertDailyRevenue(ctx, store.DailyRevenueRow{
		Date:                       dayDate,
		SubscriptionsCount:         overall.SubscriptionsCount,
		SubscriptionsRevenuePaise:  overall.SubscriptionsRevenuePaise,
		RidesCount:                 overall.RidesCount,
		RidesCompleted:             overall.RidesCompleted,
		RidesCancelled:             overall.RidesCancelled,
		FareTotalPaise:             overall.FareTotalPaise,
		CancellationFeesPaise:      overall.CancellationFeesPaise,
	}); err != nil {
		return rowsWritten, fmt.Errorf("upsert overall: %w", err)
	}
	rowsWritten++

	byPlan, err := st.ComputeRevenueByPlanForDay(ctx, dayStartUTC, dayEndUTC)
	if err != nil {
		return rowsWritten, err
	}
	for planID, v := range byPlan {
		pid := planID
		if err := st.UpsertDailyRevenue(ctx, store.DailyRevenueRow{
			Date:                       dayDate,
			PlanID:                     &pid,
			SubscriptionsCount:         v.SubscriptionsCount,
			SubscriptionsRevenuePaise:  v.SubscriptionsRevenuePaise,
		}); err != nil {
			return rowsWritten, fmt.Errorf("upsert plan %s: %w", pid, err)
		}
		rowsWritten++
	}

	byCity, err := st.ComputeRevenueByCityForDay(ctx, dayStartUTC, dayEndUTC)
	if err != nil {
		return rowsWritten, err
	}
	for cityID, v := range byCity {
		cid := cityID
		if err := st.UpsertDailyRevenue(ctx, store.DailyRevenueRow{
			Date:                       dayDate,
			CityID:                     &cid,
			RidesCount:                 v.RidesCount,
			RidesCompleted:             v.RidesCompleted,
			RidesCancelled:             v.RidesCancelled,
			FareTotalPaise:             v.FareTotalPaise,
			CancellationFeesPaise:      v.CancellationFeesPaise,
		}); err != nil {
			return rowsWritten, fmt.Errorf("upsert city %s: %w", cid, err)
		}
		rowsWritten++
	}

	if pub != nil {
		payload := events.DailyRevenueReportPayload{
			Date:                       dayDate.Format("2006-01-02"),
			SubscriptionsCount:         overall.SubscriptionsCount,
			SubscriptionsRevenuePaise:  overall.SubscriptionsRevenuePaise,
			RidesCount:                 overall.RidesCount,
			RidesCompleted:             overall.RidesCompleted,
			RidesCancelled:             overall.RidesCancelled,
			FareTotalPaise:             overall.FareTotalPaise,
			CancellationFeesPaise:      overall.CancellationFeesPaise,
			OccurredAt:                 time.Now().UTC(),
		}
		// We swallow the publish error here — the rollup row is the
		// canonical record; downstream notification is best-effort.
		_ = pub.PublishDailyRevenueReport(ctx, payload)
	}
	return rowsWritten, nil
}
