//go:build integration

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func cleanupCreatorMoney(ctx context.Context, t *testing.T, pool *pgxpool.Pool, creator uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE debit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1) OR credit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1)`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE owner_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_carry WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
	})
}

func cleanupBudget(ctx context.Context, t *testing.T, pool *pgxpool.Pool, periodKey string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_budgets WHERE period_key = $1 AND region_code = 'IN'`, periodKey)
	})
}

// ---------------------------------------------------------------------------
// 2A — correction primitives
// ---------------------------------------------------------------------------

// An adjustment is identified by its cause, not by the call. Posting the
// same cause twice returns the first transaction and moves no money; a
// different cause is a different adjustment. Negative balances are allowed
// because they are the truth after a reversal.
func TestPostAdjustmentIdempotentOnCause(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	if _, err := store.EnsureWallet(ctx, creator); err != nil {
		t.Fatal(err)
	}

	cause := AdjustmentCause{Kind: "test_credit", Ref: uuid.NewString()}
	first, err := svc.PostAdjustment(ctx, creator, 1_000, "test credit", cause)
	if err != nil {
		t.Fatalf("first adjustment: %v", err)
	}
	if first.Type != "adjustment" || first.AmountPaise != 1_000 {
		t.Fatalf("first adjustment row = %+v", first)
	}
	if got := walletBalance(ctx, t, pool, creator); got != 1_000 {
		t.Fatalf("balance after first adjustment = %d, want 1000", got)
	}

	// Same cause again: the row we already have, and the wallet untouched.
	again, err := svc.PostAdjustment(ctx, creator, 1_000, "test credit (retry)", cause)
	if err != nil {
		t.Fatalf("repeat adjustment: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("repeat adjustment created a second transaction: %s then %s", first.ID, again.ID)
	}
	if got := walletBalance(ctx, t, pool, creator); got != 1_000 {
		t.Fatalf("balance after repeated cause = %d, want still 1000", got)
	}

	// A different cause is a different adjustment, and may go negative.
	other := AdjustmentCause{Kind: "test_debit", Ref: uuid.NewString()}
	debit, err := svc.PostAdjustment(ctx, creator, -1_500, "test debit", other)
	if err != nil {
		t.Fatalf("debit adjustment: %v", err)
	}
	if debit.ID == first.ID || debit.AmountPaise != -1_500 {
		t.Fatalf("debit adjustment row = %+v", debit)
	}
	if got := walletBalance(ctx, t, pool, creator); got != -500 {
		t.Fatalf("balance after debit = %d, want -500 (a negative balance is the true state)", got)
	}

	// Exactly one wallet transaction per cause, keyed on the cause.
	var txns int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions WHERE wallet_id = $1 AND type = 'adjustment'`, creator).Scan(&txns); err != nil {
		t.Fatal(err)
	}
	if txns != 2 {
		t.Fatalf("%d adjustment transactions, want 2", txns)
	}
	var keyed int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions WHERE idempotency_key = $1`, "adj:"+cause.Kind+":"+cause.Ref).Scan(&keyed); err != nil {
		t.Fatal(err)
	}
	if keyed != 1 {
		t.Fatalf("%d transactions carry the cause key, want 1", keyed)
	}
	// And one double-entry leg per adjustment, keyed the same way.
	var legs int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM ledger_entries WHERE idempotency_key IN ($1, $2)`,
		"adj:"+cause.Kind+":"+cause.Ref, "adj:"+other.Kind+":"+other.Ref).Scan(&legs); err != nil {
		t.Fatal(err)
	}
	if legs != 2 {
		t.Fatalf("%d ledger legs for two adjustments, want 2", legs)
	}
	// Zero and unnamed causes are refused rather than silently keyed on "".
	if _, err := svc.PostAdjustment(ctx, creator, 0, "nothing", other); err == nil {
		t.Error("a zero adjustment was accepted")
	}
	if _, err := svc.PostAdjustment(ctx, creator, 10, "unnamed", AdjustmentCause{}); err == nil {
		t.Error("an adjustment with no cause was accepted")
	}
}

// Reversing a credited earning: the row is marked, the wallet gives the
// net back through a keyed adjustment, and the period statement no longer
// counts the row on its fund line while still reporting it as a memo.
func TestReverseFundEarningDropsFromStatement(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	period := MonthPeriod(2025, time.August)
	dayA := time.Date(2025, 8, 12, 0, 0, 0, 0, time.UTC)
	dayB := dayA.AddDate(0, 0, 1)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, dayA, false)

	creator, content := uuid.New(), uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, content, creator, dayA, "long_video", 2000, 5000, 0.4)
	insertDailySummary(ctx, t, pool, content, creator, dayB, "long_video", 1000, 5000, 0.4)

	first, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if !first.NewlyCredited || first.Fund.Count != 2 {
		t.Fatalf("first settlement: %+v", first.Fund)
	}
	balanceAfterSettle := walletBalance(ctx, t, pool, creator)

	var earningA uuid.UUID
	var grossA, netA int64
	if err := pool.QueryRow(ctx, `
		SELECT id, gross_paise, net_paise FROM creator_fund_earnings
		WHERE creator_id = $1 AND day_bucket = $2 AND content_type = 'long_video'`,
		creator, dayA).Scan(&earningA, &grossA, &netA); err != nil {
		t.Fatal(err)
	}
	if netA <= 0 {
		t.Fatalf("day A net = %d", netA)
	}

	res, err := svc.ReverseFundEarning(ctx, earningA, "no hourly rows behind these views")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if res.AlreadyReversed {
		t.Fatal("first reversal reported already_reversed")
	}
	if !res.MoneyMoved || res.Adjustment == nil || res.Adjustment.AmountPaise != -netA {
		t.Fatalf("reversal of a CREDITED row must post -net through an adjustment: %+v", res)
	}
	if got := walletBalance(ctx, t, pool, creator); got != balanceAfterSettle-netA {
		t.Fatalf("wallet after reversal = %d, want %d (settled balance minus the row's net)", got, balanceAfterSettle-netA)
	}
	if res.BalanceAfter != balanceAfterSettle-netA {
		t.Errorf("balance_after reported %d, want %d", res.BalanceAfter, balanceAfterSettle-netA)
	}

	// The row itself: marked, linked, money columns untouched.
	var status, reason string
	var reversedAt *time.Time
	var reversalTx *uuid.UUID
	var grossStill, netStill int64
	if err := pool.QueryRow(ctx, `
		SELECT status, COALESCE(reversal_reason, ''), reversed_at, reversal_transaction_id, gross_paise, net_paise
		FROM creator_fund_earnings WHERE id = $1`, earningA).Scan(&status, &reason, &reversedAt, &reversalTx, &grossStill, &netStill); err != nil {
		t.Fatal(err)
	}
	if status != "reversed" || reversedAt == nil || reason == "" {
		t.Fatalf("row after reversal: status=%q reversed_at=%v reason=%q", status, reversedAt, reason)
	}
	if reversalTx == nil || *reversalTx != res.Adjustment.ID {
		t.Fatalf("reversal_transaction_id = %v, want the adjustment %s", reversalTx, res.Adjustment.ID)
	}
	if grossStill != grossA || netStill != netA {
		t.Fatalf("a settled row's money columns were rewritten: gross %d->%d net %d->%d", grossA, grossStill, netA, netStill)
	}
	// Keyed on the cause, so a retry cannot post it twice.
	var legs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE idempotency_key = $1`,
		"adj:creator_fund_earning_reversal:"+earningA.String()).Scan(&legs); err != nil {
		t.Fatal(err)
	}
	if legs != 1 {
		t.Fatalf("%d ledger legs for the reversal, want 1", legs)
	}

	// The platform's share of the reversed income must go too: a fee leg
	// mirrored back out of platform_revenue_fees, keyed adj_fee:<cause>,
	// in the same transaction as the net. Otherwise the platform keeps 30%
	// of income that no longer exists.
	var feeA int64
	if err := pool.QueryRow(ctx, `SELECT platform_fee_paise FROM creator_fund_earnings WHERE id = $1`, earningA).Scan(&feeA); err != nil {
		t.Fatal(err)
	}
	if feeA <= 0 {
		t.Fatalf("day A platform fee = %d; the fixture must carry a fee for this assertion to mean anything", feeA)
	}
	feeKey := "adj_fee:creator_fund_earning_reversal:" + earningA.String()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE idempotency_key = $1`, feeKey) })
	var feeLegs int
	var feeLegAmount int64
	var feeDebitType, feeCreditType string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(MAX(l.amount_paise), 0),
		       COALESCE(MAX(d.account_type), ''), COALESCE(MAX(c.account_type), '')
		FROM ledger_entries l
		LEFT JOIN accounts d ON d.id = l.debit_account_id
		LEFT JOIN accounts c ON c.id = l.credit_account_id
		WHERE l.idempotency_key = $1`, feeKey).Scan(&feeLegs, &feeLegAmount, &feeDebitType, &feeCreditType); err != nil {
		t.Fatal(err)
	}
	if feeLegs != 1 || feeLegAmount != feeA {
		t.Fatalf("%d fee reversal leg(s) totalling %d under %s, want 1 leg of %d", feeLegs, feeLegAmount, feeKey, feeA)
	}
	if feeDebitType != "platform_revenue_fees" || feeCreditType != "platform_revenue" {
		t.Fatalf("fee leg runs %s -> %s, want platform_revenue_fees -> platform_revenue", feeDebitType, feeCreditType)
	}
	if res.FeeReversedPaise != feeA || res.NetReversedPaise != netA {
		t.Fatalf("reversal result reports fee %d / net %d, want %d / %d", res.FeeReversedPaise, res.NetReversedPaise, feeA, netA)
	}

	// Reversing again is a no-op that says so.
	again, err := svc.ReverseFundEarning(ctx, earningA, "second attempt")
	if err != nil {
		t.Fatalf("second reversal: %v", err)
	}
	if !again.AlreadyReversed || again.MoneyMoved {
		t.Fatalf("second reversal moved money or did not report already_reversed: %+v", again)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE idempotency_key = $1`, feeKey).Scan(&feeLegs); err != nil {
		t.Fatal(err)
	}
	if feeLegs != 1 {
		t.Fatalf("second reversal left %d fee legs, want still 1", feeLegs)
	}
	if got := walletBalance(ctx, t, pool, creator); got != balanceAfterSettle-netA {
		t.Fatalf("second reversal moved the wallet to %d", got)
	}

	// The statement: fund line drops the row; the memo line carries it.
	second, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("re-settle: %v", err)
	}
	if second.NewlyCredited {
		t.Error("re-settling after a reversal credited money")
	}
	if second.Fund.Count != 1 || second.Fund.GrossPaise != first.Fund.GrossPaise-grossA {
		t.Errorf("fund line after reversal = %d rows / %d gross, want 1 row / %d", second.Fund.Count, second.Fund.GrossPaise, first.Fund.GrossPaise-grossA)
	}
	if second.ReversedPaise != netA || second.FundReversedRows != 1 {
		t.Errorf("reversed memo = %d paise / %d rows, want %d / 1", second.ReversedPaise, second.FundReversedRows, netA)
	}
	if second.CreditedPaise != first.CreditedPaise-netA {
		t.Errorf("credited_paise after reversal = %d, want %d", second.CreditedPaise, first.CreditedPaise-netA)
	}
	if err := CheckStatementArithmetic(second); err != nil {
		t.Fatalf("statement no longer balances: %v", err)
	}
	// The re-accrual path must not silently re-insert the day: the reversed
	// row keeps its UNIQUE slot on purpose.
	var rowsForDayA int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`,
		creator, dayA).Scan(&rowsForDayA); err != nil {
		t.Fatal(err)
	}
	if rowsForDayA != 1 {
		t.Fatalf("%d rows for the reversed day, want 1", rowsForDayA)
	}
	// The adjustment landed today, so it belongs to the CURRENT period's
	// statement, not August 2025's.
	if second.AdjustmentsPaise != 0 {
		t.Errorf("August 2025 statement shows adjustments_paise = %d; the adjustment landed now, not then", second.AdjustmentsPaise)
	}
	current, err := svc.SettleCreatorFundPeriod(ctx, creator, PeriodContaining(time.Now().UTC(), CadenceMonthly))
	if err != nil {
		t.Fatalf("settle current period: %v", err)
	}
	if current.AdjustmentsPaise != -netA || current.AdjustmentsCount != 1 {
		t.Errorf("current period adjustments = %d paise / %d, want %d / 1", current.AdjustmentsPaise, current.AdjustmentsCount, -netA)
	}
	if current.WalletMovementPaise != -netA {
		t.Errorf("current period wallet movement = %d, want %d", current.WalletMovementPaise, -netA)
	}
	if err := CheckStatementArithmetic(current); err != nil {
		t.Fatalf("current statement does not balance: %v", err)
	}
	if _, err := svc.ReverseFundEarning(ctx, uuid.New(), "nope"); !errors.Is(err, ErrEarningNotFound) {
		t.Errorf("reversing an unknown earning: err = %v, want ErrEarningNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// 2C — budget cap, prospective stop
// ---------------------------------------------------------------------------

// Three days at 5,000 paise gross each against an 8,000 paise cap. Day one
// accrues in full; day two gets exactly the 3,000 that remain and marks the
// cap reached; day three is recorded at zero with the reason. Day one is
// never touched: nothing already earned is reduced.
func TestBudgetProspectiveStop(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	period := MonthPeriod(2025, time.June)
	day1 := time.Date(2025, 6, 10, 0, 0, 0, 0, time.UTC)
	day2, day3 := day1.AddDate(0, 0, 1), day1.AddDate(0, 0, 2)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, day1, false)
	cleanupBudget(ctx, t, pool, period.Key)

	creator, content := uuid.New(), uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	for _, d := range []time.Time{day1, day2, day3} {
		insertDailySummary(ctx, t, pool, content, creator, d, "long_video", 1000, 4000, 0.4)
	}
	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, RegionCode: "IN", CapPaise: 8_000, Notes: "test cap"}, nil); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	r1, err := svc.AccrueCreatorFundDay(ctx, creator, day1)
	if err != nil {
		t.Fatalf("day 1: %v", err)
	}
	if r1.Accrued != 1 || len(r1.Skipped) != 0 {
		t.Fatalf("day 1 = %+v, want one full accrual", r1)
	}
	r2, err := svc.AccrueCreatorFundDay(ctx, creator, day2)
	if err != nil {
		t.Fatalf("day 2: %v", err)
	}
	if r2.Skipped[SkipBudgetCapped] != 1 {
		t.Fatalf("day 2 = %+v, want budget_capped recorded", r2)
	}
	r3, err := svc.AccrueCreatorFundDay(ctx, creator, day3)
	if err != nil {
		t.Fatalf("day 3: %v", err)
	}
	if r3.Skipped[SkipBudgetExhausted] != 1 || r3.Accrued != 0 {
		t.Fatalf("day 3 = %+v, want budget_exhausted and nothing accrued", r3)
	}

	type row struct {
		gross, net, views int64
		skip              string
	}
	read := func(day time.Time) row {
		var r row
		if err := pool.QueryRow(ctx, `
			SELECT gross_paise, net_paise, view_count, COALESCE(skip_reason, '')
			FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`, creator, day).
			Scan(&r.gross, &r.net, &r.views, &r.skip); err != nil {
			t.Fatalf("read %s: %v", day.Format("2006-01-02"), err)
		}
		return r
	}
	d1, d2, d3 := read(day1), read(day2), read(day3)
	if d1.gross != 5_000 || d1.skip != "" {
		t.Errorf("day 1 = %+v, want gross 5000 and no skip reason (nothing earned is reduced)", d1)
	}
	if d2.gross != 3_000 || d2.skip != SkipBudgetCapped {
		t.Errorf("day 2 = %+v, want exactly the remaining 3000, marked budget_capped", d2)
	}
	if d3.gross != 0 || d3.net != 0 || d3.skip != SkipBudgetExhausted || d3.views != 1000 {
		t.Errorf("day 3 = %+v, want a zero row with reason budget_exhausted and the views still recorded", d3)
	}

	var accrued int64
	var exhaustedAt, exhaustedOn *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT accrued_paise, exhausted_at, exhausted_on_day FROM creator_fund_budgets
		WHERE period_key = $1 AND region_code = 'IN'`, period.Key).Scan(&accrued, &exhaustedAt, &exhaustedOn); err != nil {
		t.Fatal(err)
	}
	if accrued != 8_000 {
		t.Errorf("budget accrued_paise = %d, want exactly the cap 8000", accrued)
	}
	if exhaustedAt == nil || exhaustedOn == nil || !exhaustedOn.Equal(day2) {
		t.Errorf("exhausted_at=%v exhausted_on_day=%v, want set on day 2", exhaustedAt, exhaustedOn)
	}

	// The statement carries the cap, the date, and says so in words.
	st, err := svc.SettleCreatorFundPeriod(ctx, creator, period)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if st.BudgetCapPaise == nil || *st.BudgetCapPaise != 8_000 {
		t.Errorf("statement budget cap = %v, want 8000", st.BudgetCapPaise)
	}
	if st.BudgetExhaustedOnDay == nil || !st.BudgetExhaustedOnDay.Equal(day2) {
		t.Errorf("statement exhausted on = %v, want day 2", st.BudgetExhaustedOnDay)
	}
	if st.FundRowsSkipped != 1 {
		t.Errorf("statement fund_rows_skipped = %d, want 1", st.FundRowsSkipped)
	}
	if st.Fund.GrossPaise != 8_000 {
		t.Errorf("statement fund gross = %d, want 8000", st.Fund.GrossPaise)
	}
	if !containsAll(st.Explanation, "capped", "11 June 2025") {
		t.Errorf("explanation does not mention the cap and the day it was reached: %q", st.Explanation)
	}
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatalf("statement does not balance: %v", err)
	}
}

// Nothing earned is reduced, so a cap can never be set below what has
// already accrued against it. Raising it re-opens the period only when the
// accrued amount is below the new cap.
func TestLoweringBudgetBelowAccruedIsRefused(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	period := MonthPeriod(2025, time.October)
	day := time.Date(2025, 10, 8, 0, 0, 0, 0, time.UTC)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, day, false)
	cleanupBudget(ctx, t, pool, period.Key)

	creator, content := uuid.New(), uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, content, creator, day, "long_video", 1000, 4000, 0.4)

	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, CapPaise: 5_000}, nil); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if _, err := svc.AccrueCreatorFundDay(ctx, creator, day); err != nil {
		t.Fatal(err)
	}
	b, err := store.GetCreatorFundBudget(ctx, period.Key, "IN")
	if err != nil || b == nil {
		t.Fatalf("budget: %v %v", b, err)
	}
	if b.AccruedPaise != 5_000 || b.ExhaustedAt == nil {
		t.Fatalf("after one full day the cap should be reached: %+v", b)
	}

	// Below accrued: refused, and nothing changes.
	_, err = svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, CapPaise: 4_000}, nil)
	if !errors.Is(err, ErrBudgetBelowAccrued) {
		t.Fatalf("lowering below accrued: err = %v, want ErrBudgetBelowAccrued", err)
	}
	b, _ = store.GetCreatorFundBudget(ctx, period.Key, "IN")
	if b.CapPaise != 5_000 || b.ExhaustedAt == nil {
		t.Fatalf("refused change still altered the row: %+v", b)
	}
	// Equal to accrued: allowed, still exhausted.
	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, CapPaise: 5_000}, nil); err != nil {
		t.Fatalf("setting cap equal to accrued: %v", err)
	}
	b, _ = store.GetCreatorFundBudget(ctx, period.Key, "IN")
	if b.ExhaustedAt == nil {
		t.Fatalf("cap == accrued should stay exhausted: %+v", b)
	}
	// Raised above accrued: re-opened.
	raised, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, CapPaise: 9_000}, nil)
	if err != nil {
		t.Fatalf("raising: %v", err)
	}
	if raised.CapPaise != 9_000 || raised.ExhaustedAt != nil || raised.ExhaustedOnDay != nil || raised.AccruedPaise != 5_000 {
		t.Fatalf("raised budget = %+v, want cap 9000, exhaustion cleared, accrued untouched", raised)
	}
	// A key of the wrong cadence is refused: a mid-period cadence change
	// would rename the period every accrual is counted against.
	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: "2025-10-H1", CapPaise: 1_000}, nil); !errors.Is(err, ErrCadenceMismatch) {
		t.Errorf("semimonthly key under monthly cadence: err = %v, want ErrCadenceMismatch", err)
	}
	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: "garbage", CapPaise: 1_000}, nil); err == nil {
		t.Error("an unparseable period key was accepted")
	}
	// And the cadence itself cannot change while this period holds money.
	semi := New(store, nil)
	cfg := semi.CreatorFundConfigSnapshot()
	cfg.SettlementCadence = CadenceSemiMonthly
	semi.WithCreatorFundConfig(cfg)
	if err := semi.CheckSettlementCadence(ctx); !errors.Is(err, ErrCadenceMismatch) {
		t.Errorf("cadence change with accrued budgets: err = %v, want ErrCadenceMismatch", err)
	}
	if err := svc.CheckSettlementCadence(ctx); err != nil {
		t.Errorf("configured cadence should pass: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 2B — every skip has a name
// ---------------------------------------------------------------------------

// The old loop `continue`d silently. Every reason a (creator, day, type)
// did not accrue is now counted and returned, so an operator can tell
// "no views" from "no rate" from "already done" without reading the table.
func TestAccrualRecordsSkipReasons(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	day := time.Date(2025, 9, 9, 0, 0, 0, 0, time.UTC)
	// A flick rate only. long_video has no rate in this window, which is
	// the no_rate case; the launch baseline rows start in 2026.
	seedRateAndBandFor(ctx, t, pool, "flick", 300, day, false)

	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "flick", 5000, 20000, 0.4)    // accrues
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "long_video", 500, 2000, 0.4) // no_rate
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "post", 10, 100, 0.4)         // unsupported_type:post
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "image", 0, 100, 0.4)         // zero_views

	first, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil {
		t.Fatalf("first accrual: %v", err)
	}
	if first.Accrued != 1 {
		t.Errorf("accrued = %d, want 1 (the flick day)", first.Accrued)
	}
	want := map[string]int{
		SkipNoRate:                   1,
		SkipUnsupportedType + "post": 1,
		SkipZeroViews:                1,
	}
	for reason, n := range want {
		if first.Skipped[reason] != n {
			t.Errorf("first run skipped[%q] = %d, want %d (all: %v)", reason, first.Skipped[reason], n, first.Skipped)
		}
	}
	if len(first.Skipped) != len(want) {
		t.Errorf("first run recorded extra reasons: %v", first.Skipped)
	}

	second, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil {
		t.Fatalf("second accrual: %v", err)
	}
	if second.Accrued != 0 || second.Skipped[SkipAlreadyAccrued] != 1 {
		t.Errorf("second run = %+v, want nothing accrued and already_accrued=1", second)
	}

	// The versioned row: rate, band, rule version and an input revision.
	var rateID, bandID *uuid.UUID
	var ruleVersion, revision string
	if err := pool.QueryRow(ctx, `
		SELECT rate_id, band_id, rule_version, COALESCE(input_revision, '')
		FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2 AND content_type = 'flick'`,
		creator, day).Scan(&rateID, &bandID, &ruleVersion, &revision); err != nil {
		t.Fatal(err)
	}
	if rateID == nil || bandID == nil || ruleVersion != RuleVersion || len(revision) != 64 {
		t.Errorf("row versioning: rate=%v band=%v rule=%q revision=%q", rateID, bandID, ruleVersion, revision)
	}

	// Inputs that change after the day was accrued are an error, not a
	// silent no-op: that is the signal for a correction adjustment.
	if _, err := pool.Exec(ctx, `
		UPDATE analytics.content_daily_summary SET views_display = views_display + 1, updated_at = NOW()
		WHERE creator_id = $1 AND day_bucket = $2 AND content_type = 'flick'`, creator, day); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AccrueCreatorFundDay(ctx, creator, day); !errors.Is(err, ErrInputRevisionChanged) {
		t.Errorf("changed inputs on an accrued day: err = %v, want ErrInputRevisionChanged", err)
	}
}

// The lock order inside one accrual transaction is budget, then carry,
// then the earnings insert. Two accruals that took them in different
// orders could deadlock, and a budget read outside the lock could be
// overspent; the trace makes the order an assertion instead of a comment.
func TestAccrueDayTxLockOrderIsBudgetThenCarryThenEarnings(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	period := MonthPeriod(2025, time.November)
	day := time.Date(2025, 11, 5, 0, 0, 0, 0, time.UTC)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, day, false)
	cleanupBudget(ctx, t, pool, period.Key)
	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, CapPaise: 1_000_000}, nil); err != nil {
		t.Fatal(err)
	}

	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, uuid.New(), creator, day, "long_video", 100, 400, 0.4)

	var trace []string
	postgres.SetAccrualLockTraceForTest(func(step string) { trace = append(trace, step) })
	t.Cleanup(func() { postgres.SetAccrualLockTraceForTest(nil) })

	if _, err := svc.AccrueCreatorFundDay(ctx, creator, day); err != nil {
		t.Fatal(err)
	}
	want := []string{postgres.LockStepBudget, postgres.LockStepCarry, postgres.LockStepEarnings}
	if len(trace) != len(want) {
		t.Fatalf("lock trace = %v, want %v", trace, want)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("lock trace = %v, want %v", trace, want)
		}
	}
}

// The budget row lock is what makes the cap hold under concurrency. Six
// creators accrue the same day at once against a cap that fits two and a
// bit of them: with the lock, every accrual serialises, exactly the cap is
// spent, and nobody errors. Without FOR UPDATE two accruals read the same
// remaining amount, and the second commit either overspends (which the
// table CHECK turns into an error) or under-records it.
func TestBudgetLockSerialisesConcurrentAccruals(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	period := MonthPeriod(2025, time.December)
	day := time.Date(2025, 12, 3, 0, 0, 0, 0, time.UTC)
	seedRateAndBandFor(ctx, t, pool, "long_video", 5000, day, false)
	cleanupBudget(ctx, t, pool, period.Key)
	const cap = int64(12_000) // two full 5,000 days, one capped at 2,000, the rest zero
	if _, err := svc.UpsertCreatorFundBudget(ctx, BudgetInput{PeriodKey: period.Key, CapPaise: cap}, nil); err != nil {
		t.Fatal(err)
	}

	const n = 6
	creators := make([]uuid.UUID, n)
	for i := range creators {
		creators[i] = uuid.New()
		cleanupCreatorMoney(ctx, t, pool, creators[i])
		makeEligible(ctx, t, pool, creators[i])
		insertDailySummary(ctx, t, pool, uuid.New(), creators[i], day, "long_video", 1000, 4000, 0.4)
	}

	errs := make(chan error, n)
	start := make(chan struct{})
	for _, c := range creators {
		go func(c uuid.UUID) {
			<-start
			_, err := svc.AccrueCreatorFundDay(ctx, c, day)
			errs <- err
		}(c)
	}
	close(start)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent accrual failed: %v", err)
		}
	}

	var accrued int64
	if err := pool.QueryRow(ctx, `SELECT accrued_paise FROM creator_fund_budgets WHERE period_key = $1 AND region_code = 'IN'`, period.Key).Scan(&accrued); err != nil {
		t.Fatal(err)
	}
	var sumGross, rows, capped, exhausted int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(gross_paise), 0), COUNT(*),
		       COUNT(*) FILTER (WHERE skip_reason = 'budget_capped'),
		       COUNT(*) FILTER (WHERE skip_reason = 'budget_exhausted')
		FROM creator_fund_earnings WHERE creator_id = ANY($1) AND day_bucket = $2`,
		creators, day).Scan(&sumGross, &rows, &capped, &exhausted); err != nil {
		t.Fatal(err)
	}
	if accrued != cap || sumGross != cap {
		t.Fatalf("budget accrued %d, rows sum %d, want exactly the cap %d", accrued, sumGross, cap)
	}
	if rows != n || capped != 1 || exhausted != 3 {
		t.Fatalf("rows=%d capped=%d exhausted=%d, want 6 rows: two full, one capped, three zero", rows, capped, exhausted)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
