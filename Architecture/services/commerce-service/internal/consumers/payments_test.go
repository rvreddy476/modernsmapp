package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
)

type fakeApplier struct {
	succeeded, failed, refunded []string
	err                         error
}

func (f *fakeApplier) ApplyVerifiedPaymentEvent(_ context.Context, orderID uuid.UUID, paymentID string) error {
	f.succeeded = append(f.succeeded, orderID.String()+":"+paymentID)
	return f.err
}
func (f *fakeApplier) MarkPaymentFailed(_ context.Context, orderID uuid.UUID, paymentID string) error {
	f.failed = append(f.failed, orderID.String()+":"+paymentID)
	return f.err
}
func (f *fakeApplier) ApplyRefundEvent(_ context.Context, intentID string) error {
	f.refunded = append(f.refunded, intentID)
	return f.err
}

// envelope builds what payments-service's outbox relays: an EventEnvelope
// whose Payload is the marshalled PaymentIntent.
func envelope(eventType string, payload map[string]any) *events.EventEnvelope {
	b, _ := json.Marshal(payload)
	return &events.EventEnvelope{EventID: uuid.NewString(), EventType: eventType, Payload: b}
}

func TestPaymentsConsumer_Routing(t *testing.T) {
	orderID, intentID := uuid.New(), uuid.New()
	base := map[string]any{"id": intentID.String(), "reference_type": "order", "reference_id": orderID.String(), "provider_ref": "order_rzp_1", "amount_minor": 90000}

	t.Run("payment.succeeded confirms the order", func(t *testing.T) {
		f := &fakeApplier{}
		c := &PaymentsConsumer{svc: f}
		if err := c.handle(context.Background(), envelope(events.EventPaymentSucceeded, base)); err != nil {
			t.Fatal(err)
		}
		if len(f.succeeded) != 1 || f.succeeded[0] != orderID.String()+":order_rzp_1" {
			t.Fatalf("succeeded = %v", f.succeeded)
		}
	})
	t.Run("payment.failed marks failed", func(t *testing.T) {
		f := &fakeApplier{}
		c := &PaymentsConsumer{svc: f}
		if err := c.handle(context.Background(), envelope(events.EventPaymentFailed, base)); err != nil {
			t.Fatal(err)
		}
		if len(f.failed) != 1 {
			t.Fatalf("failed = %v", f.failed)
		}
	})
	t.Run("payment.refunded keys off the intent id", func(t *testing.T) {
		f := &fakeApplier{}
		c := &PaymentsConsumer{svc: f}
		if err := c.handle(context.Background(), envelope(events.EventPaymentRefunded, base)); err != nil {
			t.Fatal(err)
		}
		if len(f.refunded) != 1 || f.refunded[0] != intentID.String() {
			t.Fatalf("refunded = %v", f.refunded)
		}
	})
	t.Run("non-order references and unrelated events are ignored", func(t *testing.T) {
		f := &fakeApplier{}
		c := &PaymentsConsumer{svc: f}
		sub := map[string]any{"id": intentID.String(), "reference_type": "subscription", "reference_id": uuid.NewString()}
		if err := c.handle(context.Background(), envelope(events.EventPaymentSucceeded, sub)); err != nil {
			t.Fatal(err)
		}
		if err := c.handle(context.Background(), envelope("post.created", base)); err != nil {
			t.Fatal(err)
		}
		if len(f.succeeded)+len(f.failed)+len(f.refunded) != 0 {
			t.Fatalf("unexpected calls: %+v", f)
		}
	})
	t.Run("malformed payload is dropped, not retried", func(t *testing.T) {
		f := &fakeApplier{}
		c := &PaymentsConsumer{svc: f}
		env := &events.EventEnvelope{EventType: events.EventPaymentSucceeded, Payload: []byte(`{not json`)}
		if err := c.handle(context.Background(), env); err != nil {
			t.Fatalf("err = %v, want nil (drop)", err)
		}
	})
}

// TestPaymentsConsumer_ErrorClassification: transient errors retry (plain
// error), "order gone" and "order not payable" are permanent so the
// consumer parks them in the DLQ instead of spinning.
func TestPaymentsConsumer_ErrorClassification(t *testing.T) {
	orderID := uuid.New()
	base := map[string]any{"id": uuid.NewString(), "reference_type": "order", "reference_id": orderID.String(), "provider_ref": "order_rzp_1"}
	for _, tc := range []struct {
		name      string
		err       error
		permanent bool
	}{
		{"transient db error retries", errors.New("dial tcp: connection refused"), false},
		{"order not found is permanent", service.ErrOrderNotFound, true},
		{"order not payable is permanent", service.ErrOrderNotPayable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeApplier{err: tc.err}
			c := &PaymentsConsumer{svc: f}
			err := c.handle(context.Background(), envelope(events.EventPaymentSucceeded, base))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("error chain lost the cause: %v", err)
			}
			if got := sharedkafka.IsPermanent(err); got != tc.permanent {
				t.Fatalf("permanent = %v, want %v (err=%v)", got, tc.permanent, err)
			}
		})
	}
}
