package payments

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
)

type fakeApplier struct {
	calls []Event
	ret   Applied
	err   error
}

func (f *fakeApplier) ApplyRidePaymentEvent(_ context.Context, ev Event) (Applied, error) {
	f.calls = append(f.calls, ev)
	return f.ret, f.err
}

func envelope(t *testing.T, eventType, eventID string, payload any) *events.EventEnvelope {
	t.Helper()
	raw, ok := payload.([]byte)
	if !ok {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	return &events.EventEnvelope{EventID: eventID, EventType: eventType, Payload: raw}
}

func mopeduPayload() map[string]any {
	return map[string]any{
		"id":             tIntentID,
		"payer_id":       tCustomerID.String(),
		"payee_id":       DefaultPayeeID.String(),
		"reference_type": "mopedu_ride",
		"reference_id":   tRideID.String(),
		"amount_minor":   12451,
		"amount":         1.0, // the deprecated float mirror is never read
		"currency":       "INR",
		"status":         "succeeded",
		"application_id": "mopedu",
	}
}

func newTestConsumer(f *fakeApplier) (*Consumer, *[]Applied) {
	var notified []Applied
	return NewHandlerForTest(f, func(_ context.Context, a Applied) { notified = append(notified, a) }), &notified
}

func TestHandle_IgnoresOtherEventTypes(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	for _, typ := range []string{"payment.status_changed", events.EventPaymentRefundFailed, "rider.ride.completed"} {
		if err := c.Handle(context.Background(), envelope(t, typ, "e1", mopeduPayload())); err != nil {
			t.Fatalf("%s: err = %v", typ, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("store called for an unrelated event")
	}
}

// Mutation guard: the reference-type filter.
func TestHandle_IgnoresOtherReferenceTypes(t *testing.T) {
	for _, ref := range []string{"order", "food_order", "dating_premium", "", "MOPEDU_RIDE"} {
		f := &fakeApplier{}
		c, _ := newTestConsumer(f)
		p := mopeduPayload()
		p["reference_type"] = ref
		if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); err != nil {
			t.Fatalf("%q: err = %v", ref, err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("%q reached the store", ref)
		}
	}
}

// Mutation guard: another application's event (food, commerce, dating)
// carrying a mopedu_ride reference is dropped by ForApplication.
func TestHandle_IgnoresOtherApplication(t *testing.T) {
	for _, app := range []string{"feast", "commerce", "dating"} {
		f := &fakeApplier{}
		c, _ := newTestConsumer(f)
		p := mopeduPayload()
		p["application_id"] = app
		if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); err != nil {
			t.Fatalf("%s: err = %v", app, err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("application %s reached the store", app)
		}
	}
}

func TestHandle_IgnoresUnstatedApplication(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	p := mopeduPayload()
	delete(p, "application_id")
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("an event stating no application reached the store")
	}
}

func TestHandle_RefusesEmptyEventID(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "", mopeduPayload()))
	if err == nil || !sharedkafka.IsPermanent(err) || !errors.Is(err, errNoEventID) {
		t.Fatalf("err = %v, want a permanent errNoEventID", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("store called without a dedupe key")
	}
}

func TestHandle_PoisonIsPermanent(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", []byte(`{not json`)))
	if err == nil || !sharedkafka.IsPermanent(err) {
		t.Fatalf("malformed payload: err = %v, want permanent", err)
	}
	p := mopeduPayload()
	p["reference_id"] = "not-a-uuid"
	err = c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e2", p))
	if err == nil || !sharedkafka.IsPermanent(err) {
		t.Fatalf("bad reference: err = %v, want permanent", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("poison reached the store")
	}
}

func TestHandle_RoutesParsedEventAndNotifies(t *testing.T) {
	f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: OutcomePaid, Effect: EffectMarkPaid}, Target: TargetRide, RideID: tRideID}}
	c, notified := newTestConsumer(f)
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "evt-1", mopeduPayload())); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %d", len(f.calls))
	}
	got := f.calls[0]
	if got.EventID != "evt-1" || got.EventType != events.EventPaymentSucceeded || got.IntentID != tIntentID ||
		got.ReferenceID != tRideID || got.PayerID != tCustomerID || got.AmountMinor != 12451 || got.Currency != "INR" {
		t.Fatalf("event = %+v", got)
	}
	if len(*notified) != 1 {
		t.Fatalf("notified = %d", len(*notified))
	}
}

func TestHandle_RefundUsesIntentIDField(t *testing.T) {
	f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: OutcomeRefunded, Effect: EffectRefund}}}
	c, _ := newTestConsumer(f)
	p := map[string]any{
		"intent_id": tIntentID, "provider": "razorpay", "provider_refund_id": "rfnd_1", "amount_minor": 5000,
		"status": "partially_refunded", "reference_type": "mopedu_ride", "reference_id": tRideID.String(), "application_id": "mopedu",
	}
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentRefunded, "e9", p)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 || f.calls[0].IntentID != tIntentID || f.calls[0].AmountMinor != 5000 || f.calls[0].Status != "partially_refunded" || f.calls[0].ProviderRef != "rfnd_1" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestHandle_OutcomeClassification(t *testing.T) {
	// Duplicate, unknown target and mismatch are handled (nil): recorded
	// once, never retried, never notified. A store error is retried.
	for _, oc := range []Outcome{OutcomeDuplicate, OutcomeTargetNotFound, OutcomeMismatch} {
		f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: oc}}}
		c, notified := newTestConsumer(f)
		if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", mopeduPayload())); err != nil {
			t.Fatalf("%s: err = %v", oc, err)
		}
		if len(*notified) != 0 {
			t.Fatalf("%s notified", oc)
		}
	}
	f := &fakeApplier{err: errors.New("pg down")}
	c, _ := newTestConsumer(f)
	err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", mopeduPayload()))
	if err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("transient store error: %v, want a retryable error", err)
	}
	_ = uuid.Nil
}
