//go:build integration

package service

import (
	"errors"
	"sync"
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
)

// Four withdrawals for ₹100 each, fired at once against a ₹250 balance.
// Exactly two may pass and the other two must be refused for insufficient
// balance: the ledger row is locked for the whole pipeline, so a balance
// read by one request cannot be spent by another before the first has
// moved it to pending. The release-acceptance line "retried requests and
// parallel settlement cannot duplicate money" for the withdrawal side.
//
// What "generous by accident" would look like here: three or four
// payout_requests rows, pending_payout above the balance that existed, or
// a negative balance — each of which is a rupee promised twice.
func TestConcurrentRequestPayoutsCannotOverdraw(t *testing.T) {
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	const (
		balance  = 25_000
		amount   = 10_000
		callers  = 4
		expected = balance / amount // 2
	)
	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(balance))

	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = svc.RequestPayout(ctx, creator, amount, method)
		}(i)
	}
	close(start)
	wg.Wait()

	ok, refused := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInsufficientBalance):
			refused++
		default:
			t.Errorf("caller %d: unexpected error %v", i, err)
		}
	}
	if ok != expected || refused != callers-expected {
		t.Fatalf("succeeded=%d refused=%d, want %d/%d", ok, refused, expected, callers-expected)
	}

	if n := countRows(ctx, t, pool, `SELECT count(*) FROM payout_requests WHERE user_id = $1`, creator); n != expected {
		t.Fatalf("payout_requests rows = %d, want %d", n, expected)
	}
	if n := countRows(ctx, t, pool, `
		SELECT count(*) FROM ledger_entries le
		JOIN accounts c ON c.id = le.credit_account_id
		WHERE c.account_type = 'payout_hold' AND le.idempotency_key LIKE 'payout_hold:%'
		  AND le.debit_account_id IN (SELECT id FROM accounts WHERE owner_id = $1 AND account_type = 'user_wallet')`,
		creator); n != expected {
		t.Fatalf("payout_hold legs = %d, want %d", n, expected)
	}
	gotBalance, pending := ledgerState(ctx, t, pool, creator)
	if gotBalance != balance-expected*amount || pending != expected*amount {
		t.Fatalf("ledger: balance=%d pending=%d, want %d/%d", gotBalance, pending, balance-expected*amount, expected*amount)
	}
	if gotBalance < 0 {
		t.Fatalf("balance went negative: %d", gotBalance)
	}
}
