//go:build integration

package service

import (
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Review 2026-11-07, against the real schema: analytics' finality line
// (analytics migration 016) decides what is priced, a period waits for it,
// and a suspended creator's period is held, then released.

func TestAccrualSkipsADayAnalyticsMayStillReRoll(t *testing.T) {
	ctx, pool := openTestPool(t)
	svc := New(postgres.New(pool), nil)

	finalBefore, ok, err := postgres.New(pool).AnalyticsFinalBefore(ctx)
	if err != nil || !ok {
		t.Fatalf("analytics finality: %v (known=%v); the analytics migrations seed a watermark", err, ok)
	}
	today := utcDay(time.Now())
	if !finalBefore.Before(today) {
		t.Fatalf("final_before %s must be behind today %s", finalBefore.Format("2006-01-02"), today.Format("2006-01-02"))
	}

	day := today.AddDate(0, 0, -1) // inside the 48-hour window
	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "flick", 1_000, 5_000, 0.4)

	res, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil {
		t.Fatalf("an open day is not an error: %v", err)
	}
	if !res.NotFinal || res.Accrued != 0 {
		t.Fatalf("open day = %+v, want NotFinal and nothing accrued", res)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM creator_fund_earnings WHERE creator_id = $1`, creator).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d earnings rows written for a day that is not final", rows)
	}

	// The period that contains it waits, and says so.
	st, err := svc.SettleCreatorFundPeriod(ctx, creator, PeriodContaining(day, CadenceMonthly))
	if err != nil {
		t.Fatalf("a period with open days must wait, not fail: %v", err)
	}
	if st.Status != StatusAwaitingFinalAnalytics || st.Persisted {
		t.Fatalf("open period = %+v", st)
	}
}

func TestFinalDayAccruesOnce(t *testing.T) {
	ctx, pool := openTestPool(t)
	svc := New(postgres.New(pool), nil)
	day := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	seedRateAndBandFor(ctx, t, pool, "flick", 300, day, false)
	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "flick", 1_000, 5_000, 0.4)

	first, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil || first.Accrued != 1 || first.NotFinal {
		t.Fatalf("first accrual of a final day = %+v, %v", first, err)
	}
	second, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil || second.Accrued != 0 || second.Skipped[SkipAlreadyAccrued] != 1 {
		t.Fatalf("second accrual = %+v, %v; want already_accrued", second, err)
	}
}

func TestSuspendedCreatorsPeriodIsHeldThenReleased(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	period := MonthPeriod(2024, time.July)
	day := time.Date(2024, 7, 10, 0, 0, 0, 0, time.UTC)
	seedRateAndBandFor(ctx, t, pool, "flick", 300, day, false)
	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "flick", 10_000, 50_000, 0.4)

	if res, err := svc.AccrueCreatorFundDay(ctx, creator, day); err != nil || res.Accrued != 1 {
		t.Fatalf("accrue while eligible: %+v, %v", res, err)
	}
	// Suspended after the day accrued, before the period settles.
	if err := store.SetCreatorFundSuspension(ctx, creator, "integration test"); err != nil {
		t.Fatal(err)
	}

	balance := func() int64 {
		t.Helper()
		var b int64
		err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT balance FROM creator_ledger WHERE user_id = $1), 0)`, creator).Scan(&b)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	for run := 1; run <= 2; run++ {
		st, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
		if err != nil {
			t.Fatalf("run %d: a held period is not an error: %v", run, err)
		}
		if st.Status != StatusHeld || st.HoldReason == "" || st.NewlyCredited || st.CreditedPaise != 0 || st.PendingPaise <= 0 || !st.Persisted {
			t.Fatalf("run %d: held statement = status %q hold %q credited %d pending %d persisted %v",
				run, st.Status, st.HoldReason, st.CreditedPaise, st.PendingPaise, st.Persisted)
		}
		if b := balance(); b != 0 {
			t.Fatalf("run %d: a suspended creator's wallet moved by %d", run, b)
		}
	}

	// Lifted: the release sweep pays exactly what was held.
	if err := store.ClearCreatorFundSuspension(ctx, creator); err != nil {
		t.Fatal(err)
	}
	var owed int64
	if err := pool.QueryRow(ctx, `SELECT pending_paise FROM creator_fund_period_settlements WHERE creator_id = $1 AND period_key = $2`,
		creator, period.Key).Scan(&owed); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleReleasedHolds(ctx, 10_000, nil); err != nil {
		t.Fatalf("release sweep: %v", err)
	}
	if b := balance(); b != owed {
		t.Fatalf("after release the wallet holds %d, want the %d that was held", b, owed)
	}
	again, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil || again.Status == StatusHeld || again.PendingPaise != 0 || again.CreditedPaise != owed || again.NewlyCredited {
		t.Fatalf("re-run after release = %+v, %v", again, err)
	}
}

// A partial membership refund debits the creator; the period statement's
// subscription line must show the same net, not the gross it was before.
func TestPartialMembershipRefundIsOnTheStatement(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	creator, payment := uuid.New(), uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	at := time.Date(2024, 8, 12, 10, 0, 0, 0, time.UTC)
	// transactions.wallet_id references creator_ledger(user_id): the
	// creator's ledger row exists before any of its transactions, as
	// EnsureWallet makes it on the real paths. 7000 is what the creator kept.
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_ledger (user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
		VALUES ($1, 7000, 10000, 0, 'INR', false, $2, $2)`, creator, at); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'earning', 10000, 'INR', 'completed', 'subscription', $3, 'Membership join', $4),
		       ($5, $2, 'refund',   3000, 'INR', 'completed', 'subscription_refund', $3, 'Membership refund', $6)`,
		uuid.New(), creator, payment.String(), at, uuid.New(), at.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	count, total, err := store.SumSubscriptionEarningsBetween(ctx, creator,
		time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || total != 7000 {
		t.Fatalf("subscription line = %d charges / %d paise, want 1 / 7000 (10000 less the 3000 refunded)", count, total)
	}
}
