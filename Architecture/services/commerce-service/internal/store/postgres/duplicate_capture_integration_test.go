//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// A second payment.succeeded on an order whose own payment already landed,
// against a real database. The same payment again changes nothing; a payment
// on another intent is recorded as owed back without touching the order.

type captureState struct {
	payStatus     string
	commands      int
	lateMarker    bool
	duplicateEvts int
	dueMinor      int64
}

func readCaptureState(t *testing.T, orderID uuid.UUID) captureState {
	t.Helper()
	var s captureState
	if err := testPool.QueryRow(context.Background(), `
		SELECT o.payment_status,
		       (SELECT count(*) FROM order_refund_commands WHERE order_id = o.id),
		       o.late_capture_refund_id IS NOT NULL,
		       (SELECT count(*) FROM outbox_events
		         WHERE partition_key = o.id::text AND event_type = 'commerce.order.duplicate_capture'),
		       COALESCE((SELECT (payload->'payload'->>'refund_due_minor')::bigint FROM outbox_events
		                  WHERE partition_key = o.id::text AND event_type = 'commerce.order.duplicate_capture'
		                  ORDER BY id DESC LIMIT 1), -1)
		  FROM orders o WHERE o.id = $1`, orderID).
		Scan(&s.payStatus, &s.commands, &s.lateMarker, &s.duplicateEvts, &s.dueMinor); err != nil {
		t.Fatalf("read capture state: %v", err)
	}
	return s
}

// partiallyRefundedOrder is a paid order bound to intent, with a ₹200 refund settled.
func partiallyRefundedOrder(t *testing.T) (*fixture, *CheckoutResult, string) {
	t.Helper()
	f, res := paidOrder(t, 10, 2, 100000)
	intent := uuid.NewString()
	mustExec(t, `UPDATE orders SET payment_intent_id = $2 WHERE id = $1`, res.OrderID, intent)
	if _, err := f.store.SettleRefund(context.Background(), RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: intent,
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: money.Paise(20000), ProviderStatus: "partially_refunded",
	}); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if s := readCaptureState(t, res.OrderID); s.payStatus != "partially_refunded" || s.commands != 0 {
		t.Fatalf("fixture: %+v, want partially_refunded with no refund command", s)
	}
	return f, res, intent
}

func TestSecondCaptureOnPartiallyRefundedOrder_SamePaymentIsANoOp(t *testing.T) {
	ctx := context.Background()
	f, res, intent := partiallyRefundedOrder(t)
	stockBefore, _ := f.inventory()

	if err := f.store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		IntentID: intent, AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}); err != nil {
		t.Fatalf("redelivered capture: %v", err)
	}
	s := readCaptureState(t, res.OrderID)
	if s.payStatus != "partially_refunded" || s.commands != 0 || s.lateMarker || s.duplicateEvts != 0 {
		t.Fatalf("after a redelivered capture: %+v; want the order untouched (it used to refund the whole order again)", s)
	}
	if total, _ := f.inventory(); total != stockBefore {
		t.Fatalf("stock moved on a redelivery: %d, want %d", total, stockBefore)
	}
}

func TestSecondCaptureOnPartiallyRefundedOrder_OtherIntentIsOwedBackNotBooked(t *testing.T) {
	ctx := context.Background()
	f, res, _ := partiallyRefundedOrder(t)
	other := uuid.NewString()

	evt := PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		IntentID: other, AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}
	if err := f.store.ApplyPaymentSucceeded(ctx, evt); err != nil {
		t.Fatalf("duplicate capture: %v (it must not be a mismatch left to spin in the DLQ)", err)
	}
	s := readCaptureState(t, res.OrderID)
	if s.payStatus != "partially_refunded" || s.commands != 0 || s.lateMarker {
		t.Fatalf("after a duplicate capture: %+v; the order's own payment state and refund ledger must not change", s)
	}
	if s.duplicateEvts != 1 || s.dueMinor != res.TotalMinor.Int64() {
		t.Fatalf("duplicate capture record: %+v, want one event owing %d", s, res.TotalMinor)
	}

	// The same event again is caught by the inbox.
	if err := f.store.ApplyPaymentSucceeded(ctx, evt); err != ErrDuplicatePaymentEvt {
		t.Fatalf("redelivery of the duplicate: %v, want ErrDuplicatePaymentEvt", err)
	}
	if s := readCaptureState(t, res.OrderID); s.duplicateEvts != 1 {
		t.Fatalf("redelivery wrote a second record: %+v", s)
	}
}

// ─── Late captures and captures on another attempt's intent ──────────

// pendingOrder is a checked-out, unpaid order bound to intent.
func pendingOrder(t *testing.T) (*fixture, *CheckoutResult, string) {
	t.Helper()
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	res, err := f.store.Checkout(context.Background(), f.params(f.quote(0), "idem-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	intent := uuid.NewString()
	mustExec(t, `UPDATE orders SET payment_intent_id = $2 WHERE id = $1`, res.OrderID, intent)
	return f, res, intent
}

// expiredOrder is pendingOrder after its hold lapsed and the sweep expired it.
func expiredOrder(t *testing.T) (*fixture, *CheckoutResult, string) {
	t.Helper()
	f, res, intent := pendingOrder(t)
	if err := f.store.expireOne(context.Background(), res.OrderID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	return f, res, intent
}

func capture(f *fixture, res *CheckoutResult, intent string) PaymentEvent {
	return PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		IntentID: intent, AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}
}

func boundIntent(t *testing.T, orderID uuid.UUID) string {
	t.Helper()
	var id *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT payment_intent_id::text FROM orders WHERE id = $1`, orderID).Scan(&id); err != nil {
		t.Fatalf("read bound intent: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

// A late capture refunded, then announced again under another event id: the
// order stays refunded with one refund command. It used to move back to
// refund_pending.
func TestLateCaptureRepeat_RefundedOrderStaysRefunded(t *testing.T) {
	ctx := context.Background()
	f, res, intent := expiredOrder(t)
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, intent)); err != nil {
		t.Fatalf("late capture: %v", err)
	}
	if s := readCaptureState(t, res.OrderID); s.payStatus != "refund_pending" || s.commands != 1 || !s.lateMarker {
		t.Fatalf("after the late capture: %+v, want refund_pending with one command", s)
	}
	if _, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: intent,
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: res.TotalMinor, ProviderStatus: "refunded",
	}); err != nil {
		t.Fatalf("settle the late-capture refund: %v", err)
	}

	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, intent)); err != nil {
		t.Fatalf("repeat of the late capture: %v", err)
	}
	s := readCaptureState(t, res.OrderID)
	if s.payStatus != "refunded" || s.commands != 1 || s.duplicateEvts != 0 {
		t.Fatalf("after the repeat: %+v, want refunded, one command, no duplicate", s)
	}
}

// A second late capture on another intent is not booked against the order
// (whose refund ledger cannot settle a refund of another intent): it is
// recorded for payments-service to refund. It used to vanish under the
// order-wide key.
func TestSecondLateCaptureOnAnotherIntent_IsADuplicate(t *testing.T) {
	ctx := context.Background()
	f, res, intent := expiredOrder(t)
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, intent)); err != nil {
		t.Fatalf("late capture: %v", err)
	}
	other := uuid.NewString()
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, other)); err != nil {
		t.Fatalf("second late capture: %v", err)
	}
	s := readCaptureState(t, res.OrderID)
	if s.payStatus != "refund_pending" || s.commands != 1 {
		t.Fatalf("after the second late capture: %+v, want the order's own refund only", s)
	}
	if s.duplicateEvts != 1 || s.dueMinor != res.TotalMinor.Int64() {
		t.Fatalf("second late capture: %+v, want one duplicate_capture owing %d", s, res.TotalMinor)
	}
	if got := boundIntent(t, res.OrderID); got != intent {
		t.Fatalf("bound intent = %s, want the first capture's %s", got, intent)
	}
}

// The buyer paid on an earlier attempt's intent before the bound one
// confirmed: that payment applies and becomes the order's; the bound one, if
// it is captured too, is a duplicate. Before, the first went to the mismatch
// dead-letter queue and the second paid the order.
func TestCaptureOnAnotherAttemptsIntent_FirstToLandPaysTheOrder(t *testing.T) {
	ctx := context.Background()
	f, res, bound := pendingOrder(t)
	first := uuid.NewString()
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, first)); err != nil {
		t.Fatalf("capture on another attempt's intent: %v (it must not be dead-lettered)", err)
	}
	if s := readCaptureState(t, res.OrderID); s.payStatus != "paid" || s.duplicateEvts != 0 {
		t.Fatalf("after the first capture: %+v, want paid", s)
	}
	if got := boundIntent(t, res.OrderID); got != first {
		t.Fatalf("bound intent = %s, want the intent that paid (%s)", got, first)
	}

	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, bound)); err != nil {
		t.Fatalf("capture on the formerly bound intent: %v", err)
	}
	s := readCaptureState(t, res.OrderID)
	if s.payStatus != "paid" || s.commands != 0 || s.duplicateEvts != 1 {
		t.Fatalf("after the second capture: %+v, want paid with one duplicate_capture", s)
	}
}

// An intent bound to another order never pays this one.
func TestCaptureOnAnIntentOfAnotherOrder_IsRefused(t *testing.T) {
	ctx := context.Background()
	f, res, _ := pendingOrder(t)
	_, _, otherIntent := pendingOrder(t)
	before := readCaptureState(t, res.OrderID)

	err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, otherIntent))
	if !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("capture on another order's intent: %v, want ErrAmountMismatch", err)
	}
	if s := readCaptureState(t, res.OrderID); s != before {
		t.Fatalf("the order moved on a refused capture: %+v, was %+v", s, before)
	}
}
