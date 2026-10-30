//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// Audit 2026-10-30 M3: an order that dies unpaid gives its coupon use back,
// once, in the same transaction as the state change; a payment retry
// re-claims it or is refused.

// couponOrder checks out one line with a one-use-per-buyer coupon.
func couponOrder(t *testing.T, maxUses int) (*fixture, string, *CheckoutResult) {
	t.Helper()
	f := newFixture(t, 10, 100000, "18")
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, perUser: 1, maxUses: ptrI(maxUses)})
	f.addToCart(1, 100000)
	q, p, err := f.quoteCoupon(4000, code, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.checkoutCoupon(q, code, p.TotalMinor, false)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if n := couponUses(t, code); n != 1 {
		t.Fatalf("uses_count after checkout = %d, want 1", n)
	}
	return f, code, res
}

func buyerUses(t *testing.T, code string, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM coupon_usages u JOIN coupons c ON c.id = u.coupon_id WHERE c.code = $1 AND u.user_id = $2`,
		code, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCouponRelease_OnExpiry(t *testing.T) {
	ctx := context.Background()
	f, code, res := couponOrder(t, 1)
	mustExec(t, fmt.Sprintf(
		`UPDATE inventory_reservations SET expires_at = NOW() - INTERVAL '1 minute' WHERE order_id = '%s'`, res.OrderID))
	for i := 0; i < 200; i++ {
		n, err := f.store.ExpireStaleOrders(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if n := couponUses(t, code); n != 0 {
		t.Fatalf("uses_count after expiry = %d, want 0", n)
	}
	if n := buyerUses(t, code, f.userID); n != 0 {
		t.Fatalf("buyer uses after expiry = %d, want 0", n)
	}
	// The buyer can use it again: the one-use coupon is not burned.
	f.addToCart(1, 100000)
	if _, _, err := f.quoteCoupon(4000, code, false); err != nil {
		t.Fatalf("re-quote after expiry: %v", err)
	}
}

func TestCouponRelease_OnCancelBeforePaymentOnly(t *testing.T) {
	ctx := context.Background()
	f, code, res := couponOrder(t, 5)
	if err := f.store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "changed my mind"); err != nil {
		t.Fatal(err)
	}
	if n := couponUses(t, code); n != 0 {
		t.Fatalf("uses_count after an unpaid cancel = %d, want 0", n)
	}
	// A repeat cancel releases nothing more (and the count never goes negative).
	if err := f.store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "again"); err != nil {
		t.Fatal(err)
	}
	if n := couponUses(t, code); n != 0 {
		t.Fatalf("uses_count after a repeated cancel = %d, want 0", n)
	}

	// A PAID order that is cancelled keeps its use.
	f.addToCart(1, 100000)
	q, p, err := f.quoteCoupon(4000, code, false)
	if err != nil {
		t.Fatal(err)
	}
	paid, err := f.checkoutCoupon(q, code, p.TotalMinor, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: paid.OrderID,
		AmountMinor: paid.TotalMinor, Currency: "INR", PayerID: f.userID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CancelOrder(ctx, paid.OrderID, f.userID, "customer", "paid then cancelled"); err != nil {
		t.Fatal(err)
	}
	if n := couponUses(t, code); n != 1 {
		t.Fatalf("uses_count after a paid cancel = %d, want 1 (the use was redeemed)", n)
	}
}

func TestCouponRelease_OnPaymentFailure_RetryReclaims(t *testing.T) {
	ctx := context.Background()
	f, code, res := couponOrder(t, 1)
	failed := PaymentEvent{EventID: "evt-" + uuid.NewString(), EventType: "payment.failed", OrderID: res.OrderID}
	if err := f.store.ApplyPaymentFailed(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if n := couponUses(t, code); n != 0 {
		t.Fatalf("uses_count after payment failure = %d, want 0", n)
	}
	// Redelivery: deduped, nothing released twice.
	if err := f.store.ApplyPaymentFailed(ctx, failed); !errors.Is(err, ErrDuplicatePaymentEvt) {
		t.Fatalf("redelivery: %v", err)
	}
	if n := couponUses(t, code); n != 0 {
		t.Fatalf("uses_count after redelivery = %d, want 0", n)
	}

	// The retry re-claims the use at the checkout price.
	if _, err := f.store.RetryPaymentReservation(ctx, res.OrderID, f.userID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n := couponUses(t, code); n != 1 {
		t.Fatalf("uses_count after retry = %d, want 1", n)
	}
	if n := buyerUses(t, code, f.userID); n != 1 {
		t.Fatalf("buyer uses after retry = %d, want 1", n)
	}
}

func TestCouponRelease_RetryRefusedWhenTheLastUseWasTaken(t *testing.T) {
	ctx := context.Background()
	f, code, res := couponOrder(t, 1)
	if err := f.store.ApplyPaymentFailed(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.failed", OrderID: res.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	// Someone else takes the freed last use.
	mustExec(t, `UPDATE coupons SET uses_count = max_uses WHERE code = $1`, code)

	_, err := f.store.RetryPaymentReservation(ctx, res.OrderID, f.userID)
	if !errors.Is(err, ErrCouponExhausted) {
		t.Fatalf("retry: err = %v, want ErrCouponExhausted", err)
	}
	var status string
	_ = testPool.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, res.OrderID).Scan(&status)
	if status != "payment_failed" {
		t.Fatalf("status after refused retry = %q, want payment_failed", status)
	}
	if _, reserved := f.inventory(); reserved != 0 {
		t.Fatalf("a refused retry held stock: reserved = %d", reserved)
	}
}
