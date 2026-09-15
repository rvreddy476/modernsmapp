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

var (
	tUserID     = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tPurchaseID = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

const tIntentID = "33333333-3333-4333-8333-333333333333"

type fakeApplier struct {
	calls []Event
	ret   Applied
	err   error
}

func (f *fakeApplier) ApplyPremiumPaymentEvent(_ context.Context, ev Event) (Applied, error) {
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

func datingPayload() map[string]any {
	return map[string]any{
		"id":             tIntentID,
		"payer_id":       tUserID.String(),
		"payee_id":       DefaultPayeeID.String(),
		"reference_type": "dating_premium",
		"reference_id":   tPurchaseID.String(),
		"amount_minor":   39900,
		"amount":         1.0, // the deprecated float mirror is never read
		"currency":       "INR",
		"status":         "succeeded",
		"application_id": "dating",
	}
}

func newTestConsumer(f *fakeApplier) (*Consumer, *[]Applied) {
	var notified []Applied
	return NewHandlerForTest(f, func(_ context.Context, a Applied) { notified = append(notified, a) }), &notified
}

func TestPremiumHandle_IgnoresOtherEventTypes(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	for _, typ := range []string{"payment.status_changed", events.EventPaymentRefundFailed, "dating.premium.subscribed"} {
		if err := c.Handle(context.Background(), envelope(t, typ, "e1", datingPayload())); err != nil {
			t.Fatalf("%s: err = %v", typ, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("store called for an unrelated event")
	}
}

// Mutation guard: the reference-type filter.
func TestPremiumHandle_IgnoresOtherReferenceTypes(t *testing.T) {
	for _, ref := range []string{"order", "food_order", "", "DATING_PREMIUM"} {
		f := &fakeApplier{}
		c, _ := newTestConsumer(f)
		p := datingPayload()
		p["reference_type"] = ref
		if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); err != nil {
			t.Fatalf("ref %q: err = %v", ref, err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("ref %q: a non-dating payment reached the dating store", ref)
		}
	}
}

// Mutation guard: ForApplication("dating"). A dating_premium reference stamped
// for another application must never reach the store.
func TestPremiumHandle_IgnoresOtherApplication(t *testing.T) {
	for _, typ := range []string{events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded} {
		for _, app := range []string{"feast", "mstore", "Dating"} {
			f := &fakeApplier{}
			c, _ := newTestConsumer(f)
			p := datingPayload()
			p["application_id"] = app
			if err := c.Handle(context.Background(), envelope(t, typ, "e-"+app, p)); err != nil {
				t.Fatalf("%s/%s: err = %v", typ, app, err)
			}
			if len(f.calls) != 0 {
				t.Fatalf("%s for application %q reached the dating store", typ, app)
			}
		}
	}
}

func TestPremiumHandle_IgnoresUnstatedApplication(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	p := datingPayload()
	delete(p, "application_id")
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("an event stating no application reached the store")
	}
}

func TestPremiumHandle_RefusesEmptyEventID(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, " ", datingPayload()))
	if !sharedkafka.IsPermanent(err) {
		t.Fatalf("want permanent refusal, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("applied an event with no dedupe key")
	}
}

func TestPremiumHandle_PoisonIsPermanent(t *testing.T) {
	f := &fakeApplier{}
	c, _ := newTestConsumer(f)
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", []byte("{not json"))); !sharedkafka.IsPermanent(err) {
		t.Fatalf("unparseable payload: want permanent, got %v", err)
	}
	p := datingPayload()
	p["reference_id"] = "not-a-uuid"
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", p)); !sharedkafka.IsPermanent(err) {
		t.Fatalf("bad reference id: want permanent, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("poison reached the store")
	}
}

func TestPremiumHandle_RoutesParsedEvent(t *testing.T) {
	f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: OutcomeGranted, Effect: EffectGrant}, PurchaseID: tPurchaseID}}
	c, notified := newTestConsumer(f)
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "evt-9", datingPayload())); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("store calls = %d", len(f.calls))
	}
	want := Event{
		EventID: "evt-9", EventType: events.EventPaymentSucceeded, IntentID: tIntentID,
		PurchaseID: tPurchaseID, PayerID: tUserID, AmountMinor: 39900, Currency: "INR", Status: "succeeded",
	}
	if f.calls[0] != want {
		t.Fatalf("event = %+v\nwant   %+v", f.calls[0], want)
	}
	if len(*notified) != 1 {
		t.Fatalf("an applied effect must notify once, got %d", len(*notified))
	}
}

func TestPremiumHandle_RefundUsesIntentIDField(t *testing.T) {
	f := &fakeApplier{ret: Applied{Decision: Decision{Outcome: OutcomePartiallyRefunded, Effect: EffectPartialRevoke}}}
	c, _ := newTestConsumer(f)
	p := map[string]any{
		"intent_id":      tIntentID,
		"reference_type": "dating_premium",
		"reference_id":   tPurchaseID.String(),
		"amount_minor":   5000,
		"status":         "partially_refunded",
		"application_id": "dating",
	}
	if err := c.Handle(context.Background(), envelope(t, events.EventPaymentRefunded, "evt-r", p)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 || f.calls[0].IntentID != tIntentID || f.calls[0].AmountMinor != 5000 || f.calls[0].Status != "partially_refunded" {
		t.Fatalf("refund event parsed wrong: %+v", f.calls)
	}
}

func TestPremiumHandle_OutcomeClassification(t *testing.T) {
	cases := []struct {
		name      string
		ret       Applied
		err       error
		permanent bool
		isErr     bool
		notify    bool
	}{
		{"duplicate is a quiet no-op", Applied{Decision: Decision{Outcome: OutcomeDuplicate}}, nil, false, false, false},
		{"unknown purchase is recorded, not retried", Applied{Decision: Decision{Outcome: OutcomePurchaseNotFound}}, nil, false, false, false},
		{"amount mismatch is recorded, not retried, not announced", Applied{Decision: Decision{Outcome: OutcomeAmountMismatch}}, nil, false, false, false},
		{"already paid does not re-announce", Applied{Decision: Decision{Outcome: OutcomeAlreadyPaid}}, nil, false, false, false},
		{"marked failed announces", Applied{Decision: Decision{Outcome: OutcomeMarkedFailed, Effect: EffectMarkFailed}}, nil, false, false, true},
		{"transient store failure is retried", Applied{}, errors.New("connection reset"), false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeApplier{ret: tc.ret, err: tc.err}
			c, notified := newTestConsumer(f)
			err := c.Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", datingPayload()))
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
