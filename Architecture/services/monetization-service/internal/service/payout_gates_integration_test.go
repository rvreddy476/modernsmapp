//go:build integration

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// 3A — the withdrawal path runs every gate, in order, in one transaction
// ---------------------------------------------------------------------------
//
// Before this phase RequestPayout moved balance to pending_payout and wrote
// a transactions row, and that was all: no minimum, no KYC, no method
// check, no hold, no TDS, and payout_requests had no writer anywhere in
// the service (M-04). Each test below pins one gate through the real
// service call against the scratch database.

// enabledPayoutService is the service under test with the payouts flag
// open. It is a helper so the same test bodies run before and after the
// flag exists.
func enabledPayoutService(store *postgres.Store) *Service {
	return New(store, nil).WithPayoutsEnabled(true)
}

type payoutFixture struct {
	balancePaise int64
	ledgerAge    time.Duration
	kycVerified  bool
	methodOK     bool
}

// seedPayoutCreator writes the rows a withdrawal reads: a ledger row of
// a given age and balance, a tax profile (verified or not), and one
// payout method. Everything is removed on cleanup, in FK order.
func seedPayoutCreator(ctx context.Context, t *testing.T, pool *pgxpool.Pool, f payoutFixture) (creator, method uuid.UUID) {
	t.Helper()
	creator, method = uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM payout_requests WHERE user_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM tds_ledger WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM fraud_reviews WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE debit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1) OR credit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1)`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE owner_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM payout_methods WHERE user_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_tax_profiles WHERE user_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
	})
	createdAt := time.Now().UTC().Add(-f.ledgerAge)
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_ledger (user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
		VALUES ($1, $2, $2, 0, 'INR', false, $3, $3)`, creator, f.balancePaise, createdAt); err != nil {
		t.Fatal(err)
	}
	var verifiedAt *time.Time
	if f.kycVerified {
		now := time.Now().UTC()
		verifiedAt = &now
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_tax_profiles (user_id, tax_residency, tds_exempt, verified_at)
		VALUES ($1, 'IN', false, $2)`, creator, verifiedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payout_methods (id, user_id, method_type, details_encrypted, is_verified)
		VALUES ($1, $2, 'upi', 'enc:test', $3)`, method, creator, f.methodOK); err != nil {
		t.Fatal(err)
	}
	return creator, method
}

func healthyPayoutFixture(balance int64) payoutFixture {
	return payoutFixture{balancePaise: balance, ledgerAge: 30 * 24 * time.Hour, kycVerified: true, methodOK: true}
}

func countRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func ledgerState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, creator uuid.UUID) (balance, pending int64) {
	t.Helper()
	if err := pool.QueryRow(ctx, `SELECT balance, pending_payout FROM creator_ledger WHERE user_id = $1`, creator).Scan(&balance, &pending); err != nil {
		t.Fatal(err)
	}
	return balance, pending
}

// A withdrawal that passes every gate leaves exactly one payout_requests
// row, one pending payout transaction keyed to it, one ledger leg into
// payout_hold keyed to it, and the balance moved to pending_payout.
func TestRequestPayoutInsertsPayoutRequestRow(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
	if _, err := svc.RequestPayout(ctx, creator, 20_000, method); err != nil {
		t.Fatalf("RequestPayout: %v", err)
	}

	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1`, creator); n != 1 {
		t.Fatalf("payout_requests rows = %d, want exactly one", n)
	}
	var (
		reqID    uuid.UUID
		status   string
		amount   int64
		tds      int64
		net      *int64
		methodID *uuid.UUID
		txnID    *uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT id, status, amount, tds_paise, net_paise, payout_method_id, transaction_id
		FROM payout_requests WHERE user_id = $1`, creator).
		Scan(&reqID, &status, &amount, &tds, &net, &methodID, &txnID); err != nil {
		t.Fatal(err)
	}
	if status != "requested" || amount != 20_000 || tds != 0 || net == nil || *net != 20_000 {
		t.Fatalf("payout_requests row: status=%s amount=%d tds=%d net=%v", status, amount, tds, net)
	}
	if methodID == nil || *methodID != method {
		t.Fatalf("payout_method_id = %v, want %s", methodID, method)
	}
	if txnID == nil {
		t.Fatal("payout_requests.transaction_id is NULL on a requested payout")
	}

	var txType, txStatus, txKey string
	var txAmount int64
	if err := pool.QueryRow(ctx, `
		SELECT type, status, amount, COALESCE(idempotency_key, '') FROM transactions WHERE id = $1`, *txnID).
		Scan(&txType, &txStatus, &txAmount, &txKey); err != nil {
		t.Fatal(err)
	}
	if txType != "payout" || txStatus != "pending" || txAmount != 20_000 || txKey != "payout:"+reqID.String() {
		t.Fatalf("transaction: type=%s status=%s amount=%d key=%q", txType, txStatus, txAmount, txKey)
	}

	if n := countRows(ctx, t, pool, `
		SELECT count(*) FROM ledger_entries le
		JOIN accounts d ON d.id = le.debit_account_id
		JOIN accounts c ON c.id = le.credit_account_id
		WHERE le.idempotency_key = $1 AND d.owner_id = $2 AND d.account_type = 'user_wallet'
		  AND c.account_type = 'payout_hold' AND le.amount_paise = 20000`,
		"payout_hold:"+reqID.String(), creator); n != 1 {
		t.Fatalf("payout_hold ledger legs = %d, want one user_wallet -> payout_hold leg keyed to the request", n)
	}

	balance, pending := ledgerState(ctx, t, pool, creator)
	if balance != 30_000 || pending != 20_000 {
		t.Fatalf("ledger after payout: balance=%d pending=%d, want 30000/20000", balance, pending)
	}
}

// An unverified tax profile refuses the withdrawal before any money moves.
func TestRequestPayoutRefusesUnverifiedKYC(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	f := healthyPayoutFixture(50_000)
	f.kycVerified = false
	creator, method := seedPayoutCreator(ctx, t, pool, f)

	_, err := svc.RequestPayout(ctx, creator, 20_000, method)
	if err == nil {
		t.Fatal("RequestPayout succeeded with an unverified tax profile")
	}
	if !strings.Contains(err.Error(), "KYC_NOT_VERIFIED") {
		t.Fatalf("error = %v, want KYC_NOT_VERIFIED", err)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM transactions WHERE wallet_id = $1`, creator); n != 0 {
		t.Fatalf("refused payout wrote %d transactions", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1`, creator); n != 0 {
		t.Fatalf("refused payout wrote %d payout_requests", n)
	}
	if balance, pending := ledgerState(ctx, t, pool, creator); balance != 50_000 || pending != 0 {
		t.Fatalf("refused payout moved money: balance=%d pending=%d", balance, pending)
	}
}

// A ledger younger than seven days is held: a payout_requests row in
// status 'held', a fraud_reviews row of type new_creator_hold, and no
// money moved. Both rows are the first ever written by this service.
func TestRequestPayoutHoldsNewAccount(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	f := healthyPayoutFixture(50_000)
	f.ledgerAge = 2 * 24 * time.Hour
	creator, method := seedPayoutCreator(ctx, t, pool, f)

	if _, err := svc.RequestPayout(ctx, creator, 20_000, method); err != nil {
		t.Fatalf("a hold is an outcome, not an error: %v", err)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1 AND status = 'held'`, creator); n != 1 {
		t.Fatalf("held payout_requests rows = %d, want one", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM fraud_reviews WHERE creator_id = $1 AND review_type = 'new_creator_hold' AND status = 'pending'`, creator); n != 1 {
		t.Fatalf("new_creator_hold fraud_reviews rows = %d, want one", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM transactions WHERE wallet_id = $1`, creator); n != 0 {
		t.Fatalf("held payout wrote %d transactions", n)
	}
	if balance, pending := ledgerState(ctx, t, pool, creator); balance != 50_000 || pending != 0 {
		t.Fatalf("held payout moved money: balance=%d pending=%d", balance, pending)
	}
}

// More than three requests in 24 hours: the fourth is held with a
// velocity review, and the three before it are unaffected.
func TestRequestPayoutVelocityHold(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(100_000))
	for i := 0; i < 3; i++ {
		if _, err := svc.RequestPayout(ctx, creator, 10_000, method); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1 AND status = 'requested'`, creator); n != 3 {
		t.Fatalf("requested rows after three withdrawals = %d, want 3", n)
	}
	if _, err := svc.RequestPayout(ctx, creator, 10_000, method); err != nil {
		t.Fatalf("fourth request: %v", err)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1 AND status = 'held'`, creator); n != 1 {
		t.Fatalf("held rows after the fourth withdrawal = %d, want 1", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM fraud_reviews WHERE creator_id = $1 AND review_type = 'velocity'`, creator); n != 1 {
		t.Fatalf("velocity fraud_reviews rows = %d, want 1", n)
	}
	if balance, pending := ledgerState(ctx, t, pool, creator); balance != 70_000 || pending != 30_000 {
		t.Fatalf("ledger after 3 paid + 1 held: balance=%d pending=%d, want 70000/30000", balance, pending)
	}
}

// The other refusals, each before any money moves.
func TestRequestPayoutRefusals(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	t.Run("below minimum", func(t *testing.T) {
		creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
		_, err := svc.RequestPayout(ctx, creator, 9_999, method)
		if err == nil || !strings.Contains(err.Error(), "MINIMUM_PAYOUT_NOT_MET") {
			t.Fatalf("error = %v, want MINIMUM_PAYOUT_NOT_MET", err)
		}
	})
	t.Run("unverified method", func(t *testing.T) {
		f := healthyPayoutFixture(50_000)
		f.methodOK = false
		creator, method := seedPayoutCreator(ctx, t, pool, f)
		_, err := svc.RequestPayout(ctx, creator, 20_000, method)
		if err == nil || !strings.Contains(err.Error(), "PAYOUT_METHOD_NOT_VERIFIED") {
			t.Fatalf("error = %v, want PAYOUT_METHOD_NOT_VERIFIED", err)
		}
	})
	t.Run("someone else's method", func(t *testing.T) {
		creator, _ := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
		_, other := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
		_, err := svc.RequestPayout(ctx, creator, 20_000, other)
		if err == nil || !strings.Contains(err.Error(), "PAYOUT_METHOD_NOT_FOUND") {
			t.Fatalf("error = %v, want PAYOUT_METHOD_NOT_FOUND", err)
		}
		if balance, pending := ledgerState(ctx, t, pool, creator); balance != 50_000 || pending != 0 {
			t.Fatalf("refused payout moved money: balance=%d pending=%d", balance, pending)
		}
	})
	t.Run("insufficient balance", func(t *testing.T) {
		creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(15_000))
		_, err := svc.RequestPayout(ctx, creator, 20_000, method)
		if err == nil || !strings.Contains(err.Error(), "INSUFFICIENT_BALANCE") {
			t.Fatalf("error = %v, want INSUFFICIENT_BALANCE", err)
		}
	})
	t.Run("frozen ledger", func(t *testing.T) {
		creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
		if _, err := pool.Exec(ctx, `UPDATE creator_ledger SET is_frozen = true WHERE user_id = $1`, creator); err != nil {
			t.Fatal(err)
		}
		_, err := svc.RequestPayout(ctx, creator, 20_000, method)
		if err == nil || !strings.Contains(err.Error(), "WALLET_FROZEN") {
			t.Fatalf("error = %v, want WALLET_FROZEN", err)
		}
	})
}

// The TDS threshold is on cumulative GROSS for the financial year. The
// old code summed the tds_amount_paise column, which is zero until the
// first deduction — so the threshold could never be crossed by earnings
// alone.
func TestTDSThresholdSumsGross(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	creator, _ := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(0))
	// Rs 30,000 already paid out this year with no TDS taken: the next
	// rupee crosses the threshold.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tds_ledger (creator_id, financial_year, gross_amount_paise, tds_amount_paise, section)
		VALUES ($1, $2, 3000000, 0, '194-O')`, creator, GetFinancialYear()); err != nil {
		t.Fatal(err)
	}
	net, tds, err := svc.DeductTDS(ctx, creator, 100_000)
	if err != nil {
		t.Fatalf("DeductTDS: %v", err)
	}
	if tds != 10_000 || net != 90_000 {
		t.Fatalf("DeductTDS over the gross threshold: net=%d tds=%d, want 90000/10000", net, tds)
	}
}

// A withdrawal is refused outright while payouts are off, before any gate
// or any write. This is the beta boundary at the service layer, so it
// holds for every caller, not just the HTTP route.
func TestPayoutsDisabledRefusesWithdrawal(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := New(store, nil) // the default: payouts off

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
	_, err := svc.RequestPayout(ctx, creator, 20_000, method)
	if err == nil {
		t.Fatal("RequestPayout succeeded with payouts disabled")
	}
	if !strings.Contains(err.Error(), "PAYOUTS_NOT_ENABLED") {
		t.Fatalf("error = %v, want PAYOUTS_NOT_ENABLED", err)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM transactions WHERE wallet_id = $1`, creator); n != 0 {
		t.Fatalf("disabled payout wrote %d transactions", n)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1`, creator); n != 0 {
		t.Fatalf("disabled payout wrote %d payout_requests", n)
	}
	if balance, pending := ledgerState(ctx, t, pool, creator); balance != 50_000 || pending != 0 {
		t.Fatalf("disabled payout moved money: balance=%d pending=%d", balance, pending)
	}
}

// A client idempotency key makes a retried request land once: the second
// call returns the first request and moves no more money.
func TestRequestPayoutIdempotencyKeyReplays(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
	key := "client-" + uuid.NewString()
	first, err := svc.RequestPayoutWithKey(ctx, creator, 20_000, method, key)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.RequestPayoutWithKey(ctx, creator, 20_000, method, key)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || second.Request.ID != first.Request.ID {
		t.Fatalf("replay = %+v, want the first request %s back", second, first.Request.ID)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1`, creator); n != 1 {
		t.Fatalf("payout_requests rows after replay = %d, want 1", n)
	}
	if balance, pending := ledgerState(ctx, t, pool, creator); balance != 30_000 || pending != 20_000 {
		t.Fatalf("ledger after replay: balance=%d pending=%d, want 30000/20000 (moved once)", balance, pending)
	}
	// A different key is a different request.
	if _, err := svc.RequestPayoutWithKey(ctx, creator, 10_000, method, key+"-2"); err != nil {
		t.Fatalf("second key: %v", err)
	}
	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1`, creator); n != 2 {
		t.Fatalf("payout_requests rows after a second key = %d, want 2", n)
	}
}
