package payments

import (
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func snap(status string) PurchaseSnapshot {
	return PurchaseSnapshot{PurchaseID: tPurchaseID, UserID: tUserID, Product: ProductPass30d, Status: status,
		AmountMinor: 39900, Currency: "INR", IntentID: tIntentID}
}

func captured() Event {
	return Event{EventID: "e1", EventType: events.EventPaymentSucceeded, IntentID: tIntentID, PurchaseID: tPurchaseID,
		PayerID: tUserID, AmountMinor: 39900, Currency: "INR", Status: "succeeded"}
}

func TestPremiumDecide_CaptureGrantsOnlyAwaitingPurchases(t *testing.T) {
	for _, st := range []string{StatusCreated, StatusConfirming, StatusFailed} {
		if d := Decide(snap(st), captured()); d.Outcome != OutcomeGranted || d.Effect != EffectGrant {
			t.Fatalf("%s: %+v", st, d)
		}
	}
	for _, st := range []string{StatusPaid, StatusRefunded, StatusPartiallyRefunded} {
		if d := Decide(snap(st), captured()); d.Outcome != OutcomeAlreadyPaid || d.Effect != EffectNone {
			t.Fatalf("%s: a second capture must not grant again: %+v", st, d)
		}
	}
}

// Mutation guard: the capture money check.
func TestPremiumDecide_MismatchGrantsNothing(t *testing.T) {
	cases := map[string]func(*Event){
		"amount":            func(e *Event) { e.AmountMinor = 4900 },
		"amount one paisa":  func(e *Event) { e.AmountMinor = 39899 },
		"currency":          func(e *Event) { e.Currency = "USD" },
		"unstated currency": func(e *Event) { e.Currency = "" },
		"payer":             func(e *Event) { e.PayerID = uuid.New() },
		"unstated payer":    func(e *Event) { e.PayerID = uuid.Nil },
		"intent":            func(e *Event) { e.IntentID = uuid.NewString() },
	}
	for name, mutate := range cases {
		ev := captured()
		mutate(&ev)
		d := Decide(snap(StatusConfirming), ev)
		if d.Outcome != OutcomeAmountMismatch || d.Effect != EffectNone || d.Detail == "" {
			t.Fatalf("%s: %+v", name, d)
		}
	}
}

func TestPremiumDecide_Failed(t *testing.T) {
	ev := captured()
	ev.EventType = events.EventPaymentFailed
	for _, st := range []string{StatusCreated, StatusConfirming} {
		if d := Decide(snap(st), ev); d.Effect != EffectMarkFailed {
			t.Fatalf("%s: %+v", st, d)
		}
	}
	for _, st := range []string{StatusPaid, StatusFailed, StatusRefunded} {
		if d := Decide(snap(st), ev); d.Effect != EffectNone {
			t.Fatalf("%s: a failure after capture must change nothing: %+v", st, d)
		}
	}
}

func refund(amount int64, status string) Event {
	return Event{EventID: "r1", EventType: events.EventPaymentRefunded, IntentID: tIntentID, PurchaseID: tPurchaseID,
		AmountMinor: amount, Status: status}
}

func TestPremiumDecide_Refunds(t *testing.T) {
	if d := Decide(snap(StatusPaid), refund(39900, "refunded")); d.Effect != EffectRevoke {
		t.Fatalf("full refund: %+v", d)
	}
	if d := Decide(snap(StatusPaid), refund(10000, "partially_refunded")); d.Effect != EffectPartialRevoke {
		t.Fatalf("partial refund: %+v", d)
	}
	// The last partial refund that completes the amount is a full revoke.
	s := snap(StatusPartiallyRefunded)
	s.RefundedMinor = 29900
	if d := Decide(s, refund(10000, "partially_refunded")); d.Effect != EffectRevoke {
		t.Fatalf("completing partial refund: %+v", d)
	}
	// More than is left is a mismatch.
	if d := Decide(s, refund(10001, "partially_refunded")); d.Outcome != OutcomeAmountMismatch {
		t.Fatalf("over-refund: %+v", d)
	}
	if d := Decide(snap(StatusPaid), refund(0, "partially_refunded")); d.Outcome != OutcomeAmountMismatch {
		t.Fatalf("zero refund: %+v", d)
	}
	other := refund(100, "partially_refunded")
	other.IntentID = uuid.NewString()
	if d := Decide(snap(StatusPaid), other); d.Outcome != OutcomeAmountMismatch {
		t.Fatalf("refund of another intent: %+v", d)
	}
	for _, st := range []string{StatusCreated, StatusConfirming, StatusFailed, StatusRefunded} {
		if d := Decide(snap(st), refund(100, "partially_refunded")); d.Effect != EffectNone {
			t.Fatalf("%s: refund must not apply: %+v", st, d)
		}
	}
}

func TestPremiumProRataRevokeSeconds(t *testing.T) {
	const day = int64(86400)
	cases := []struct {
		granted, amount, refunded, want int64
	}{
		{30 * day, 39900, 0, 0},
		{30 * day, 39900, 39900, 30 * day},
		{30 * day, 39900, 50000, 30 * day},
		{30 * day, 39900, 19950, 15 * day},
		{365 * day, 249900, 1, 365 * day / 249900},
		{0, 4900, 4900, 0},
	}
	for _, c := range cases {
		if got := ProRataRevokeSeconds(c.granted, c.amount, c.refunded); got != c.want {
			t.Fatalf("ProRata(%d,%d,%d) = %d, want %d", c.granted, c.amount, c.refunded, got, c.want)
		}
	}
}
