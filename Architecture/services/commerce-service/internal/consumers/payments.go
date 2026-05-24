// Package consumers contains Kafka consumers that drive commerce-service
// state transitions in response to events from sibling services.
package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// PaymentsConsumer listens for payment lifecycle events from payments-service.
// On payment.succeeded for an order reference, it confirms the order
// (mark paid → deduct stock → kick off invoice + shipment fulfillment).
// On payment.failed, it marks the order as payment_failed so the UI can
// prompt the customer to retry, and stock reservations age out naturally.
type PaymentsConsumer struct {
	svc      *service.Service
	consumer *sharedkafka.Consumer
}

// paymentEventPayload mirrors the JSON-marshalled PaymentIntent that
// payments-service publishes inside its EventEnvelope.Payload.
type paymentEventPayload struct {
	ID            string  `json:"id"`
	PayerID       string  `json:"payer_id"`
	PayeeID       string  `json:"payee_id"`
	ReferenceType string  `json:"reference_type"`
	ReferenceID   string  `json:"reference_id"`
	Amount        float64 `json:"amount"`
	Method        string  `json:"method"`
	Status        string  `json:"status"`
	ProviderRef   string  `json:"provider_ref,omitempty"`
}

func NewPaymentsConsumer(
	svc *service.Service,
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

func (c *PaymentsConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	// Only react to payment lifecycle events.
	switch env.EventType {
	case events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded:
	default:
		return nil
	}

	var p paymentEventPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		// Malformed payload — log + drop. Don't return error or the consumer
		// will retry forever and eventually DLQ a message that won't parse.
		slog.Warn("payments consumer: bad payload", "event_type", env.EventType, "error", err)
		return nil
	}

	// Refunds are keyed off intent_id (set via SetReturnRefund at
	// approve time + Order.refund_intent_id at CancelOrder time), not
	// the order reference — payments-service may emit refund events
	// for refunds initiated against arbitrary intents. Handle the
	// refund branch up-front so we don't bail on the order-ref check.
	if env.EventType == events.EventPaymentRefunded {
		intentID := p.ID
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

	// Only orders matter to commerce-service. Future reference types
	// (subscriptions, donations, etc.) get filtered here.
	if p.ReferenceType != "order" {
		return nil
	}

	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		slog.Warn("payments consumer: bad order id", "reference_id", p.ReferenceID)
		return nil
	}

	switch env.EventType {
	case events.EventPaymentSucceeded:
		// payment.succeeded is published only after payments-service has
		// already HMAC-verified the Razorpay webhook upstream, so this
		// is the system-trusted entry. ApplyVerifiedPaymentEvent is
		// idempotent — UpdatePaymentStatus is row-level, and DeductStock
		// + invoice + shipment fan out from there.
		if err := c.svc.ApplyVerifiedPaymentEvent(ctx, orderID, p.ProviderRef); err != nil {
			return fmt.Errorf("confirm payment for order %s: %w", orderID, err)
		}
		slog.Info("payments consumer: confirmed order",
			"order_id", orderID, "provider_ref", p.ProviderRef)
	case events.EventPaymentFailed:
		if err := c.svc.MarkPaymentFailed(ctx, orderID, p.ProviderRef); err != nil {
			return fmt.Errorf("mark payment failed for order %s: %w", orderID, err)
		}
		slog.Info("payments consumer: marked failed",
			"order_id", orderID, "provider_ref", p.ProviderRef)
	}
	return nil
}
