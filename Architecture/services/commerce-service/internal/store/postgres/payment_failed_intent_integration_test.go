//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A payment.failed against a real database: it fails the order only when it
// is the order's live attempt (decideFailure). Any other failure is
// acknowledged (its inbox row commits) and changes nothing: no status, no
// stock, no coupon, no outbox event.

type failureState struct {
	status, payStatus string
	bound             string
	reserved          int
	failedEvts        int
}

func readFailureState(t *testing.T, f *fixture, orderID uuid.UUID) failureState {
	t.Helper()
	var s failureState
	if err := testPool.QueryRow(context.Background(), `
		SELECT o.status, o.payment_status, COALESCE(o.payment_intent_id::text, ''),
		       (SELECT count(*) FROM outbox_events
		         WHERE partition_key = o.id::text AND event_type = 'commerce.order.payment_failed')
		  FROM orders o WHERE o.id = $1`, orderID).
		Scan(&s.status, &s.payStatus, &s.bound, &s.failedEvts); err != nil {
		t.Fatalf("read failure state: %v", err)
	}
	_, s.reserved = f.inventory()
	return s
}

func failedEvent(orderID uuid.UUID, intent string) PaymentEvent {
	return PaymentEvent{EventID: "evt-" + uuid.NewString(), EventType: "payment.failed", OrderID: orderID, IntentID: intent}
}

func TestPaymentFailed_OnTheBoundIntentFailsTheOrder_RedeliveryIsANoOp(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)

	first := failedEvent(res.OrderID, intent)
	if err := f.store.ApplyPaymentFailed(ctx, first); err != nil {
		t.Fatalf("failure on the bound intent: %v", err)
	}
	s := readFailureState(t, f, res.OrderID)
	if s.status != "payment_failed" || s.payStatus != "failed" || s.reserved != 0 || s.failedEvts != 1 {
		t.Fatalf("after the bound intent failed: %+v; want payment_failed, stock released, one event", s)
	}
	// The same event again is deduped by the inbox.
	if err := f.store.ApplyPaymentFailed(ctx, first); !errors.Is(err, ErrDuplicatePaymentEvt) {
		t.Fatalf("same event again: %v, want ErrDuplicatePaymentEvt", err)
	}
	// The same failure under another event id is acknowledged and changes nothing.
	if err := f.store.ApplyPaymentFailed(ctx, failedEvent(res.OrderID, intent)); err != nil {
		t.Fatalf("redelivery under another event id: %v", err)
	}
	if again := readFailureState(t, f, res.OrderID); again != s {
		t.Fatalf("a redelivered failure changed the order: %+v, want %+v", again, s)
	}
}

func TestPaymentFailed_AnotherAttemptsIntentLeavesTheOrderPayable(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)
	before := readFailureState(t, f, res.OrderID)
	if before.reserved != 1 {
		t.Fatalf("fixture: reserved = %d, want 1", before.reserved)
	}

	other := failedEvent(res.OrderID, uuid.NewString())
	if err := f.store.ApplyPaymentFailed(ctx, other); err != nil {
		t.Fatalf("failure on another intent: %v (it must be acknowledged, never dead-lettered)", err)
	}
	if s := readFailureState(t, f, res.OrderID); s != before {
		t.Fatalf("another attempt's failure changed the order: %+v, want %+v", s, before)
	}
	// Acknowledged: its inbox row committed.
	if err := f.store.ApplyPaymentFailed(ctx, other); !errors.Is(err, ErrDuplicatePaymentEvt) {
		t.Fatalf("the ignored failure was not recorded: %v", err)
	}
	// The bound intent still pays the order.
	if err := f.store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		IntentID: intent, AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}); err != nil {
		t.Fatalf("capture on the bound intent: %v", err)
	}
	if s := readFailureState(t, f, res.OrderID); s.status != "confirmed" || s.payStatus != "paid" {
		t.Fatalf("after the bound intent captured: %+v, want confirmed and paid", s)
	}
}

func TestPaymentFailed_AfterTheCaptureChangesNothing(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)
	if err := f.store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		IntentID: intent, AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	paid := readFailureState(t, f, res.OrderID)
	stock, _ := f.inventory()

	// Out of order on the same intent, then another attempt's failure.
	for _, e := range []PaymentEvent{failedEvent(res.OrderID, intent), failedEvent(res.OrderID, uuid.NewString())} {
		if err := f.store.ApplyPaymentFailed(ctx, e); err != nil {
			t.Fatalf("failure after the capture (intent %s): %v", e.IntentID, err)
		}
	}
	if s := readFailureState(t, f, res.OrderID); s != paid {
		t.Fatalf("a failure after the capture changed the order: %+v, want %+v", s, paid)
	}
	if total, _ := f.inventory(); total != stock {
		t.Fatalf("stock moved: %d, want %d", total, stock)
	}
}

// The retry race: the first attempt failed, the buyer retried (the failed
// intent is unbound and the stock re-held), and a late copy of the first
// attempt's failure arrives before or after the retry's intent is bound.
func TestPaymentFailed_TheEarlierAttemptsFailureNeverFailsTheRetry(t *testing.T) {
	ctx := context.Background()
	f, res, first := pendingOrder(t)
	if err := f.store.ApplyPaymentFailed(ctx, failedEvent(res.OrderID, first)); err != nil {
		t.Fatalf("first attempt failed: %v", err)
	}
	if _, err := f.store.RetryPaymentReservation(ctx, res.OrderID, f.userID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	retried := readFailureState(t, f, res.OrderID)
	if retried.status != "payment_pending" || retried.bound != "" || retried.reserved != 1 {
		t.Fatalf("fixture after retry: %+v, want payment_pending, unbound, re-held", retried)
	}

	// Before the retry's intent is bound.
	if err := f.store.ApplyPaymentFailed(ctx, failedEvent(res.OrderID, first)); err != nil {
		t.Fatalf("late failure, nothing bound: %v", err)
	}
	if s := readFailureState(t, f, res.OrderID); s != retried {
		t.Fatalf("the earlier attempt's failure failed the retry (nothing bound): %+v, want %+v", s, retried)
	}

	// After the retry's intent is bound.
	second := uuid.New()
	if err := f.store.BindPaymentIntent(ctx, res.OrderID, second); err != nil {
		t.Fatal(err)
	}
	bound := readFailureState(t, f, res.OrderID)
	if err := f.store.ApplyPaymentFailed(ctx, failedEvent(res.OrderID, first)); err != nil {
		t.Fatalf("late failure, retry bound: %v", err)
	}
	if s := readFailureState(t, f, res.OrderID); s != bound {
		t.Fatalf("the earlier attempt's failure failed the retry: %+v, want %+v", s, bound)
	}

	// The retry's own failure still fails it.
	if err := f.store.ApplyPaymentFailed(ctx, failedEvent(res.OrderID, second.String())); err != nil {
		t.Fatalf("the retry's own failure: %v", err)
	}
	if s := readFailureState(t, f, res.OrderID); s.status != "payment_failed" || s.reserved != 0 || s.failedEvts != 2 {
		t.Fatalf("after the retry's own failure: %+v, want payment_failed, released, a second event", s)
	}
}
