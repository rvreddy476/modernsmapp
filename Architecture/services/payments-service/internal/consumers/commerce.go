// Package consumers holds payments-service's Kafka consumers.
//
// payments-service consumed nothing before this: every payment fact reaches
// it from the provider's signed webhook or a service's authenticated call.
// This consumer takes ONE commerce event, commerce.order.duplicate_capture,
// and turns it into a refund of the duplicate payment it names
// (store.RecordOrderDuplicateCapture). It moves no money itself: it writes a
// payments.refund_required row, which the refund-required worker refunds and
// the provider's signed refund webhook resolves.
//
// The pattern is commerce's payment consumer (commerce internal/consumers):
//
//   - group payments-commerce on social.events.v1, dead letters to
//     social.events.v1.dlq;
//   - no Redis dedupe: the durable dedupe is the refund_required row's key,
//     (provider, provider_payment_id), written in the transaction that checks
//     the report, so a redelivery is a no-op whatever its event id;
//   - RetryForever: a database blip may not park money owed back in the DLQ.
//     A report this ledger does not bear out, or a payload that does not
//     parse, is kafka.Permanent: dead-lettered at once and logged at ERROR.
//
// Trust: the topic is the same one commerce takes payment.succeeded from, and
// the report is never taken at its word. Every figure is re-read from this
// ledger, and the refund happens only when the order has a DIFFERENT paid
// intent, so no report can refund an order's only payment.
package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
)

// DuplicateRecorder is the store half the consumer drives.
type DuplicateRecorder interface {
	RecordOrderDuplicateCapture(ctx context.Context, ev postgres.OrderDuplicateCapture) (bool, error)
}

// CommerceConsumer consumes commerce.order.duplicate_capture.
type CommerceConsumer struct {
	store    DuplicateRecorder
	consumer *sharedkafka.Consumer
}

// NewCommerceConsumer builds the consumer.
func NewCommerceConsumer(store DuplicateRecorder, brokers []string, m *metrics.KafkaConsumerMetrics) *CommerceConsumer {
	c := &CommerceConsumer{store: store}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      "payments-commerce",
			Topic:        "social.events.v1",
			DLQTopic:     "social.events.v1.dlq",
			RetryForever: true,
		},
		nil, m, c.Handle,
	)
	return c
}

func (c *CommerceConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *CommerceConsumer) Close() error              { return c.consumer.Close() }

// Handle applies one envelope. Every event but commerce.order.duplicate_capture
// is ignored.
func (c *CommerceConsumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	if env == nil || env.EventType != events.EventCommerceOrderDuplicateCapture {
		return nil
	}
	ev, err := ParseDuplicateCapture(env)
	if err != nil {
		slog.ErrorContext(ctx, "payments: DUPLICATE CAPTURE REPORT UNREADABLE — not refunded; dead-lettered",
			"event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(err)
	}
	recorded, err := c.store.RecordOrderDuplicateCapture(ctx, ev)
	switch {
	case errors.Is(err, postgres.ErrOrderDuplicateRefused):
		slog.ErrorContext(ctx, "payments: DUPLICATE CAPTURE REPORT REFUSED — the ledger does not bear it out; "+
			"nothing refunded, dead-lettered for a person",
			"event_id", env.EventID, "order_id", ev.OrderID, "intent_id", ev.IntentID,
			"bound_intent_id", ev.BoundIntentID, "error", err)
		return sharedkafka.Permanent(err)
	case err != nil:
		return err // retried: the report is still owed an answer
	case recorded:
		slog.ErrorContext(ctx, "payments: DUPLICATE ORDER CAPTURE — the buyer paid twice; the duplicate is recorded "+
			"in payments.refund_required and the refund-required worker returns it when enabled",
			"event_id", env.EventID, "order_id", ev.OrderID, "intent_id", ev.IntentID,
			"bound_intent_id", ev.BoundIntentID, "amount_minor", ev.AmountMinor)
	default:
		slog.InfoContext(ctx, "payments: duplicate order capture already recorded; nothing to do",
			"event_id", env.EventID, "intent_id", ev.IntentID)
	}
	return nil
}

// ParseDuplicateCapture reads a commerce.order.duplicate_capture envelope. The
// ids must be UUIDs and the event id present.
func ParseDuplicateCapture(env *events.EventEnvelope) (postgres.OrderDuplicateCapture, error) {
	var out postgres.OrderDuplicateCapture
	if strings.TrimSpace(env.EventID) == "" {
		return out, errors.New("duplicate capture report has no event id")
	}
	var p events.CommerceOrderDuplicateCapturePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return out, fmt.Errorf("duplicate capture report %s: %w", env.EventID, err)
	}
	ids := make([]uuid.UUID, 3)
	for i, raw := range []string{p.OrderID, p.IntentID, p.BoundIntentID} {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return out, fmt.Errorf("duplicate capture report %s: id %q: %w", env.EventID, raw, err)
		}
		ids[i] = id
	}
	return postgres.OrderDuplicateCapture{
		EventID: env.EventID, OrderID: ids[0], IntentID: ids[1], BoundIntentID: ids[2],
		AmountMinor: p.AmountMinor, Currency: p.Currency,
	}, nil
}
