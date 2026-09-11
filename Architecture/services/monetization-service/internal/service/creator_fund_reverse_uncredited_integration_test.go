//go:build integration

package service

import (
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Reversing a row that was never credited must be a status change and
// nothing else: no adjustment transaction, no ledger leg, no fee leg, the
// wallet untouched. This is the property the January remediation leans on
// for the two migration-017 artefact rows (plan Phase 2A; reviewer memo
// decision 1, completion condition): once their unsupported credited
// flag is cleared, the existing mechanism must take them to a terminal
// 'reversed' state without debiting anything.
func TestReverseUncreditedEarningMovesNoMoney(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil)

	creator := uuid.New()
	cleanupCreatorMoney(ctx, t, pool, creator)
	if _, err := store.EnsureWallet(ctx, creator); err != nil {
		t.Fatal(err)
	}

	// A settled, priced, NOT credited row: what a January artefact row
	// looks like after step 2 of the runbook clears its flag. Gross, fee
	// and net mirror one of the live rows (42,645 / 12,793 / 29,852).
	earning := uuid.New()
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_earnings (
			id, creator_id, day_bucket, content_type, region_code,
			view_count, watch_time_ms, rpm_paise, gross_paise, platform_fee_paise, net_paise,
			status, settled_at, base_gross_paise, quality_multiplier_bps, credited, credited_at, settlement_id)
		VALUES ($1, $2, $3, 'long_video', 'IN',
			8529, 255870000, 5000, 42645, 12793, 29852,
			'settled', NOW(), 42645, 10000, FALSE, NULL, NULL)`,
		earning, creator, day); err != nil {
		t.Fatal(err)
	}

	before := walletBalance(ctx, t, pool, creator)
	res, err := svc.ReverseFundEarning(ctx, earning, "dry run: uncredited row reverses without money")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if res.AlreadyReversed {
		t.Fatal("reported already_reversed on the first call")
	}
	if res.MoneyMoved || res.Adjustment != nil {
		t.Fatalf("an uncredited row moved money: %+v", res)
	}
	if res.NetReversedPaise != 0 || res.FeeReversedPaise != 0 {
		t.Fatalf("net_reversed_paise=%d fee_reversed_paise=%d, want 0 / 0", res.NetReversedPaise, res.FeeReversedPaise)
	}
	if res.LedgerFrozen {
		t.Fatal("ledger was frozen by a reversal that moved nothing")
	}
	if res.BalanceAfter != before {
		t.Fatalf("balance_after_paise=%d, want the untouched balance %d", res.BalanceAfter, before)
	}
	if res.Earning == nil || res.Earning.Status != "reversed" || res.Earning.ReversedAt == nil || res.Earning.ReversalTransactionID != nil {
		t.Fatalf("earning after reversal: %+v", res.Earning)
	}

	// The database agrees with the response.
	var status string
	var reversedAt *time.Time
	var reversalTx *uuid.UUID
	var credited bool
	if err := pool.QueryRow(ctx, `
		SELECT status, reversed_at, reversal_transaction_id, credited
		FROM creator_fund_earnings WHERE id = $1`, earning).Scan(&status, &reversedAt, &reversalTx, &credited); err != nil {
		t.Fatal(err)
	}
	if status != "reversed" || reversedAt == nil || reversalTx != nil || credited {
		t.Fatalf("row: status=%q reversed_at=%v reversal_transaction_id=%v credited=%v", status, reversedAt, reversalTx, credited)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM transactions WHERE wallet_id = $1`, creator); n != 0 {
		t.Fatalf("%d transactions rows for the creator, want 0", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM transactions WHERE idempotency_key = $1`,
		"adj:creator_fund_earning_reversal:"+earning.String()); n != 0 {
		t.Fatalf("%d adjustment transactions keyed on the row, want 0", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM ledger_entries WHERE reference_id = $1 OR idempotency_key LIKE '%' || $2`,
		earning, earning.String()); n != 0 {
		t.Fatalf("%d ledger_entries rows reference the row, want 0 (no net leg, no fee leg)", n)
	}
	if got := walletBalance(ctx, t, pool, creator); got != before {
		t.Fatalf("wallet moved from %d to %d", before, got)
	}
	var frozen bool
	if err := pool.QueryRow(ctx, `SELECT is_frozen FROM creator_ledger WHERE user_id = $1`, creator).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if frozen {
		t.Fatal("creator_ledger.is_frozen was set")
	}

	// Terminal: a second call reports already_reversed and still moves nothing.
	again, err := svc.ReverseFundEarning(ctx, earning, "second attempt")
	if err != nil {
		t.Fatalf("second reversal: %v", err)
	}
	if !again.AlreadyReversed || again.MoneyMoved || again.NetReversedPaise != 0 || again.FeeReversedPaise != 0 {
		t.Fatalf("second reversal: %+v", again)
	}
	t.Logf("OBSERVED uncredited reversal: status=%s money_moved=%v net_reversed_paise=%d fee_reversed_paise=%d balance_after_paise=%d ledger_frozen=%v transactions=0 ledger_entries=0",
		res.Earning.Status, res.MoneyMoved, res.NetReversedPaise, res.FeeReversedPaise, res.BalanceAfter, res.LedgerFrozen)
}
