// Package consumers contains Kafka consumers that drive commerce-service
// state transitions in response to events from sibling services.
package consumers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// PaymentsConsumer listens for payment lifecycle events from payments-service.
// On payment.succeeded for an order reference, it confirms the order
// (mark paid → deduct stock → kick off invoice + shipment fulfillment).
// On payment.failed, it marks the order as payment_failed so the UI can
// prompt the customer to retry, and stock reservations age out naturally.
//
// Payloads are decoded by shared/paymentevents, which reads amount_minor and
// never the deprecated float `amount`.
type PaymentsConsumer struct {
	paymentevents.NopHandler
	svc      PaymentApplier
	consumer *sharedkafka.Consumer
}

// PaymentApplier is the slice of *service.Service the consumer drives.
// An interface so the routing + error-classification logic in handle is
// unit-testable with a recording fake.
type PaymentApplier interface {
	ApplyVerifiedPaymentEvent(ctx context.Context, orderID uuid.UUID, paymentID string) error
	MarkPaymentFailed(ctx context.Context, orderID uuid.UUID, paymentID string) error
	ApplyRefundEvent(ctx context.Context, intentID string) error
}

func NewPaymentsConsumer(
	svc PaymentApplier,
	brokers []string,
	rdb *redis.Client,
	m *metrics.KafkaConsumerMetrics,
) *PaymentsConsumer {
	pc := &PaymentsConsumer{svc: svc}
	pc.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:  brokers,
			GroupID:  "commerce-payments",
			Topic:    "social.events.v1",
			DLQTopic: "social.events.v1.dlq",
		},
		rdb, m, pc.handle,
	)
	return pc
}

// Start blocks; cancel ctx to stop.
func (c *PaymentsConsumer) Start(ctx context.Context) {
	c.consumer.Start(ctx)
}

func (c *PaymentsConsumer) Close() error {
	return c.consumer.Close()
}

// legacyConsumedTypes: only react to payment lifecycle events.
var legacyConsumedTypes = paymentevents.Only(events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded)

func (c *PaymentsConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	err := paymentevents.Dispatch(ctx, env, c, legacyConsumedTypes)
	if errors.Is(err, paymentevents.ErrMalformed) {
		// Malformed payload — log + drop. Don't return error or the consumer
		// will retry forever and eventually DLQ a message that won't parse.
		slog.Warn("payments consumer: bad payload", "event_type", env.EventType, "error", err)
		return nil
	}
	return err
}

// OnRefunded applies payment.refunded.
//
// Refunds are keyed off intent_id (set via SetReturnRefund at
// approve time + Order.refund_intent_id at CancelOrder time), not
// the order reference — payments-service may emit refund events
// for refunds initiated against arbitrary intents. Handle the
// refund branch without the order-ref check.
func (c *PaymentsConsumer) OnRefunded(ctx context.Context, _ *events.EventEnvelope, ev paymentevents.Refunded) error {
	intentID := ev.ID
	if intentID == "" {
		slog.Warn("payments consumer: refund event missing intent id")
		return nil
	}
	if err := c.svc.ApplyRefundEvent(ctx, intentID); err != nil {
		return fmt.Errorf("apply refund for intent %s: %w", intentID, err)
	}
	slog.Info("payments consumer: applied refund", "intent_id", intentID)
	return nil
}

// OnSucceeded applies payment.succeeded.
func (c *PaymentsConsumer) OnSucceeded(ctx context.Context, _ *events.EventEnvelope, ev paymentevents.Succeeded) error {
	orderID, ok := orderReference(ev.Payment)
	if !ok {
		return nil
	}
	// payment.succeeded is published only after payments-service has
	// already HMAC-verified the Razorpay webhook upstream, so this
	// is the system-trusted entry. ApplyVerifiedPaymentEvent is
	// idempotent — the paid transition is one guarded UPDATE, so a
	// replay (or a race with the customer's confirm call) converges
	// without re-running DeductStock / invoice / shipment.
	if err := c.svc.ApplyVerifiedPaymentEvent(ctx, orderID, ev.ProviderRef); err != nil {
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			// Retrying will never make the order appear. Park it in
			// the DLQ (Permanent) rather than spinning the consumer.
			return sharedkafka.Permanent(fmt.Errorf("confirm payment for order %s: %w", orderID, err))
		case errors.Is(err, service.ErrOrderNotPayable):
			// Money was captured for an order that can no longer be
			// paid (cancelled / refunded). Needs a refund by ops —
			// loud log + DLQ, not an infinite retry.
			slog.Error("payments consumer: captured payment for non-payable order — refund required",
				"order_id", orderID, "provider_ref", ev.ProviderRef, "intent_id", ev.ID, "error", err)
			return sharedkafka.Permanent(fmt.Errorf("confirm payment for order %s: %w", orderID, err))
		}
		return fmt.Errorf("confirm payment for order %s: %w", orderID, err)
	}
	slog.Info("payments consumer: confirmed order",
		"order_id", orderID, "provider_ref", ev.ProviderRef)
	return nil
}

// OnFailed applies payment.failed.
func (c *PaymentsConsumer) OnFailed(ctx context.Context, _ *events.EventEnvelope, ev paymentevents.Failed) error {
	orderID, ok := orderReference(ev.Payment)
	if !ok {
		return nil
	}
	if err := c.svc.MarkPaymentFailed(ctx, orderID, ev.ProviderRef); err != nil {
		return fmt.Errorf("mark payment failed for order %s: %w", orderID, err)
	}
	slog.Info("payments consumer: marked failed",
		"order_id", orderID, "provider_ref", ev.ProviderRef)
	return nil
}

// orderReference returns the order a payment is for. Only orders matter to
// commerce-service; future reference types (subscriptions, donations, etc.)
// and an unparseable id are ignored.
func orderReference(p paymentevents.Payment) (uuid.UUID, bool) {
	if p.ReferenceType != "order" {
		return uuid.Nil, false
	}
	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		slog.Warn("payments consumer: bad order id", "reference_id", p.ReferenceID)
		return uuid.Nil, false
	}
	return orderID, true
}
