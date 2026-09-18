package payments

import (
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func subSnapshot(status string) Snapshot {
	return Snapshot{Kind: TargetSubscription, ID: uuid.New(), Status: status, AmountMinor: 19900, Currency: "INR", PayerID: tCustomerID, IntentID: tIntentID}
}

func subCapture() Event {
	return Event{EventID: "e1", EventType: events.EventPaymentSucceeded, IntentID: tIntentID, ReferenceType: RefTypeMopeduSubscription,
		PayerID: tCustomerID, AmountMinor: 19900, Currency: "INR", Status: "succeeded"}
}

// The subscription checkout table: a capture matching the plan price, the
// currency, the captain and the bound intent activates; nothing else does.
func TestDecide_SubscriptionCapture(t *testing.T) {
	for _, status := range []string{SubscriptionPaymentPending, SubscriptionPaymentConfirming, SubscriptionPaymentFailed} {
		d := Decide(subSnapshot(status), subCapture())
		if d.Outcome != OutcomeSubscriptionActivated || d.Effect != EffectActivateSubscription {
			t.Fatalf("%s: %+v", status, d)
		}
	}
	if d := Decide(subSnapshot(SubscriptionPaymentPaid), subCapture()); d.Outcome != OutcomeAlreadyPaid || d.Effect != EffectNone {
		t.Fatalf("already paid: %+v", d)
	}
}

// Mutation guards: a wrong-amount, wrong-currency, wrong-payer, other-intent
// or unstated capture is a mismatch and never activates; a capture for a
// legacy (no checkout) or free row neither.
func TestDecide_SubscriptionCaptureMismatch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot, *Event)
	}{
		{"wrong amount", func(_ *Snapshot, e *Event) { e.AmountMinor = 100 }},
		{"wrong currency", func(_ *Snapshot, e *Event) { e.Currency = "USD" }},
		{"unstated currency", func(_ *Snapshot, e *Event) { e.Currency = "" }},
		{"another payer", func(_ *Snapshot, e *Event) { e.PayerID = uuid.New() }},
		{"unstated payer", func(_ *Snapshot, e *Event) { e.PayerID = uuid.Nil }},
		{"another intent", func(_ *Snapshot, e *Event) { e.IntentID = "int_other" }},
		{"legacy row without a checkout", func(s *Snapshot, _ *Event) { s.Status = "legacy" }},
		{"free trial row", func(s *Snapshot, e *Event) { s.AmountMinor = 0; e.AmountMinor = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, e := subSnapshot(SubscriptionPaymentConfirming), subCapture()
			tc.mutate(&s, &e)
			d := Decide(s, e)
			if d.Outcome != OutcomeMismatch || d.Effect != EffectNone {
				t.Fatalf("%+v", d)
			}
		})
	}
}

func TestDecide_SubscriptionFailedAndRefunded(t *testing.T) {
	failed := Event{EventID: "e2", EventType: events.EventPaymentFailed, IntentID: tIntentID, ReferenceType: RefTypeMopeduSubscription}
	if d := Decide(subSnapshot(SubscriptionPaymentConfirming), failed); d.Outcome != OutcomeSubscriptionFailed || d.Effect != EffectMarkSubscriptionFailed {
		t.Fatalf("failed while confirming: %+v", d)
	}
	// A failure after the capture never un-pays.
	if d := Decide(subSnapshot(SubscriptionPaymentPaid), failed); d.Outcome != OutcomeFailedIgnored || d.Effect != EffectNone {
		t.Fatalf("failed after paid: %+v", d)
	}
	refunded := Event{EventID: "e3", EventType: events.EventPaymentRefunded, IntentID: tIntentID, ReferenceType: RefTypeMopeduSubscription, AmountMinor: 19900, Status: StatusRefunded}
	if d := Decide(subSnapshot(SubscriptionPaymentPaid), refunded); d.Effect != EffectNone {
		t.Fatalf("subscription refund must not be applied by rule: %+v", d)
	}
}

// Rule (b) in the decision table: a second capture on another intent for a
// paid ride files the duplicate refund; the same intent re-delivered is
// already_paid; a different amount is a mismatch.
func TestDecide_DuplicateCapture(t *testing.T) {
	paid := Snapshot{Kind: TargetRide, ID: uuid.New(), RideID: tRideID, Status: StatusSucceeded, Method: MethodUPI, AmountMinor: 12451, Currency: "INR", PayerID: tCustomerID, IntentID: tIntentID}
	dup := Event{EventID: "e4", EventType: events.EventPaymentSucceeded, IntentID: "int_second", ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 12451, Currency: "INR", Status: "succeeded"}
	if d := Decide(paid, dup); d.Outcome != OutcomeDuplicateCapture || d.Effect != EffectRefundDuplicate {
		t.Fatalf("duplicate: %+v", d)
	}
	same := dup
	same.IntentID = tIntentID
	if d := Decide(paid, same); d.Outcome != OutcomeAlreadyPaid || d.Effect != EffectNone {
		t.Fatalf("same intent: %+v", d)
	}
	wrong := dup
	wrong.AmountMinor = 999
	if d := Decide(paid, wrong); d.Outcome != OutcomeMismatch || d.Effect != EffectNone {
		t.Fatalf("wrong amount on another intent: %+v", d)
	}
	// Not paid yet: an intent that is not the bound one is a mismatch.
	unpaid := paid
	unpaid.Status = StatusConfirming
	if d := Decide(unpaid, dup); d.Outcome != OutcomeMismatch {
		t.Fatalf("unpaid + other intent: %+v", d)
	}
}

// The refund of the duplicate intent settles the rule refund row only.
func TestDecide_DuplicateRefunded(t *testing.T) {
	paid := Snapshot{Kind: TargetRide, ID: uuid.New(), RideID: tRideID, Status: StatusSucceeded, Method: MethodUPI, AmountMinor: 12451, Currency: "INR", PayerID: tCustomerID, IntentID: tIntentID, RuleRefundPending: true}
	ev := Event{EventID: "e5", EventType: events.EventPaymentRefunded, IntentID: "int_second", ReferenceID: tRideID, AmountMinor: 12451, Status: StatusRefunded}
	if d := Decide(paid, ev); d.Outcome != OutcomeDuplicateRefunded || d.Effect != EffectSettleDuplicateRefund {
		t.Fatalf("%+v", d)
	}
	// Without a pending rule refund the other-intent refund is a mismatch
	// (CheckRefund), never applied to the paid row.
	paid.RuleRefundPending = false
	if d := Decide(paid, ev); d.Outcome != OutcomeMismatch {
		t.Fatalf("no pending rule refund: %+v", d)
	}
}

// Rule (c) settlement: the refund of the intent that settled a fee, in
// full, with a rule refund pending, marks the fee refunded.
func TestDecide_OutstandingRefunded(t *testing.T) {
	settled := Snapshot{Kind: TargetOutstanding, ID: uuid.New(), RideID: tRideID, Status: OutstandingSettled, AmountMinor: 1500, Currency: "INR", PayerID: tCustomerID, IntentID: tIntentID, RuleRefundPending: true}
	ev := Event{EventID: "e6", EventType: events.EventPaymentRefunded, IntentID: tIntentID, ReferenceID: settled.ID, AmountMinor: 1500, Status: StatusRefunded}
	if d := Decide(settled, ev); d.Outcome != OutcomeOutstandingRefunded || d.Effect != EffectRefundOutstanding {
		t.Fatalf("%+v", d)
	}
	settled.RuleRefundPending = false
	if d := Decide(settled, ev); d.Effect != EffectNone {
		t.Fatalf("no rule refund pending: %+v", d)
	}
	partial := ev
	partial.AmountMinor = 700
	settled.RuleRefundPending = true
	if d := Decide(settled, partial); d.Effect != EffectNone {
		t.Fatalf("partial fee refund: %+v", d)
	}
}
