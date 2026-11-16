//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The introductory zero commission end to end on a real database: a creator
// approved before the launch date, a fund day on the last day of their
// window and one on the first day after it, a tip inside the window. The
// first day is priced at 0% and says why, the second at the standard rate,
// the statement shows both, and a second settlement changes nothing.
func TestLiveIntroZeroCommissionPricesTheWindowAndReSettlesIdentically(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	cfg := DefaultCreatorFundConfig()
	cfg.IntroZeroCommissionLaunch = time.Date(2025, time.February, 10, 0, 0, 0, 0, time.UTC)
	cfg.IntroZeroCommissionMonths = 3
	store := postgres.New(pool)
	svc := New(store, nil).WithCreatorFundConfig(cfg)

	// Approved a month before "launch": the window is [10 Feb, 10 May) 2025.
	approved := time.Date(2025, time.January, 5, 16, 0, 0, 0, time.UTC)
	lastIn := time.Date(2025, time.May, 9, 0, 0, 0, 0, time.UTC)
	firstOut := time.Date(2025, time.May, 10, 0, 0, 0, 0, time.UTC)
	period := MonthPeriod(2025, time.May)
	seedHistoricalRateAndBand(ctx, t, pool, lastIn)

	creator, content, fan := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE debit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1) OR credit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1)`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE owner_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM tips WHERE recipient_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_carry WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
	})

	for _, day := range []time.Time{lastIn, firstOut} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.content_daily_summary (
				content_id, day_bucket, creator_id, content_type,
				impressions, views_display, watch_time_total_ms, content_quality_score
			) VALUES ($1, $2, $3, 'long_video', 40000, 8000, 2400000000, 0.35)`,
			content, day, creator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_eligibility (creator_id, status, eligible_since)
		VALUES ($1, 'eligible', $2)
		ON CONFLICT (creator_id) DO UPDATE SET status = 'eligible', eligible_since = EXCLUDED.eligible_since`,
		creator, approved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureWallet(ctx, creator); err != nil {
		t.Fatal(err)
	}
	// A tip one minute before the window closes.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tips (id, sender_id, recipient_id, amount_paise, currency, status, created_at)
		VALUES ($1, $2, $3, 50000, 'INR', 'completed', $4)`,
		uuid.New(), fan, creator, firstOut.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	first, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("first settlement: %v", err)
	}
	if first.Status == StatusAwaitingFinalAnalytics {
		t.Skip("analytics has not finalised May 2025 in this database")
	}

	in, err := store.FindCreatorFundEarningSlot(ctx, creator, lastIn, "long_video", "IN")
	if err != nil || in == nil {
		t.Fatalf("last in-window day not accrued: %v", err)
	}
	if in.PlatformFeePaise != 0 || in.NetPaise != in.GrossPaise || in.PlatformFeeBps == nil || *in.PlatformFeeBps != 0 ||
		in.CommissionReason != CommissionReasonIntroZero || in.CommissionWindowEnd == nil || !in.CommissionWindowEnd.Equal(firstOut) {
		t.Errorf("in-window row = fee %d bps %v reason %q window end %v", in.PlatformFeePaise, in.PlatformFeeBps, in.CommissionReason, in.CommissionWindowEnd)
	}
	out, err := store.FindCreatorFundEarningSlot(ctx, creator, firstOut, "long_video", "IN")
	if err != nil || out == nil {
		t.Fatalf("first day after the window not accrued: %v", err)
	}
	if out.PlatformFeeBps == nil || *out.PlatformFeeBps != cfg.PlatformFeeBps || out.CommissionReason != CommissionReasonStandard ||
		out.PlatformFeePaise != out.GrossPaise*cfg.PlatformFeeBps/10_000 {
		t.Errorf("after-window row = fee %d of %d bps %v reason %q", out.PlatformFeePaise, out.GrossPaise, out.PlatformFeeBps, out.CommissionReason)
	}

	if first.Fund.CommissionReason != CommissionReasonMixed || len(first.Fund.Commission) != 2 {
		t.Errorf("fund line = %q %+v", first.Fund.CommissionReason, first.Fund.Commission)
	}
	if first.Tips.CommissionReason != CommissionReasonIntroZero || first.Tips.PlatformFeePaise != 0 {
		t.Errorf("tips line = %q fee %d", first.Tips.CommissionReason, first.Tips.PlatformFeePaise)
	}
	if first.IntroCommission == nil || !first.IntroCommission.AppliesToPeriod ||
		!first.IntroCommission.WindowStart.Equal(cfg.IntroZeroCommissionLaunch) || !first.IntroCommission.WindowEnd.Equal(firstOut) {
		t.Errorf("intro terms = %+v", first.IntroCommission)
	}
	if first.Fund.PlatformFeePaise != out.PlatformFeePaise {
		t.Errorf("fund fee %d, want only the after-window day's %d", first.Fund.PlatformFeePaise, out.PlatformFeePaise)
	}

	// Re-settle: same money, nothing new credited, same stored breakdown.
	storedFirst, err := store.GetPeriodSettlement(ctx, creator, period.Key, "IN")
	if err != nil || storedFirst == nil {
		t.Fatalf("settlement row: %v", err)
	}
	second, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("second settlement: %v", err)
	}
	if second.NewlyCredited || second.CreditedPaise != first.CreditedPaise || second.PlatformFeePaise != first.PlatformFeePaise || second.NetPaise != first.NetPaise {
		t.Errorf("re-settlement changed the money: first %+v second %+v", first, second)
	}
	storedSecond, err := store.GetPeriodSettlement(ctx, creator, period.Key, "IN")
	if err != nil || storedSecond == nil {
		t.Fatalf("settlement row after re-run: %v", err)
	}
	// jsonb text is normalised by Postgres, so equal documents compare equal.
	if storedFirst.CommissionJSON == "" || storedFirst.CommissionJSON != storedSecond.CommissionJSON {
		t.Errorf("stored commission changed on re-settlement:\n%s\n%s", storedFirst.CommissionJSON, storedSecond.CommissionJSON)
	}

	// The creator-facing read shows the same fields.
	read, err := svc.GetCreatorPeriodStatement(ctx, creator, period.Key)
	if err != nil || read == nil {
		t.Fatalf("read statement: %v", err)
	}
	if read.Fund.CommissionReason != CommissionReasonMixed || read.IntroCommission == nil {
		t.Errorf("read statement lost the commission fields: %+v", read.Fund)
	}
	var sawIntroRow bool
	for _, r := range read.FundBreakdown {
		if r.CommissionReason == CommissionReasonIntroZero && r.PlatformFeeBps != nil && *r.PlatformFeeBps == 0 {
			sawIntroRow = true
		}
	}
	if !sawIntroRow {
		t.Error("the fund breakdown does not show the intro day's rate")
	}
}
