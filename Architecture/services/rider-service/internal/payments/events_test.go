package payments

import (
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func rideSnap(status, method string) Snapshot {
	return Snapshot{Kind: TargetRide, ID: uuid.New(), RideID: tRideID, Status: status, Method: method,
		AmountMinor: 12451, Currency: "INR", PayerID: tCustomerID, IntentID: tIntentID}
}

func capture(amount int64) Event {
	return Event{EventID: "e1", EventType: events.EventPaymentSucceeded, IntentID: tIntentID, ReferenceID: tRideID,
		PayerID: tCustomerID, AmountMinor: amount, Currency: "INR", Status: "succeeded"}
}

func TestDecide_CapturePaysOnlyAwaitingOnlinePayments(t *testing.T) {
	for _, st := range []string{StatusPending, StatusConfirming, StatusFailed} {
		d := Decide(rideSnap(st, MethodUPI), capture(12451))
		if d.Outcome != OutcomePaid || d.Effect != EffectMarkPaid {
			t.Fatalf("%s: %+v", st, d)
		}
	}
	for _, st := range []string{StatusSucceeded, StatusPartiallyRefunded, StatusRefunded} {
		d := Decide(rideSnap(st, MethodCard), capture(12451))
		if d.Outcome != OutcomeAlreadyPaid || d.Effect != EffectNone {
			t.Fatalf("%s: %+v", st, d)
		}
	}
}

// Mutation guard: a capture with a different amount, currency, payer or
// intent, or one that states none, marks nothing paid.
func TestDecide_MismatchPaysNothing(t *testing.T) {
	snap := rideSnap(StatusConfirming, MethodUPI)
	cases := map[string]Event{}
	ev := capture(12450)
	cases["amount"] = ev
	ev = capture(12451)
	ev.Currency = "USD"
	cases["currency"] = ev
	ev = capture(12451)
	ev.Currency = ""
	cases["unstated currency"] = ev
	ev = capture(12451)
	ev.PayerID = uuid.New()
	cases["payer"] = ev
	ev = capture(12451)
	ev.PayerID = uuid.Nil
	cases["unstated payer"] = ev
	ev = capture(12451)
	ev.IntentID = uuid.NewString()
	cases["intent"] = ev
	for name, ev := range cases {
		d := Decide(snap, ev)
		if d.Outcome != OutcomeMismatch || d.Effect != EffectNone {
			t.Fatalf("%s: %+v", name, d)
		}
	}
}

// A capture for a ride whose payment is (now) cash never marks it paid: it
// is reconciliation, whatever the amount says.
func TestDecide_CaptureOnCashPaymentIsMismatch(t *testing.T) {
	for _, st := range []string{StatusPendingCash, StatusPending} {
		d := Decide(rideSnap(st, MethodCash), capture(12451))
		if d.Outcome != OutcomeMismatch || d.Effect != EffectNone {
			t.Fatalf("%s: %+v", st, d)
		}
	}
}

func TestDecide_Failed(t *testing.T) {
	failed := Event{EventID: "e2", EventType: events.EventPaymentFailed, IntentID: tIntentID, ReferenceID: tRideID, Status: "failed"}
	for _, st := range []string{StatusPending, StatusConfirming} {
		if d := Decide(rideSnap(st, MethodUPI), failed); d.Outcome != OutcomeMarkedFailed || d.Effect != EffectMarkFailed {
			t.Fatalf("%s: %+v", st, d)
		}
	}
	for _, st := range []string{StatusSucceeded, StatusFailed, StatusRefunded} {
		if d := Decide(rideSnap(st, MethodUPI), failed); d.Outcome != OutcomeFailedIgnored || d.Effect != EffectNone {
			t.Fatalf("%s: %+v", st, d)
		}
	}
	if d := Decide(rideSnap(StatusPendingCash, MethodCash), failed); d.Effect != EffectNone {
		t.Fatalf("cash: %+v", d)
	}
}

func TestDecide_Refunds(t *testing.T) {
	refund := func(amount int64, status string) Event {
		return Event{EventID: "e3", EventType: events.EventPaymentRefunded, IntentID: tIntentID, ReferenceID: tRideID, AmountMinor: amount, Status: status}
	}
	paid := rideSnap(StatusSucceeded, MethodUPI)
	if d := Decide(paid, refund(12451, "refunded")); d.Outcome != OutcomeRefunded || d.Effect != EffectRefund {
		t.Fatalf("full: %+v", d)
	}
	if d := Decide(paid, refund(5000, "partially_refunded")); d.Outcome != OutcomePartiallyRefunded || d.Effect != EffectPartialRefund {
		t.Fatalf("partial: %+v", d)
	}
	// The second partial that completes the amount is a full refund.
	part := paid
	part.Status, part.RefundedMinor = StatusPartiallyRefunded, 5000
	if d := Decide(part, refund(7451, "partially_refunded")); d.Outcome != OutcomeRefunded || d.Effect != EffectRefund {
		t.Fatalf("completing partial: %+v", d)
	}
	// Over the remaining amount, zero, or another intent: mismatch.
	for name, ev := range map[string]Event{"over": refund(7452, "refunded"), "zero": refund(0, "refunded")} {
		if d := Decide(part, ev); d.Outcome != OutcomeMismatch || d.Effect != EffectNone {
			t.Fatalf("%s: %+v", name, d)
		}
	}
	other := refund(100, "refunded")
	other.IntentID = uuid.NewString()
	if d := Decide(paid, other); d.Outcome != OutcomeMismatch {
		t.Fatalf("other intent: %+v", d)
	}
	// Not refundable states.
	for _, st := range []string{StatusPending, StatusConfirming, StatusFailed, StatusRefunded, StatusPendingCash} {
		if d := Decide(rideSnap(st, MethodUPI), refund(100, "refunded")); d.Outcome != OutcomeRefundIgnored || d.Effect != EffectNone {
			t.Fatalf("%s: %+v", st, d)
		}
	}
}

func TestDecide_Outstanding(t *testing.T) {
	oid := uuid.New()
	snap := Snapshot{Kind: TargetOutstanding, ID: oid, Status: OutstandingPending, AmountMinor: 2000, PayerID: tCustomerID, IntentID: tIntentID}
	ev := capture(2000)
	ev.ReferenceID = oid
	if d := Decide(snap, ev); d.Outcome != OutcomeSettled || d.Effect != EffectSettleOutstanding {
		t.Fatalf("pending: %+v", d)
	}
	settled := snap
	settled.Status = OutstandingSettled
	if d := Decide(settled, ev); d.Outcome != OutcomeAlreadyPaid || d.Effect != EffectNone {
		t.Fatalf("settled: %+v", d)
	}
	waived := snap
	waived.Status = OutstandingWaived
	if d := Decide(waived, ev); d.Outcome != OutcomeMismatch || d.Effect != EffectNone {
		t.Fatalf("waived: %+v", d)
	}
	wrong := ev
	wrong.AmountMinor = 1999
	if d := Decide(snap, wrong); d.Outcome != OutcomeMismatch {
		t.Fatalf("amount: %+v", d)
	}
	failed := ev
	failed.EventType = events.EventPaymentFailed
	if d := Decide(snap, failed); d.Effect != EffectNone {
		t.Fatalf("failed: %+v", d)
	}
}

func TestPublicStatus(t *testing.T) {
	cases := []struct{ method, status, want string }{
		{MethodCash, StatusPendingCash, PublicCashPending},
		{MethodCash, StatusSucceeded, PublicCashConfirmed},
		{MethodUPI, StatusSucceeded, PublicPaid},
		{MethodCard, StatusPending, PublicPending},
		{MethodCard, StatusConfirming, PublicConfirming},
		{MethodUPI, StatusFailed, PublicFailed},
		{MethodUPI, StatusRefunded, PublicRefunded},
		{MethodUPI, StatusPartiallyRefunded, PublicPartiallyRefunded},
	}
	for _, tc := range cases {
		if got := PublicStatus(tc.method, tc.status); got != tc.want {
			t.Fatalf("%s/%s = %s, want %s", tc.method, tc.status, got, tc.want)
		}
	}
}

func TestPublicClientSession(t *testing.T) {
	i := &Intent{ProviderRef: "order_1", ClientSession: map[string]string{"provider": "razorpay", "order_id": "order_1", "key_id": "rzp_pub"}}
	if cs := i.PublicClientSession(); cs == nil || cs.OrderID != "order_1" {
		t.Fatalf("session = %+v", cs)
	}
	i.ClientSession["order_id"] = "order_2"
	if i.PublicClientSession() != nil {
		t.Fatal("an order id that is not the intent's provider ref must be dropped")
	}
	if (&Intent{}).PublicClientSession() != nil {
		t.Fatal("no session")
	}
}
