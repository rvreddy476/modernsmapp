package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
)

type fakeRecorder struct {
	calls []postgres.OrderDuplicateCapture
	err   error
}

func (f *fakeRecorder) RecordOrderDuplicateCapture(_ context.Context, ev postgres.OrderDuplicateCapture) (bool, error) {
	f.calls = append(f.calls, ev)
	return f.err == nil, f.err
}

func duplicateEnvelope(t *testing.T, p events.CommerceOrderDuplicateCapturePayload) *events.EventEnvelope {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return &events.EventEnvelope{EventID: "evt-" + uuid.NewString(), EventType: events.EventCommerceOrderDuplicateCapture, Payload: b}
}

func validPayload() events.CommerceOrderDuplicateCapturePayload {
	return events.CommerceOrderDuplicateCapturePayload{
		OrderID: uuid.NewString(), IntentID: uuid.NewString(), BoundIntentID: uuid.NewString(),
		AmountMinor: 118000, Currency: "INR", RefundDueMinor: 118000,
	}
}

func TestCommerceConsumer_RecordsTheNamedDuplicate(t *testing.T) {
	rec := &fakeRecorder{}
	c := &CommerceConsumer{store: rec}
	p := validPayload()
	env := duplicateEnvelope(t, p)
	if err := c.Handle(context.Background(), env); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("recorder called %d times, want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.EventID != env.EventID || got.OrderID.String() != p.OrderID || got.IntentID.String() != p.IntentID ||
		got.BoundIntentID.String() != p.BoundIntentID || got.AmountMinor != 118000 || got.Currency != "INR" {
		t.Fatalf("recorded %+v from %+v", got, p)
	}
}

func TestCommerceConsumer_IgnoresEveryOtherEvent(t *testing.T) {
	rec := &fakeRecorder{}
	c := &CommerceConsumer{store: rec}
	env := duplicateEnvelope(t, validPayload())
	for _, typ := range []string{events.EventCommerceOrderPaid, events.EventPaymentSucceeded, "commerce.order.late_capture_refunded", ""} {
		env.EventType = typ
		if err := c.Handle(context.Background(), env); err != nil {
			t.Fatalf("%q: %v", typ, err)
		}
	}
	if err := c.Handle(context.Background(), nil); err != nil {
		t.Fatalf("nil envelope: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("recorder called for other events: %+v", rec.calls)
	}
}

// Poison is dead-lettered at once; a database failure is retried, never
// dead-lettered, because the money is still owed back.
func TestCommerceConsumer_ErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mut           func(*events.CommerceOrderDuplicateCapturePayload, *events.EventEnvelope)
		storeErr      error
		wantPermanent bool
		wantCalled    bool
	}{
		{"refused by the ledger", nil, fmt.Errorf("%w: bound intent pending", postgres.ErrOrderDuplicateRefused), true, true},
		{"a database failure", nil, errors.New("connection reset"), false, true},
		{"an order id that is not a uuid",
			func(p *events.CommerceOrderDuplicateCapturePayload, _ *events.EventEnvelope) { p.OrderID = "ORD-1" }, nil, true, false},
		{"no duplicate intent",
			func(p *events.CommerceOrderDuplicateCapturePayload, _ *events.EventEnvelope) { p.IntentID = "" }, nil, true, false},
		{"no bound intent",
			func(p *events.CommerceOrderDuplicateCapturePayload, _ *events.EventEnvelope) { p.BoundIntentID = "" }, nil, true, false},
		{"no event id",
			func(_ *events.CommerceOrderDuplicateCapturePayload, e *events.EventEnvelope) { e.EventID = " " }, nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeRecorder{err: tc.storeErr}
			c := &CommerceConsumer{store: rec}
			p := validPayload()
			env := &events.EventEnvelope{EventID: "evt-1", EventType: events.EventCommerceOrderDuplicateCapture}
			if tc.mut != nil {
				tc.mut(&p, env)
			}
			env.Payload, _ = json.Marshal(p)
			err := c.Handle(context.Background(), env)
			if err == nil {
				t.Fatal("Handle succeeded, want an error")
			}
			if sharedkafka.IsPermanent(err) != tc.wantPermanent {
				t.Fatalf("permanent = %v, want %v (err %v)", sharedkafka.IsPermanent(err), tc.wantPermanent, err)
			}
			if (len(rec.calls) == 1) != tc.wantCalled {
				t.Fatalf("recorder calls = %d, want called %v", len(rec.calls), tc.wantCalled)
			}
		})
	}
	// A payload that is not JSON.
	c := &CommerceConsumer{store: &fakeRecorder{}}
	err := c.Handle(context.Background(), &events.EventEnvelope{EventID: "evt-2",
		EventType: events.EventCommerceOrderDuplicateCapture, Payload: json.RawMessage(`"nope"`)})
	if !sharedkafka.IsPermanent(err) {
		t.Fatalf("unparseable payload: %v, want permanent", err)
	}
}
