// Admin reports — Sprint 4. Surfaces aggregates over rider_daily_revenue
// (revenue-by-plan, revenue-by-city) plus per-cohort retention/booking
// helpers computed against the source rides table.
//
// All four endpoints are thin pass-throughs to the corresponding store
// methods so the service layer stays opinionated about input validation
// (date parsing, cohort-month boundaries) and returns typed rows.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §17.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/store"
)

// RevenueByPlan returns one row per plan with totals over [since, until].
// Either bound may be zero (=> open-ended).
func (s *Service) RevenueByPlan(ctx context.Context, since, until time.Time) ([]store.RevenueByPlanRow, error) {
	f := store.RevenueQueryFilter{}
	if !since.IsZero() {
		t := since
		f.Since = &t
	}
	if !until.IsZero() {
		t := until
		f.Until = &t
	}
	return s.store.SumRevenueByPlan(ctx, f)
}

// RevenueByCity returns one row per city with totals over [since, until].
func (s *Service) RevenueByCity(ctx context.Context, since, until time.Time) ([]store.RevenueByCityRow, error) {
	f := store.RevenueQueryFilter{}
	if !since.IsZero() {
		t := since
		f.Since = &t
	}
	if !until.IsZero() {
		t := until
		f.Until = &t
	}
	return s.store.SumRevenueByCity(ctx, f)
}

// PartnerCohortRetention computes the 1m/2m/3m retention curve for the
// partner cohort that signed up in `cohortMonth` (UTC YYYY-MM).
func (s *Service) PartnerCohortRetention(ctx context.Context, cohortMonth string) (*store.CohortRetentionRow, error) {
	t, err := parseCohortMonth(cohortMonth)
	if err != nil {
		return nil, err
	}
	return s.store.PartnerCohortRetention(ctx, t)
}

// CustomerCohortBookingRate computes the avg-rides-per-customer for the
// next 1/2/3 months for customers whose first ride lands in `cohortMonth`.
func (s *Service) CustomerCohortBookingRate(ctx context.Context, cohortMonth string) (*store.CustomerCohortBookingRateRow, error) {
	t, err := parseCohortMonth(cohortMonth)
	if err != nil {
		return nil, err
	}
	return s.store.CustomerCohortBookingRate(ctx, t)
}

// ListCronRuns is the operational visibility surface — admins call it to
// see when each job last ran successfully.
func (s *Service) ListCronRuns(ctx context.Context, job string, since *time.Time, limit int) ([]store.CronRun, error) {
	return s.store.ListCronRuns(ctx, store.CronRunFilter{
		Job:   strings.TrimSpace(job),
		Since: since,
		Limit: limit,
	})
}

// parseCohortMonth turns a "YYYY-MM" string into the UTC midnight at the
// first of that month. Errors with "invalid:" so handler maps to 400.
func parseCohortMonth(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("invalid: cohort_month required")
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid: cohort_month must be YYYY-MM")
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), nil
}

// ErrInvalidReportArg is returned by the reports endpoints for bad input.
// Reused via the "invalid:" prefix convention.
var ErrInvalidReportArg = errors.New("invalid report argument")
