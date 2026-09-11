//go:build integration

package service

import (
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
)

// Founder decision, 12 September 2026: "leave that tax part; just
// transfer what the amount is; keep that calculation ready; we'll deduct
// later per the user's tax eligibility via government APIs, as the last
// module."
//
// So, with MONETIZATION_TDS_APPLY=false (the default), a withdrawal that
// crosses the yearly threshold still COMPUTES its TDS and still WRITES
// the tds_ledger row carrying the gross and the computed amount — the
// yearly gross keeps accumulating and the number is on record — but the
// payout request carries tds_paise 0 and net_paise = gross: the creator
// is paid the full amount.
func TestTDSNotAppliedWhenFlagOff(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil).WithPayoutsEnabled(true) // TDS apply: the default, off

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(200_000))
	// Rs 30,000 already paid out this financial year: the next rupee
	// crosses the threshold, so the computed TDS on Rs 1,000 is Rs 100.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tds_ledger (creator_id, financial_year, gross_amount_paise, tds_amount_paise, section)
		VALUES ($1, $2, 3000000, 0, '194-O')`, creator, GetFinancialYear()); err != nil {
		t.Fatal(err)
	}

	out, err := svc.RequestPayout(ctx, creator, 100_000, method)
	if err != nil {
		t.Fatalf("RequestPayout: %v", err)
	}
	if out.Held {
		t.Fatalf("payout held: %s", out.HoldReason)
	}

	// The record: one new tds_ledger row for this request, gross 100,000,
	// computed TDS 10,000.
	var ledgerGross, ledgerTDS int64
	if err := pool.QueryRow(ctx, `
		SELECT gross_amount_paise, tds_amount_paise FROM tds_ledger
		WHERE creator_id = $1 AND reference_id = $2`, creator, out.Request.ID).
		Scan(&ledgerGross, &ledgerTDS); err != nil {
		t.Fatalf("tds_ledger row for the request: %v", err)
	}
	if ledgerGross != 100_000 || ledgerTDS != 10_000 {
		t.Fatalf("tds_ledger row: gross=%d tds=%d, want 100000 / 10000 (the calculation is kept on record)", ledgerGross, ledgerTDS)
	}

	// The payment: nothing deducted, net equals gross.
	var reqTDS int64
	var reqNet *int64
	if err := pool.QueryRow(ctx, `SELECT tds_paise, net_paise FROM payout_requests WHERE id = $1`, out.Request.ID).
		Scan(&reqTDS, &reqNet); err != nil {
		t.Fatal(err)
	}
	if reqTDS != 0 || reqNet == nil || *reqNet != 100_000 {
		t.Fatalf("payout_requests: tds_paise=%d net_paise=%d, want 0 / 100000 (TDS is not applied at payout until the tax module)", reqTDS, derefInt64(reqNet))
	}
	if out.TDSPaise != 0 || out.NetPaise != 100_000 || out.GrossPaise != 100_000 {
		t.Fatalf("outcome: gross=%d tds=%d net=%d, want 100000 / 0 / 100000", out.GrossPaise, out.TDSPaise, out.NetPaise)
	}
	// The yearly gross still accumulates: the next payout sees 3,100,000.
	var yearly int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(gross_amount_paise),0) FROM tds_ledger WHERE creator_id = $1`, creator).Scan(&yearly); err != nil {
		t.Fatal(err)
	}
	if yearly != 3_100_000 {
		t.Fatalf("yearly gross = %d, want 3100000", yearly)
	}
}

// With the flag ON the pipeline deducts: the same request carries
// tds_paise 10,000 and net 90,000, and the ledger row matches.
func TestTDSAppliedWhenFlagOn(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil).WithPayoutsEnabled(true).WithTDSApply(true)

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(200_000))
	if _, err := pool.Exec(ctx, `
		INSERT INTO tds_ledger (creator_id, financial_year, gross_amount_paise, tds_amount_paise, section)
		VALUES ($1, $2, 3000000, 0, '194-O')`, creator, GetFinancialYear()); err != nil {
		t.Fatal(err)
	}
	out, err := svc.RequestPayout(ctx, creator, 100_000, method)
	if err != nil {
		t.Fatalf("RequestPayout: %v", err)
	}
	if out.TDSPaise != 10_000 || out.NetPaise != 90_000 {
		t.Fatalf("outcome with TDS applied: tds=%d net=%d, want 10000 / 90000", out.TDSPaise, out.NetPaise)
	}
	var reqTDS int64
	if err := pool.QueryRow(ctx, `SELECT tds_paise FROM payout_requests WHERE id = $1`, out.Request.ID).Scan(&reqTDS); err != nil {
		t.Fatal(err)
	}
	if reqTDS != 10_000 {
		t.Fatalf("payout_requests.tds_paise = %d, want 10000", reqTDS)
	}
}

// A failed payout's counter-entry mirrors the PRICED ledger row, not the
// request's tds_paise: with the flag off the priced row recorded the
// computed 10,000 while nothing was withheld, and the record for that
// request must net to zero (gross uncounted, computed amount cancelled).
func TestTDSCounterEntryMirrorsPricedRowWhenFlagOff(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil).WithPayoutsEnabled(true)

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(200_000))
	if _, err := pool.Exec(ctx, `
		INSERT INTO tds_ledger (creator_id, financial_year, gross_amount_paise, tds_amount_paise, section)
		VALUES ($1, $2, 3000000, 0, '194-O')`, creator, GetFinancialYear()); err != nil {
		t.Fatal(err)
	}
	out, err := svc.RequestPayout(ctx, creator, 100_000, method)
	if err != nil {
		t.Fatalf("RequestPayout: %v", err)
	}
	// The funds-return step every failed/reversed-in-flight convergence
	// ends with (returnFundsTx): transaction failed, gross back to the
	// balance, the payout_hold leg returned, the TDS counter-entry.
	if err := store.WithTx(ctx, func(tx pgxTx) error {
		row, err := store.GetPayoutRequestForUpdateTx(ctx, tx, out.Request.ID)
		if err != nil {
			return err
		}
		return svc.returnFundsTx(ctx, tx, row, "test: provider refused")
	}); err != nil {
		t.Fatalf("return funds: %v", err)
	}
	var grossSum, tdsSum int64
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(gross_amount_paise),0), COALESCE(sum(tds_amount_paise),0)
		FROM tds_ledger WHERE reference_id = $1`, out.Request.ID).Scan(&n, &grossSum, &tdsSum); err != nil {
		t.Fatal(err)
	}
	if n != 2 || grossSum != 0 || tdsSum != 0 {
		t.Fatalf("tds_ledger for a failed request: rows=%d sum(gross)=%d sum(tds)=%d, want 2 / 0 / 0", n, grossSum, tdsSum)
	}
	if balance, pending := ledgerState(ctx, t, pool, creator); balance != 200_000 || pending != 0 {
		t.Fatalf("after failure: balance=%d pending=%d, want the full gross back (200000 / 0)", balance, pending)
	}
}
