//go:build integration

package postgres

import (
	"context"
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
