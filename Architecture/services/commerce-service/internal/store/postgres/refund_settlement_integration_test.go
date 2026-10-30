//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M2 against a real database: a partial refund does not
// close a pending cancel refund, the amount is recorded, the order is fully
// refunded only when the refunds cover it, and redeliveries change nothing.
func TestRefundSettlement_PartialThenRemainder(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 200000, "18")
	f.addToCart(1, 200000)
	res, err := store.Checkout(ctx, f.params(f.quote(0), "idem-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if err := store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if err := store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "changed my mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	read := func() (payStatus string, cmdStatus string, settled int64, refundedEvents, partialEvents int) {
		t.Helper()
		if err := testPool.QueryRow(ctx, `
			SELECT o.payment_status,
			       (SELECT status FROM order_refund_commands WHERE order_id = o.id),
			       (SELECT COALESCE(SUM(amount_minor),0) FROM order_refund_settlements WHERE order_id = o.id),
			       (SELECT count(*) FROM outbox_events WHERE partition_key = o.id::text AND event_type = 'commerce.order.refunded'),
			       (SELECT count(*) FROM outbox_events WHERE partition_key = o.id::text AND event_type = 'commerce.order.partially_refunded')
			  FROM orders o WHERE o.id = $1`, res.OrderID).
			Scan(&payStatus, &cmdStatus, &settled, &refundedEvents, &partialEvents); err != nil {
			t.Fatalf("read: %v", err)
		}
		return
	}

	// A ₹200 dashboard partial while the cancel refund is owed.
	partial := RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, RefundID: "rfnd_" + uuid.NewString(),
		AmountMinor: 20000, ProviderStatus: "partially_refunded",
	}
	if _, err := store.SettleRefund(ctx, partial); err != nil {
		t.Fatalf("partial: %v", err)
	}
	pay, cmd, settled, full, part := read()
	if pay != "refund_pending" || cmd == "succeeded" || settled != 20000 || full != 0 || part != 1 {
		t.Fatalf("after partial: payment=%s command=%s settled=%d refunded_events=%d partial_events=%d", pay, cmd, settled, full, part)
	}

	// Redelivery of the same event: the inbox refuses it.
	if _, err := store.SettleRefund(ctx, partial); !errors.Is(err, ErrDuplicatePaymentEvt) {
		t.Fatalf("redelivery: err = %v, want ErrDuplicatePaymentEvt", err)
	}
	// The same provider refund under a fresh event id: counted once.
	again := partial
	again.EventID = "evt-rf-" + uuid.NewString()
	if r, err := store.SettleRefund(ctx, again); err != nil || !r.Duplicate {
		t.Fatalf("same refund id: r=%+v err=%v, want Duplicate", r, err)
	}
	if _, _, settled, _, part := read(); settled != 20000 || part != 1 {
		t.Fatalf("a duplicate counted: settled=%d partial_events=%d", settled, part)
	}

	// A refund that would exceed what was paid is refused and writes nothing.
	over := RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, RefundID: "rfnd_" + uuid.NewString(),
		AmountMinor: res.TotalMinor,
	}
	if _, err := store.SettleRefund(ctx, over); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("over-refund: err = %v, want ErrAmountMismatch", err)
	}

	// The remainder settles: refunds now cover the order.
	rest := RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, RefundID: "rfnd_" + uuid.NewString(),
		AmountMinor: res.TotalMinor - money.Paise(20000), ProviderStatus: "refunded",
	}
	r, err := store.SettleRefund(ctx, rest)
	if err != nil || !r.Full || r.RefundedTotal != res.TotalMinor {
		t.Fatalf("remainder: r=%+v err=%v", r, err)
	}
	pay, cmd, settled, full, _ = read()
	if pay != "refunded" || cmd != "succeeded" || settled != res.TotalMinor.Int64() || full != 1 {
		t.Fatalf("after remainder: payment=%s command=%s settled=%d refunded_events=%d", pay, cmd, settled, full)
	}
}
