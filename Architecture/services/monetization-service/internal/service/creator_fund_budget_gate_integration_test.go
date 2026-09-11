//go:build integration

package service

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// 3C — a period with no budget row
// ---------------------------------------------------------------------------
//
// The plan's context says the mechanism "refuses to accrue against a
// period with no cap set"; Phase 2C shipped "no row = uncapped, warn
// once". Both are right, for different modes. While payouts are off the
// numbers are estimates and an uncapped estimate is harmless, so the
// warning stands. Once payouts are on, an accrual is a claim on real
// money, and a claim against a fund nobody sized is refused.

func TestNoBudgetRefusedWhenPayoutsEnabled(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	day := time.Date(2025, 3, 12, 0, 0, 0, 0, time.UTC)
	period := PeriodContaining(day, svc.CreatorFundConfigSnapshot().SettlementCadence)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, day, false)
	cleanupBudget(ctx, t, pool, period.Key)
	if _, err := pool.Exec(ctx, `DELETE FROM creator_fund_budgets WHERE period_key = $1 AND region_code = 'IN'`, period.Key); err != nil {
		t.Fatal(err)
	}

	creator, content := uuid.New(), uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, content, creator, day, "long_video", 1000, 4000, 0.4)

	_, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err == nil {
		t.Fatal("accrued against a period with no budget row while payouts are enabled")
	}
	if !errors.Is(err, ErrNoBudget) {
		t.Fatalf("error = %v, want NO_BUDGET", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM creator_fund_earnings WHERE creator_id = $1`, creator).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("refused accrual wrote %d earnings rows", rows)
	}
}

func TestNoBudgetUncappedWhenPayoutsDisabled(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil) // the default: payouts off

	day := time.Date(2025, 3, 13, 0, 0, 0, 0, time.UTC)
	period := PeriodContaining(day, svc.CreatorFundConfigSnapshot().SettlementCadence)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, day, false)
	cleanupBudget(ctx, t, pool, period.Key)
	if _, err := pool.Exec(ctx, `DELETE FROM creator_fund_budgets WHERE period_key = $1 AND region_code = 'IN'`, period.Key); err != nil {
		t.Fatal(err)
	}

	creator, content := uuid.New(), uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, content, creator, day, "long_video", 1000, 4000, 0.4)

	res, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil {
		t.Fatalf("uncapped accrual with payouts off: %v", err)
	}
	if res.Accrued != 1 {
		t.Fatalf("accrued = %d, want 1 (uncapped, warned once)", res.Accrued)
	}
	var gross int64
	if err := pool.QueryRow(ctx, `SELECT gross_paise FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`, creator, day).Scan(&gross); err != nil {
		t.Fatal(err)
	}
	if gross != 5000 {
		t.Fatalf("gross_paise = %d, want 5000 (1000 views at RPM 5000)", gross)
	}
}
