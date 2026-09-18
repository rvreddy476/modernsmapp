package payments

// The payment-event consumer.
//
// A ride payment is marked paid, failed or refunded, and an outstanding fee
// settled, ONLY here, from a payments-service event, never from the intent
// call, the callback verdict or a status poll. The inbox row, the decision
// and the row change commit in one PostgreSQL transaction in the store
// (ApplyRidePaymentEvent, over paymentevents.ApplyOnce), so:
//
//   - no Redis client is passed to the shared consumer: the dedupe authority
//     is the rider_payment_inbox row that commits with the effect;
//   - RetryForever: a captured payment must not be parked in the DLQ because
//     PostgreSQL blinked. Only genuinely unprocessable input is Permanent.
//
// Three filters run before anything reaches the store: the event must be
// one of the three consumed payment types, stamped for application `mopedu`
// (ForApplication drops another application; an event that states no
// application is dropped here too, because Mopedu's application postdates
// payments stamping it), and reference type mopedu_ride.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Applier is the store. OutcomeDuplicate and OutcomeTargetNotFound come
// back as outcomes with a nil error; an error is transient and is retried.
type Applier interface {
	ApplyRidePaymentEvent(ctx context.Context, ev Event) (Applied, error)
}

// Consumer applies payment events for Mopedu rides and outstanding fees.
type Consumer struct {
	paymentevents.NopHandler
	store    Applier
	notify   func(context.Context, Applied)
	consumer *sharedkafka.Consumer
}

// ConsumerGroup and Topic are the Kafka coordinates. payments-service
// publishes on the shared social.events.v1 topic.
const (
	ConsumerGroup = "rider-payments"
	Topic         = "social.events.v1"
	DLQTopic      = "social.events.v1.dlq"
)

// NewConsumer builds the Kafka consumer. notify runs after a committed
// effect (realtime frames to the ride topic); it may be nil.
func NewConsumer(store Applier, brokers []string, m *metrics.KafkaConsumerMetrics, notify func(context.Context, Applied)) *Consumer {
	c := &Consumer{store: store, notify: notify}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      ConsumerGroup,
			Topic:        Topic,
			DLQTopic:     DLQTopic,
			RetryForever: true,
		},
		nil, m, c.Handle,
	)
	return c
}

// NewHandlerForTest builds a Consumer without Kafka, for Handle-level tests.
func NewHandlerForTest(store Applier, notify func(context.Context, Applied)) *Consumer {
	return &Consumer{store: store, notify: notify}
}

func (c *Consumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *Consumer) Close() error              { return c.consumer.Close() }

// consumedTypes: every other type on the shared topic, payment.refund_failed
// included, is ignored without being decoded.
var consumedTypes = paymentevents.Only(events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded)

var errNoEventID = errors.New("payment event has no event_id")

// Handle is the shared/kafka handler.
func (c *Consumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	err := paymentevents.Dispatch(ctx, env, c, consumedTypes, paymentevents.ForApplication(ApplicationID))
	if errors.Is(err, paymentevents.ErrMalformed) {
		slog.Error("rider: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

// fields is the part of a payment payload a rider decision reads.
type fields struct {
	intentID      string
	payerID       string
	referenceType string
	referenceID   string
	applicationID string
	amountMinor   int64
	currency      string
	status        string
	providerRef   string
}

func paymentFields(p paymentevents.Payment) fields {
	return fields{intentID: p.ID, payerID: p.PayerID, referenceType: p.ReferenceType, referenceID: p.ReferenceID,
		applicationID: p.ApplicationID, amountMinor: p.AmountMinor, currency: p.Currency, status: p.Status, providerRef: p.ProviderRef}
}

// OnSucceeded applies payment.succeeded.
func (c *Consumer) OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Succeeded) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

// OnFailed applies payment.failed.
func (c *Consumer) OnFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Failed) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

// OnRefunded applies payment.refunded: `intent_id` names the intent when
// `id` is absent, and amount_minor is the refund's.
func (c *Consumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.apply(ctx, env, fields{intentID: ev.Intent(), referenceType: ev.ReferenceType,
		referenceID: ev.ReferenceID, applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, status: ev.Status,
		providerRef: ev.ProviderRefundID})
}

func (c *Consumer) apply(ctx context.Context, env *events.EventEnvelope, p fields) error {
	// Commerce's, food's and dating's payments share this topic. Only
	// mopedu_ride references concern rider.
	if p.referenceType != RefTypeMopeduRide {
		return nil
	}
	// ForApplication already dropped another application. An event that
	// does not state one predates payments stamping it, and so cannot be
	// Mopedu's.
	if strings.TrimSpace(p.applicationID) == "" {
		slog.Warn("rider: mopedu_ride payment event states no application; ignored",
			"event_type", env.EventType, "event_id", env.EventID, "reference_id", p.referenceID)
		return nil
	}
	// The event id is the durable dedupe key. An empty one cannot dedupe,
	// and retrying it forever would stall the partition, so it goes to the
	// DLQ.
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("rider: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.referenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	referenceID, err := uuid.Parse(p.referenceID)
	if err != nil {
		slog.Error("rider: payment event has an unparseable mopedu_ride reference",
			"event_id", env.EventID, "reference_id", p.referenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.payerID) // uuid.Nil when absent; Decide refuses a nil payer on capture

	ev := Event{
		EventID:     env.EventID,
		EventType:   env.EventType,
		IntentID:    p.intentID,
		ReferenceID: referenceID,
		PayerID:     payer,
		AmountMinor: p.amountMinor,
		Currency:    p.currency,
		Status:      p.status,
		ProviderRef: p.providerRef,
	}
	applied, err := c.store.ApplyRidePaymentEvent(ctx, ev)
	if err != nil {
		// Transient: the inbox row rolled back with everything else.
		return fmt.Errorf("apply %s for mopedu reference %s: %w", env.EventType, referenceID, err)
	}

	d := applied.Decision
	switch d.Outcome {
	case OutcomeDuplicate:
		return nil
	case OutcomeTargetNotFound:
		// Recorded and committed (a reference that is neither a ride nor an
		// outstanding row); not retried.
		slog.Error("rider: payment event references an unknown ride or outstanding fee",
			"reference_id", referenceID, "event_id", env.EventID, "event_type", env.EventType)
		return nil
	case OutcomeMismatch:
		// Recorded and committed with no payment effect, plus a
		// reconciliation_required row. This must page.
		slog.Error("rider: PAYMENT MISMATCH — nothing marked paid; reconciliation required",
			"reference_id", referenceID, "event_id", env.EventID, "event_type", env.EventType,
			"event_amount_minor", ev.AmountMinor, "detail", d.Detail)
		return nil
	}
	slog.Info("rider: payment event applied",
		"event_type", env.EventType, "target", applied.Target, "reference_id", referenceID, "event_id", env.EventID, "outcome", d.Outcome)
	if d.Effect != EffectNone && c.notify != nil {
		c.notify(ctx, applied)
	}
	return nil
}
