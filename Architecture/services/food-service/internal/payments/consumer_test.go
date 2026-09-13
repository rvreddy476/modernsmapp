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

func (f *fakeApplier) ApplyPaymentEvent(_ context.Context, ev Event) (Applied, error) {
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

func foodPayload() map[string]any {
	return map[string]any{
		"id":             tIntentID,
		"payer_id":       tUserID.String(),
		"payee_id":       uuid.NewString(),
		"reference_type": "food_order",
		"reference_id":   tOrderID.String(),
		"amount_minor":   25000,
		// The deprecated float mirror must never be read.
		"amount":   1.0,
		"currency": "INR",
		"status":   "succeeded",
	}
}

func newTestConsumer(f *fakeApplier) (*Consumer, *[]Applied) {
	var notified []Applied
	return &Consumer{store: f, notify: func(_ context.Context, a Applied) { notified = append(notified, a) }}, &notified
}

func TestHandle_IgnoresOtherEventTypes(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	if err := c.Handle(context.Background(), envelope(t, "payment.status_changed", "e1", foodPayload())); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("store called for an unrelated event")
	}
}

func TestHandle_IgnoresOtherReferenceTypes(t *testing.T) {
	for _, ref := range []string{"order", "", "FOOD_ORDER"} {
		f := &fakeApplier{}
		c, _ := newTestConsumer(f)
		p := foodPayload()
		p["reference_type"] = ref
		if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); err != nil {
			t.Fatalf("ref %q: err = %v", ref, err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("ref %q: a non-food payment reached the food store", ref)
		}
	}
}

func TestHandle_RefusesEmptyEventID(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, " ", foodPayload()))
	if !sharedkafka.IsPermanent(err) {
		t.Fatalf("want permanent refusal, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("applied an event with no dedupe key")
	}
}

func TestHandle_PoisonIsPermanent(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", []byte("{not json"))); !sharedkafka.IsPermanent(err) {
		t.Fatalf("unparseable payload: want permanent, got %v", err)
	}
	p := foodPayload()
	p["reference_id"] = "not-a-uuid"
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); !sharedkafka.IsPermanent(err) {
		t.Fatalf("bad reference id: want permanent, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("poison reached the store")
	}
}

func TestHandle_RoutesParsedEvent(t *testing.T) {
	f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: OutcomeConfirmed, Effect: EffectConfirm}, OrderID: tOrderID}}
	c, notified := newTestConsumer(f)
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "evt-9", foodPayload())); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("store calls = %d", len(f.calls))
	}
	got := f.calls[0]
	want := Event{
		EventID: "evt-9", EventType: events.EventPaymentSucceeded, IntentID: tIntentID,
		OrderID: tOrderID, PayerID: tUserID, AmountMinor: 25000, Currency: "INR", Status: "succeeded",
	}
	if got != want {
		t.Fatalf("event = %+v\nwant   %+v", got, want)
	}
	if len(*notified) != 1 {
		t.Fatalf("an applied effect must notify once, got %d", len(*notified))
	}
}

func TestHandle_RefundUsesIntentIDField(t *testing.T) {
	f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: OutcomeRefunded, Effect: EffectRefund}}}
	c, _ := newTestConsumer(f)
	p := map[string]any{
		"intent_id":      tIntentID,
		"reference_type": "food_order",
		"reference_id":   tOrderID.String(),
		"amount_minor":   5000,
		"status":         "partially_refunded",
	}
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentRefunded, "evt-r", p)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 || f.calls[0].IntentID != tIntentID || f.calls[0].AmountMinor != 5000 || f.calls[0].Status != "partially_refunded" {
		t.Fatalf("refund event parsed wrong: %+v", f.calls)
	}
}

func TestHandle_OutcomeClassification(t *testing.T) {
	cases := []struct {
		name      string
		ret       Applied
		err       error
		permanent bool
		isErr     bool
		notify    bool
	}{
		{"duplicate is a quiet no-op", Applied{Decision: Decision{Outcome: OutcomeDuplicate}}, nil, false, false, false},
		{"unknown order goes to the DLQ", Applied{Decision: Decision{Outcome: OutcomeOrderNotFound}}, nil, true, true, false},
		{"amount mismatch is recorded, not retried, not announced", Applied{Decision: Decision{Outcome: OutcomeAmountMismatch}}, nil, false, false, false},
		{"late capture is recorded, not announced", Applied{Decision: Decision{Outcome: OutcomeLateCapture}}, nil, false, false, false},
		{"already paid does not re-announce", Applied{Decision: Decision{Outcome: OutcomeAlreadyPaid}}, nil, false, false, false},
		{"marked failed announces", Applied{Decision: Decision{Outcome: OutcomeMarkedFailed, Effect: EffectMarkFailed}}, nil, false, false, true},
		{"transient store failure is retried", Applied{}, errors.New("connection reset"), false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeApplier{ret: tc.ret, err: tc.err}
			c, notified := newTestConsumer(f)
			err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", foodPayload()))
			if (err != nil) != tc.isErr {
				t.Fatalf("err = %v, want error=%v", err, tc.isErr)
			}
			if sharedkafka.IsPermanent(err) != tc.permanent {
				t.Fatalf("permanent = %v, want %v (err %v)", sharedkafka.IsPermanent(err), tc.permanent, err)
			}
			if (len(*notified) > 0) != tc.notify {
				t.Fatalf("notified = %d, want notify=%v", len(*notified), tc.notify)
			}
		})
	}
}
