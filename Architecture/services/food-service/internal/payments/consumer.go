package payments

// The payment-event consumer.
//
// An order becomes paid ONLY here, from a payments-service event, never from
// the client's confirm call. The inbox row, the decision and the guarded
// status transition commit in one PostgreSQL transaction in the store
// (ApplyPaymentEvent), so:
//
//   - no Redis client is passed to the shared consumer: the dedupe authority
//     is the inbox row that commits with the effect (commerce B1);
//   - RetryForever: a captured payment must not be parked in the DLQ because
//     PostgreSQL blinked. Only genuinely unprocessable input is Permanent.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
)

// Applied is what the store reports back after committing.
type Applied struct {
	Decision     Decision
	OrderID      uuid.UUID
	RestaurantID uuid.UUID
	UserID       uuid.UUID
}

// Applier is the store. OutcomeDuplicate and OutcomeOrderNotFound come back
// as outcomes with a nil error; an error is transient and is retried.
type Applier interface {
	ApplyPaymentEvent(ctx context.Context, ev Event) (Applied, error)
}

// Consumer applies payment events for food orders.
type Consumer struct {
	store    Applier
	notify   func(context.Context, Applied)
	consumer *sharedkafka.Consumer
}

// NewConsumer builds the Kafka consumer. notify runs after a committed effect
// (it emits the food.order.payment_* events); it may be nil.
func NewConsumer(store Applier, brokers []string, m *metrics.KafkaConsumerMetrics, notify func(context.Context, Applied)) *Consumer {
	c := &Consumer{store: store, notify: notify}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      "food-payments",
			Topic:        "social.events.v1",
			DLQTopic:     "social.events.v1.dlq",
			RetryForever: true,
		},
		nil, m, c.Handle,
	)
	return c
}

func (c *Consumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *Consumer) Close() error              { return c.consumer.Close() }

// payload mirrors what payments-service publishes. payment.succeeded /
// payment.failed carry the intent row (`id`); payment.refunded carries
// `intent_id` and the refund's amount_minor. The deprecated float `amount`
// is not declared, so nothing can start reading it.
type payload struct {
	ID            string `json:"id"`
	IntentID      string `json:"intent_id"`
	PayerID       string `json:"payer_id"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
}

var errNoEventID = errors.New("payment event has no event_id")

// Handle is the shared/kafka handler.
func (c *Consumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded:
	default:
		return nil
	}

	var p payload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		slog.Error("food: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	// Commerce's orders share this topic. Only food_order references concern
	// food; applying anything else to a food order is the cross-domain
	// confusion servicetoken ref types exist to prevent.
	if p.ReferenceType != RefTypeFoodOrder {
		return nil
	}
	// The event id is the durable dedupe key. An empty one cannot dedupe, and
	// retrying it forever would stall the partition, so it goes to the DLQ.
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("food: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.ReferenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		slog.Error("food: payment event has an unparseable food_order reference",
			"event_id", env.EventID, "reference_id", p.ReferenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.PayerID) // uuid.Nil when absent; Decide refuses a nil payer on capture
	intentID := p.ID
	if intentID == "" {
		intentID = p.IntentID
	}

	ev := Event{
		EventID:     env.EventID,
		EventType:   env.EventType,
		IntentID:    intentID,
		OrderID:     orderID,
		PayerID:     payer,
		AmountMinor: p.AmountMinor,
		Currency:    p.Currency,
		Status:      p.Status,
	}
	applied, err := c.store.ApplyPaymentEvent(ctx, ev)
	if err != nil {
		// Transient: the inbox row rolled back with everything else.
		return fmt.Errorf("apply %s for food order %s: %w", env.EventType, orderID, err)
	}

	d := applied.Decision
	switch d.Outcome {
	case OutcomeDuplicate:
		return nil
	case OutcomeOrderNotFound:
		slog.Error("food: payment event references an unknown food order",
			"order_id", orderID, "event_id", env.EventID)
		return sharedkafka.Permanent(fmt.Errorf("payment event %s: food order %s not found", env.EventID, orderID))
	case OutcomeAmountMismatch:
		// Recorded and committed with no order effect. This must page.
		slog.Error("food: PAYMENT MISMATCH — order not marked paid",
			"order_id", orderID, "event_id", env.EventID, "event_type", env.EventType,
			"event_amount_minor", ev.AmountMinor, "detail", d.Detail)
		return nil
	case OutcomeLateCapture:
		slog.Error("food: LATE CAPTURE on an order that will not be fulfilled — refund required",
			"order_id", orderID, "event_id", env.EventID, "detail", d.Detail)
		return nil
	}
	slog.Info("food: payment event applied",
		"event_type", env.EventType, "order_id", orderID, "event_id", env.EventID, "outcome", d.Outcome)
	if d.Effect != EffectNone && c.notify != nil {
		c.notify(ctx, applied)
	}
	return nil
}
