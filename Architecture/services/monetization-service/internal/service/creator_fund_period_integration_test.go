//go:build integration

package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The whole feature in one test: a creator earning from all three streams
// over a settlement period, settled once, then settled again.
//
// The second settlement is the point. Tips and subscriptions are credited
// to the wallet the moment they happen, so a monthly run that also
// credited them would pay every tip twice — and the place that would show
// up is exactly here, on a re-run of a period that already paid.
func TestLivePeriodSettlementPaysFundOnceAndNeverPaysTipsTwice(t *testing.T) {
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// t.Cleanup, not defer: deferred calls run BEFORE registered cleanups,
	// so `defer pool.Close()` closed the pool out from under every fixture
	// teardown below and left their rows behind in the shared database.
	// Registered here first, LIFO puts the close last.
	t.Cleanup(pool.Close)

	store := postgres.New(pool)
	svc := New(store, nil)

	// A period well in the past so it cannot collide with anything the
	// live stack is producing, and so its rate/band window is explicit.
	period := MonthPeriod(2025, time.March)
	day := time.Date(2025, 3, 12, 0, 0, 0, 0, time.UTC)
	seedHistoricalRateAndBand(ctx, t, pool, day)

	creator, content := uuid.New(), uuid.New()
	fan, subscriber := uuid.New(), uuid.New()
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{creator, fan, subscriber} {
			_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE debit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1) OR credit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1)`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE owner_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, id)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM tips WHERE recipient_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
	})

	// Views in the period.
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary (
			content_id, day_bucket, creator_id, content_type,
			impressions, plays, views_display, unique_viewers,
			watch_time_total_ms, avg_percent_viewed, completion_rate,
			likes, comments, shares, saves,
			view_score_total, content_quality_score
		) VALUES ($1, $2, $3, 'long_video', 40000, 9000, 8000, 7000,
		          2400000000, 50, 0.4, 300, 60, 150, 100, 4000, 0.35)`,
		content, day, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_eligibility (creator_id, status, eligible_since)
		VALUES ($1, 'eligible', NOW())
		ON CONFLICT (creator_id) DO UPDATE SET status = 'eligible'`, creator); err != nil {
		t.Fatal(err)
	}

	// Tips, already credited at send time — recorded here the way SendTip
	// records them (row plus wallet credit), because that is the state the
	// settlement must not pay for a second time.
	const tipA, tipB = int64(150_000), int64(75_000)
	const subPrice = int64(29_900)
	if _, err := store.EnsureWallet(ctx, creator); err != nil {
		t.Fatal(err)
	}
	for _, amt := range []int64{tipA, tipB} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO tips (id, sender_id, recipient_id, amount_paise, currency, status, created_at)
			VALUES ($1, $2, $3, $4, 'INR', 'completed', $5)`,
			uuid.New(), fan, creator, amt, day.Add(9*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE creator_ledger SET balance = balance + $2, lifetime_earnings = lifetime_earnings + $2
			WHERE user_id = $1`, creator, amt); err != nil {
			t.Fatal(err)
		}
	}
	// A subscription charge, likewise already credited.
	if _, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status,
		                          reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'earning', $3, 'INR', 'completed', 'subscription', $4, 'Earning from subscription', $5)`,
		uuid.New(), creator, subPrice, uuid.New().String(), day.Add(10*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance + $2, lifetime_earnings = lifetime_earnings + $2
		WHERE user_id = $1`, creator, subPrice); err != nil {
		t.Fatal(err)
	}

	balanceBefore := walletBalance(ctx, t, pool, creator)

	// --- first settlement -------------------------------------------------
	first, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("first settlement: %v", err)
	}
	if !first.NewlyCredited {
		t.Fatal("first settlement credited nothing")
	}
	if first.Fund.GrossPaise <= 0 {
		t.Fatalf("fund stream is empty: %+v", first.Fund)
	}
	if first.Tips.GrossPaise != tipA+tipB {
		t.Errorf("tips gross = %d, want %d", first.Tips.GrossPaise, tipA+tipB)
	}
	if first.Tips.Count != 2 {
		t.Errorf("tips count = %d, want 2", first.Tips.Count)
	}
	if first.Subs.GrossPaise != subPrice {
		t.Errorf("subscription gross = %d, want %d", first.Subs.GrossPaise, subPrice)
	}
	// Arithmetically checkable: the three streams are the whole of gross.
	if got, want := first.GrossPaise, first.Fund.GrossPaise+tipA+tipB+subPrice; got != want {
		t.Errorf("period gross = %d, want fund+tips+subs = %d", got, want)
	}
	// The platform share is stated per stream, and the two zero ones are
	// zero because that is what the live tip and subscription paths take.
	if first.Fund.PlatformFeeBps != svc.CreatorFundConfigSnapshot().PlatformFeeBps {
		t.Errorf("fund platform fee bps = %d", first.Fund.PlatformFeeBps)
	}
	if first.Tips.PlatformFeeBps != 0 || first.Tips.PlatformFeePaise != 0 {
		t.Errorf("tips platform share changed: %d bps / %d paise", first.Tips.PlatformFeeBps, first.Tips.PlatformFeePaise)
	}
	if first.Subs.PlatformFeeBps != 0 || first.Subs.PlatformFeePaise != 0 {
		t.Errorf("subscription platform share changed: %d bps / %d paise", first.Subs.PlatformFeeBps, first.Subs.PlatformFeePaise)
	}
	// THE invariant: this run pays the fund and nothing else.
	if first.PendingPaise != 0 {
		t.Errorf("settlement left %d paise pending", first.PendingPaise)
	}
	if first.CreditedPaise != first.Fund.NetPaise {
		t.Errorf("credited %d, want the fund net %d — anything more is a second payment of tips or subscriptions",
			first.CreditedPaise, first.Fund.NetPaise)
	}
	if first.AlreadyCreditedPaise != tipA+tipB+subPrice {
		t.Errorf("already credited = %d, want %d", first.AlreadyCreditedPaise, tipA+tipB+subPrice)
	}

	balanceAfterFirst := walletBalance(ctx, t, pool, creator)
	if got, want := balanceAfterFirst-balanceBefore, first.Fund.NetPaise; got != want {
		t.Fatalf("wallet moved by %d, want exactly the fund net %d", got, want)
	}

	// --- re-run, with the tips still sitting there ------------------------
	second, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("second settlement: %v", err)
	}
	if second.NewlyCredited {
		t.Error("re-running a settled period credited again")
	}
	if got := walletBalance(ctx, t, pool, creator); got != balanceAfterFirst {
		t.Fatalf("re-run moved the wallet from %d to %d", balanceAfterFirst, got)
	}
	if second.CreditedPaise != first.CreditedPaise {
		t.Errorf("credited_paise changed on re-run: %d -> %d", first.CreditedPaise, second.CreditedPaise)
	}
	if second.Tips.GrossPaise != first.Tips.GrossPaise {
		t.Errorf("tips total changed on re-run: %d -> %d", first.Tips.GrossPaise, second.Tips.GrossPaise)
	}

	// A third for good measure, plus the settlement-row count: the new
	// idempotency key is (creator, period, region), so there must be one.
	if _, err := svc.SettleCreatorFundPeriod(ctx, creator, period); err != nil {
		t.Fatalf("third settlement: %v", err)
	}
	if got := walletBalance(ctx, t, pool, creator); got != balanceAfterFirst {
		t.Fatalf("third run moved the wallet to %d", got)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM creator_fund_period_settlements WHERE creator_id = $1 AND period_key = $2`,
		creator, period.Key).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d settlement rows for one period, want 1", rows)
	}
	// And the wallet-side transaction: one settlement payment, not three.
	var txns int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE wallet_id = $1 AND type = 'creator_fund_period_settlement'`,
		creator).Scan(&txns); err != nil {
		t.Fatal(err)
	}
	if txns != 1 {
		t.Fatalf("%d settlement transactions, want 1", txns)
	}

	fmt.Printf("\nperiod %s\n  %s\n  credited by this run: %d paise; already in wallet: %d paise\n",
		period.Key, first.Explanation, first.CreditedPaise, first.AlreadyCreditedPaise)
}

// An ineligible creator with no tips accrues nothing, is credited
// nothing, and does not even get a statement row.
func TestLivePeriodSettlementPaysAnIneligibleCreatorNothing(t *testing.T) {
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// t.Cleanup, not defer: deferred calls run BEFORE registered cleanups,
	// so `defer pool.Close()` closed the pool out from under every fixture
	// teardown below and left their rows behind in the shared database.
	// Registered here first, LIFO puts the close last.
	t.Cleanup(pool.Close)

	svc := New(postgres.New(pool), nil)
	period := MonthPeriod(2025, time.April)
	day := time.Date(2025, 4, 9, 0, 0, 0, 0, time.UTC)
	seedHistoricalRateAndBand(ctx, t, pool, day)

	creator, content := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
	})

	// Real views — the creator is simply not in the fund.
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary (
			content_id, day_bucket, creator_id, content_type,
			impressions, views_display, watch_time_total_ms, content_quality_score
		) VALUES ($1, $2, $3, 'long_video', 20000, 5000, 900000000, 0.5)`,
		content, day, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_eligibility (creator_id, status)
		VALUES ($1, 'ineligible')
		ON CONFLICT (creator_id) DO UPDATE SET status = 'ineligible'`, creator); err != nil {
		t.Fatal(err)
	}

	st, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatal(err)
	}
	if st.CreditedPaise != 0 || st.GrossPaise != 0 || st.NetPaise != 0 {
		t.Fatalf("ineligible creator was paid: %+v", st)
	}
	if st.Persisted {
		t.Error("a statement row was written for a creator with no revenue")
	}
	if st.Status != "no_activity" {
		t.Errorf("status = %q, want no_activity", st.Status)
	}
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT balance FROM creator_ledger WHERE user_id = $1), 0)::BIGINT`, creator).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("ineligible creator's wallet holds %d paise", balance)
	}
}

func walletBalance(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int64 {
	t.Helper()
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT balance FROM creator_ledger WHERE user_id = $1), 0)::BIGINT`, userID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	return balance
}
