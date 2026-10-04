package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

var (
	customer = uuid.MustParse("2d598287-eee7-40b4-a7f5-b46b9412e4e7")
	booking  = uuid.MustParse("5a1d0c3e-0000-4000-8000-000000000001")
)

func pending() Snapshot {
	return Snapshot{BookingStatus: "pending_payment", CustomerID: customer, AmountPaise: 59900, PaymentStatus: "pending",
		IntentID: "intent-1", HoldActive: true}
}

func capture() Event {
	return Event{EventID: "e1", EventType: events.EventPaymentSucceeded, IntentID: "intent-1", BookingID: booking,
		PayerID: customer, AmountMinor: 59900, Currency: "INR"}
}

func TestDecideCapture(t *testing.T) {
	if d := Decide(pending(), capture()); d.Effect != EffectConfirm || d.Outcome != OutcomeConfirmed {
		t.Fatalf("matching capture: %+v", d)
	}
	for name, mut := range map[string]func(*Event){
		"amount":      func(e *Event) { e.AmountMinor = 59800 },
		"currency":    func(e *Event) { e.Currency = "USD" },
		"no currency": func(e *Event) { e.Currency = "" },
		"payer":       func(e *Event) { e.PayerID = uuid.New() },
		"no payer":    func(e *Event) { e.PayerID = uuid.Nil },
		"intent":      func(e *Event) { e.IntentID = "intent-2" },
	} {
		ev := capture()
		mut(&ev)
		if d := Decide(pending(), ev); d.Effect != EffectAttention || d.Outcome != OutcomeMismatch {
			t.Errorf("%s mismatch: %+v", name, d)
		}
	}
	// A mismatch is refused even on an already-paid booking (never "already paid").
	paid := pending()
	paid.PaymentStatus = "succeeded"
	ev := capture()
	ev.AmountMinor = 1
	if d := Decide(paid, ev); d.Outcome != OutcomeMismatch {
		t.Fatalf("mismatch on a paid booking: %+v", d)
	}
	if d := Decide(paid, capture()); d.Effect != EffectNone || d.Outcome != OutcomeAlreadyPaid {
		t.Fatalf("second capture: %+v", d)
	}
	for _, st := range []string{"expired", "cancelled"} {
		s := pending()
		s.BookingStatus = st
		if d := Decide(s, capture()); d.Effect != EffectLateCapture {
			t.Errorf("%s: %+v", st, d)
		}
	}
	noHold := pending()
	noHold.HoldActive = false
	if d := Decide(noHold, capture()); d.Effect != EffectLateCapture {
		t.Fatalf("pending without a hold: %+v", d)
	}
}

func TestDecideFailedAndRefunds(t *testing.T) {
	fail := capture()
	fail.EventType = events.EventPaymentFailed
	if d := Decide(pending(), fail); d.Effect != EffectMarkFailed {
		t.Fatalf("failed: %+v", d)
	}
	paid := pending()
	paid.PaymentStatus = "succeeded"
	if d := Decide(paid, fail); d.Effect != EffectNone || d.Outcome != OutcomeIgnoredAfterCapture {
		t.Fatalf("failed after capture: %+v", d)
	}
	ref := Event{EventID: "r1", EventType: events.EventPaymentRefunded, IntentID: "intent-1", BookingID: booking, AmountMinor: 59900}
	if d := Decide(paid, ref); d.Effect != EffectRefunded {
		t.Fatalf("refunded: %+v", d)
	}
	over := ref
	over.AmountMinor = 60000
	if d := Decide(paid, over); d.Effect != EffectAttention {
		t.Fatalf("over-refund: %+v", d)
	}
	if d := Decide(pending(), ref); d.Effect != EffectNone || d.Outcome != OutcomeRefundIgnored {
		t.Fatalf("refund of an unpaid booking: %+v", d)
	}
	rf := Event{EventID: "f1", EventType: events.EventPaymentRefundFailed, BookingID: booking, Reason: "closed"}
	if d := Decide(paid, rf); d.Effect != EffectRefundFailed {
		t.Fatalf("refund failed: %+v", d)
	}
}

func TestKeys(t *testing.T) {
	if IntentKey(booking) != "doorstep:booking:"+booking.String() {
		t.Fatal(IntentKey(booking))
	}
	if RefundKey(booking, CauseCustomerCancel) != "doorstep:refund:"+booking.String()+":customer_cancel" {
		t.Fatal(RefundKey(booking, CauseCustomerCancel))
	}
}

type recorder struct {
	got    []Event
	extras []Event
	out    Outcome
	err    error
}

func (r *recorder) ApplyExtrasPaymentEvent(_ context.Context, ev Event) (Applied, error) {
	r.extras = append(r.extras, ev)
	return Applied{Decision: Decision{Outcome: OutcomeUnclaimed}}, r.err
}

func (r *recorder) ApplyPaymentEvent(_ context.Context, ev Event) (Applied, error) {
	r.got = append(r.got, ev)
	return Applied{Decision: Decision{Outcome: r.out}, BookingID: ev.BookingID}, r.err
}

func env(id, typ string, payload map[string]any) *events.EventEnvelope {
	raw, _ := json.Marshal(payload)
	return &events.EventEnvelope{EventID: id, EventType: typ, Payload: raw}
}

func succeeded(app, ref string) map[string]any {
	m := map[string]any{"id": "intent-1", "payer_id": customer.String(), "reference_type": ref, "reference_id": booking.String(),
		"amount_minor": 59900, "currency": "INR", "status": "succeeded"}
	if app != "" {
		m["application_id"] = app
	}
	return m
}

func TestConsumerFilters(t *testing.T) {
	r := &recorder{out: OutcomeConfirmed}
	c := NewHandler(r, nil)
	ctx := context.Background()
	for _, e := range []*events.EventEnvelope{
		env("a", events.EventPaymentSucceeded, succeeded("feast", RefBooking)), // another application
		env("b", events.EventPaymentSucceeded, succeeded("", RefBooking)),      // no application stated
		env("d", events.EventPaymentSucceeded, succeeded(ApplicationID, "food_order")),
		env("e", "post.created", map[string]any{}),
	} {
		if err := c.Handle(ctx, e); err != nil {
			t.Fatalf("%s: %v", e.EventID, err)
		}
	}
	if len(r.got) != 0 || len(r.extras) != 0 {
		t.Fatalf("filtered events reached the store: %+v %+v", r.got, r.extras)
	}
	// doorstep_extras goes to the extras path with the bill as reference (B1);
	// a visit-extras bill comes back unclaimed (left for A5), never an error.
	if err := c.Handle(ctx, env("c", events.EventPaymentSucceeded, succeeded(ApplicationID, RefExtras))); err != nil {
		t.Fatal(err)
	}
	if len(r.extras) != 1 || r.extras[0].ExtrasBillID != booking || r.extras[0].BookingID != uuid.Nil || len(r.got) != 0 {
		t.Fatalf("extras routed %+v / %+v", r.extras, r.got)
	}
	if err := c.Handle(ctx, env("f", events.EventPaymentSucceeded, succeeded(ApplicationID, RefBooking))); err != nil {
		t.Fatal(err)
	}
	if len(r.got) != 1 || r.got[0].BookingID != booking || r.got[0].PayerID != customer || r.got[0].AmountMinor != 59900 {
		t.Fatalf("applied %+v", r.got)
	}
	// No event id: permanent (DLQ), never applied without a dedupe key.
	err := c.Handle(ctx, env("", events.EventPaymentSucceeded, succeeded(ApplicationID, RefBooking)))
	if err == nil || !strings.Contains(err.Error(), "event_id") {
		t.Fatalf("no event id: %v", err)
	}
	// Unknown booking: permanent; a store error: retried (not permanent).
	r.out = OutcomeBookingNotFound
	if err := c.Handle(ctx, env("g", events.EventPaymentSucceeded, succeeded(ApplicationID, RefBooking))); err == nil {
		t.Fatal("unknown booking accepted")
	}
	r.out, r.err = OutcomeConfirmed, errors.New("db down")
	err = c.Handle(ctx, env("h", events.EventPaymentSucceeded, succeeded(ApplicationID, RefBooking)))
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("a store error must surface (retried): %v", err)
	}
}
