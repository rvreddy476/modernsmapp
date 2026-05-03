// Daily revenue rollup store. The nightly RunDailyRevenueReport job
// computes one row per (date, city_id, plan_id) plus a (date, NULL, NULL)
// "all" rollup row for the day. The unique-key on (date, city_id, plan_id)
// — null-coalesced via the index expression — makes the upsert idempotent:
// re-running yesterday's rollup overwrites the same rows rather than
// duplicating them.
//
// Reports endpoints (RevenueByPlan / RevenueByCity) read this table
// rather than scanning rider_subscription_payments + rider_rides on every
// admin click.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15 (daily revenue) + §17 (admin reports).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DailyRevenueRow is one row in rider_daily_revenue.
type DailyRevenueRow struct {
	Date                       time.Time  `json:"date"`
	CityID                     *uuid.UUID `json:"city_id,omitempty"`
	PlanID                     *uuid.UUID `json:"plan_id,omitempty"`
	SubscriptionsCount         int        `json:"subscriptions_count"`
	SubscriptionsRevenuePaise  int64      `json:"subscriptions_revenue_paise"`
	RidesCount                 int        `json:"rides_count"`
	RidesCompleted             int        `json:"rides_completed"`
	RidesCancelled             int        `json:"rides_cancelled"`
	FareTotalPaise             int64      `json:"fare_total_paise"`
	CancellationFeesPaise      int64      `json:"cancellation_fees_paise"`
	ComputedAt                 time.Time  `json:"computed_at"`
}

// UpsertDailyRevenue writes (or replaces) one row in rider_daily_revenue.
// Uses ON CONFLICT against the (date, COALESCE(city_id), COALESCE(plan_id))
// unique index so a re-run on the same day cleanly overwrites the previous
// snapshot — this is the cron-idempotency contract.
func (s *Store) UpsertDailyRevenue(ctx context.Context, in DailyRevenueRow) error {
	const q = `
        INSERT INTO rider_daily_revenue (
            date, city_id, plan_id,
            subscriptions_count, subscriptions_revenue_paise,
            rides_count, rides_completed, rides_cancelled,
            fare_total_paise, cancellation_fees_paise, computed_at
        ) VALUES (
            $1, $2, $3,
            $4, $5,
            $6, $7, $8,
            $9, $10, NOW()
        )
        ON CONFLICT (date, COALESCE(city_id, '00000000-0000-0000-0000-000000000000'::uuid),
                     COALESCE(plan_id, '00000000-0000-0000-0000-000000000000'::uuid))
        DO UPDATE SET
            subscriptions_count         = EXCLUDED.subscriptions_count,
            subscriptions_revenue_paise = EXCLUDED.subscriptions_revenue_paise,
            rides_count                 = EXCLUDED.rides_count,
            rides_completed             = EXCLUDED.rides_completed,
            rides_cancelled             = EXCLUDED.rides_cancelled,
            fare_total_paise            = EXCLUDED.fare_total_paise,
            cancellation_fees_paise     = EXCLUDED.cancellation_fees_paise,
            computed_at                 = NOW()`
	if _, err := s.db.Exec(ctx, q,
		in.Date, in.CityID, in.PlanID,
		in.SubscriptionsCount, in.SubscriptionsRevenuePaise,
		in.RidesCount, in.RidesCompleted, in.RidesCancelled,
		in.FareTotalPaise, in.CancellationFeesPaise,
	); err != nil {
		return fmt.Errorf("upsert daily revenue: %w", err)
	}
	return nil
}

// RevenueQueryFilter is the input for the listing/aggregation queries.
type RevenueQueryFilter struct {
	Since *time.Time
	Until *time.Time
}

// RevenueByPlanRow is one row in the by-plan report.
type RevenueByPlanRow struct {
	PlanID                     *uuid.UUID `json:"plan_id,omitempty"`
	PlanName                   string     `json:"plan_name,omitempty"`
	SubscriptionsCount         int        `json:"subscriptions_count"`
	SubscriptionsRevenuePaise  int64      `json:"subscriptions_revenue_paise"`
}

// SumRevenueByPlan returns one row per plan with totals over the date
// window. Joins rider_subscription_plans for the human-friendly name.
func (s *Store) SumRevenueByPlan(ctx context.Context, f RevenueQueryFilter) ([]RevenueByPlanRow, error) {
	const q = `
        SELECT r.plan_id, COALESCE(p.name, '<unknown>'),
               COALESCE(SUM(r.subscriptions_count), 0)::int,
               COALESCE(SUM(r.subscriptions_revenue_paise), 0)::bigint
        FROM rider_daily_revenue r
        LEFT JOIN rider_subscription_plans p ON p.id = r.plan_id
        WHERE r.plan_id IS NOT NULL
          AND ($1::date IS NULL OR r.date >= $1)
          AND ($2::date IS NULL OR r.date <= $2)
          AND r.city_id IS NULL
        GROUP BY r.plan_id, p.name
        ORDER BY 4 DESC`
	rows, err := s.db.Query(ctx, q, f.Since, f.Until)
	if err != nil {
		return nil, fmt.Errorf("sum revenue by plan: %w", err)
	}
	defer rows.Close()
	var out []RevenueByPlanRow
	for rows.Next() {
		var r RevenueByPlanRow
		if err := rows.Scan(&r.PlanID, &r.PlanName, &r.SubscriptionsCount, &r.SubscriptionsRevenuePaise); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevenueByCityRow is one row in the by-city report.
type RevenueByCityRow struct {
	CityID                     *uuid.UUID `json:"city_id,omitempty"`
	CityName                   string     `json:"city_name,omitempty"`
	SubscriptionsCount         int        `json:"subscriptions_count"`
	SubscriptionsRevenuePaise  int64      `json:"subscriptions_revenue_paise"`
	RidesCount                 int        `json:"rides_count"`
	FareTotalPaise             int64      `json:"fare_total_paise"`
}

// SumRevenueByCity returns totals grouped by city.
func (s *Store) SumRevenueByCity(ctx context.Context, f RevenueQueryFilter) ([]RevenueByCityRow, error) {
	const q = `
        SELECT r.city_id, COALESCE(c.name, '<all>'),
               COALESCE(SUM(r.subscriptions_count), 0)::int,
               COALESCE(SUM(r.subscriptions_revenue_paise), 0)::bigint,
               COALESCE(SUM(r.rides_count), 0)::int,
               COALESCE(SUM(r.fare_total_paise), 0)::bigint
        FROM rider_daily_revenue r
        LEFT JOIN rider_cities c ON c.id = r.city_id
        WHERE r.city_id IS NOT NULL
          AND ($1::date IS NULL OR r.date >= $1)
          AND ($2::date IS NULL OR r.date <= $2)
          AND r.plan_id IS NULL
        GROUP BY r.city_id, c.name
        ORDER BY 4 DESC`
	rows, err := s.db.Query(ctx, q, f.Since, f.Until)
	if err != nil {
		return nil, fmt.Errorf("sum revenue by city: %w", err)
	}
	defer rows.Close()
	var out []RevenueByCityRow
	for rows.Next() {
		var r RevenueByCityRow
		if err := rows.Scan(&r.CityID, &r.CityName, &r.SubscriptionsCount, &r.SubscriptionsRevenuePaise, &r.RidesCount, &r.FareTotalPaise); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DailyRevenueComputeInput holds the raw aggregates the job computed for
// one day. ComputeDailyRevenueRaw returns this so the job can write the
// "(date, NULL, NULL)" overall row + the per-plan + per-city slices.
type DailyRevenueComputeInput struct {
	SubscriptionsCount         int
	SubscriptionsRevenuePaise  int64
	RidesCount                 int
	RidesCompleted             int
	RidesCancelled             int
	FareTotalPaise             int64
	CancellationFeesPaise      int64
}

// ComputeDailyRevenueRaw returns the all-up totals for the given UTC day
// (08:00 IST is yesterday-21:30 UTC; the caller passes the exact day
// boundaries so the job is timezone-aware).
func (s *Store) ComputeDailyRevenueRaw(ctx context.Context, dayStart, dayEnd time.Time) (DailyRevenueComputeInput, error) {
	out := DailyRevenueComputeInput{}
	const subQ = `
        SELECT COUNT(*)::int,
               COALESCE(SUM(amount * 100)::bigint, 0)
        FROM rider_subscription_payments
        WHERE status = 'verified' AND verified_at >= $1 AND verified_at < $2`
	if err := s.db.QueryRow(ctx, subQ, dayStart, dayEnd).Scan(&out.SubscriptionsCount, &out.SubscriptionsRevenuePaise); err != nil {
		return out, fmt.Errorf("daily revenue subscriptions: %w", err)
	}
	const rideQ = `
        SELECT COUNT(*)::int,
               COUNT(*) FILTER (WHERE status = 'completed')::int,
               COUNT(*) FILTER (WHERE status LIKE 'cancelled_%')::int,
               COALESCE(SUM(final_fare_paise) FILTER (WHERE status = 'completed'), 0)::bigint,
               COALESCE(SUM(cancellation_fee_paise) FILTER (WHERE status LIKE 'cancelled_%'), 0)::bigint
        FROM rider_rides
        WHERE created_at >= $1 AND created_at < $2`
	if err := s.db.QueryRow(ctx, rideQ, dayStart, dayEnd).Scan(
		&out.RidesCount, &out.RidesCompleted, &out.RidesCancelled, &out.FareTotalPaise, &out.CancellationFeesPaise,
	); err != nil {
		return out, fmt.Errorf("daily revenue rides: %w", err)
	}
	return out, nil
}

// ComputeRevenueByPlanForDay returns one input row per plan touched by
// verified payments on the day. Used by the job to populate the per-plan
// slice rows in rider_daily_revenue.
func (s *Store) ComputeRevenueByPlanForDay(ctx context.Context, dayStart, dayEnd time.Time) (map[uuid.UUID]DailyRevenueComputeInput, error) {
	const q = `
        SELECT plan_id,
               COUNT(*)::int,
               COALESCE(SUM(amount * 100)::bigint, 0)
        FROM rider_subscription_payments
        WHERE status = 'verified' AND verified_at >= $1 AND verified_at < $2
        GROUP BY plan_id`
	rows, err := s.db.Query(ctx, q, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("revenue by plan: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]DailyRevenueComputeInput{}
	for rows.Next() {
		var planID uuid.UUID
		var v DailyRevenueComputeInput
		if err := rows.Scan(&planID, &v.SubscriptionsCount, &v.SubscriptionsRevenuePaise); err != nil {
			return nil, err
		}
		out[planID] = v
	}
	return out, rows.Err()
}

// ComputeRevenueByCityForDay returns one input row per city with rides
// created in the window. Subscription revenue is not split by city
// (subscription_payments isn't keyed on city); only ride totals are.
func (s *Store) ComputeRevenueByCityForDay(ctx context.Context, dayStart, dayEnd time.Time) (map[uuid.UUID]DailyRevenueComputeInput, error) {
	const q = `
        SELECT city_id,
               COUNT(*)::int,
               COUNT(*) FILTER (WHERE status = 'completed')::int,
               COUNT(*) FILTER (WHERE status LIKE 'cancelled_%')::int,
               COALESCE(SUM(final_fare_paise) FILTER (WHERE status = 'completed'), 0)::bigint,
               COALESCE(SUM(cancellation_fee_paise) FILTER (WHERE status LIKE 'cancelled_%'), 0)::bigint
        FROM rider_rides
        WHERE city_id IS NOT NULL
          AND created_at >= $1 AND created_at < $2
        GROUP BY city_id`
	rows, err := s.db.Query(ctx, q, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("revenue by city: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]DailyRevenueComputeInput{}
	for rows.Next() {
		var cityID uuid.UUID
		var v DailyRevenueComputeInput
		if err := rows.Scan(&cityID, &v.RidesCount, &v.RidesCompleted, &v.RidesCancelled, &v.FareTotalPaise, &v.CancellationFeesPaise); err != nil {
			return nil, err
		}
		out[cityID] = v
	}
	return out, rows.Err()
}
