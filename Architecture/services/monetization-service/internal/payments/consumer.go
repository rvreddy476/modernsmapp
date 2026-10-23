package payments

// The payment-event consumer for memberships.
//
// A membership period is granted ONLY here, from a signed payments-service
// event, never from the intent call, a verify callback or a status poll. The
// inbox row, the decision and the effect commit in one PostgreSQL transaction
// in the store (ApplyMembershipPaymentEvent over paymentevents.ApplyOnce), so:
//
//   - no Redis client is passed to the shared consumer: the dedupe authority is
//     the inbox row that commits with the effect;
//   - RetryForever: a captured payment must not be parked in the DLQ because
//     PostgreSQL blinked. Only genuinely unprocessable input is Permanent.
//
// Three filters run before anything reaches the store: the event must be one
// of the four payment types, stamped for application `memberships`
// (ForApplication drops another application; an event that states none is
// dropped here too, because the application postdates payments stamping it),
// and reference type creator_membership.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Applier applies one event and reports what it did. Duplicate and
// payment-not-found come back as outcomes with a nil error; an error is
// transient and is retried.
type Applier interface {
	ApplyMembershipPaymentEvent(ctx context.Context, ev membership.Event) (membership.Applied, error)
}

// Kafka coordinates.
const (
	ConsumerGroup = "monetization-membership-payments"
	Topic         = "social.events.v1"
	DLQTopic      = "social.events.v1.dlq"
)

// Consumer applies payment events for membership payments.
type Consumer struct {
	paymentevents.NopHandler
	applier  Applier
	consumer *sharedkafka.Consumer
}

// NewConsumer builds the Kafka consumer.
func NewConsumer(applier Applier, brokers []string, m *metrics.KafkaConsumerMetrics) *Consumer {
	c := &Consumer{applier: applier}
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
func NewHandlerForTest(applier Applier) *Consumer { return &Consumer{applier: applier} }

func (c *Consumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *Consumer) Close() error              { return c.consumer.Close() }

var consumedTypes = paymentevents.Only(events.EventPaymentSucceeded, events.EventPaymentFailed,
	events.EventPaymentRefunded, events.EventPaymentRefundFailed)

var errNoEventID = errors.New("payment event has no event_id")

// Handle is the shared/kafka handler.
func (c *Consumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	err := paymentevents.Dispatch(ctx, env, c, consumedTypes, paymentevents.ForApplication(membership.ApplicationID))
	if errors.Is(err, paymentevents.ErrMalformed) {
		slog.Error("memberships: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

type fields struct {
	intentID, payerID, referenceType, referenceID, applicationID string
	amountMinor                                                  int64
	currency, status, reasonCode                                 string
}

func paymentFields(p paymentevents.Payment) fields {
	return fields{intentID: p.ID, payerID: p.PayerID, referenceType: p.ReferenceType, referenceID: p.ReferenceID,
		applicationID: p.ApplicationID, amountMinor: p.AmountMinor, currency: p.Currency, status: p.Status}
}

func (c *Consumer) OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Succeeded) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

func (c *Consumer) OnFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Failed) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

func (c *Consumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.apply(ctx, env, fields{intentID: ev.Intent(), referenceType: ev.ReferenceType,
		referenceID: ev.ReferenceID, applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, status: ev.Status})
}

func (c *Consumer) OnRefundFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.RefundFailed) error {
	intent := ev.IntentID
	if intent == "" {
		intent = ev.ID
	}
	return c.apply(ctx, env, fields{intentID: intent, referenceType: ev.ReferenceType, referenceID: ev.ReferenceID,
		applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, currency: ev.Currency, status: ev.Status,
		reasonCode: ev.ReasonCode})
}

func (c *Consumer) apply(ctx context.Context, env *events.EventEnvelope, p fields) error {
	if p.referenceType != membership.RefType {
		return nil
	}
	if strings.TrimSpace(p.applicationID) == "" {
		slog.Warn("memberships: creator_membership payment event states no application; ignored",
			"event_type", env.EventType, "event_id", env.EventID, "reference_id", p.referenceID)
		return nil
	}
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("memberships: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.referenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	paymentID, err := uuid.Parse(p.referenceID)
	if err != nil {
		slog.Error("memberships: payment event has an unparseable creator_membership reference",
			"event_id", env.EventID, "reference_id", p.referenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.payerID) // uuid.Nil when absent; CheckCapture refuses a nil payer

	ev := membership.Event{
		EventID: env.EventID, EventType: env.EventType, IntentID: p.intentID, PaymentID: paymentID,
		PayerID: payer, AmountMinor: p.amountMinor, Currency: p.currency, Status: p.status, ReasonCode: p.reasonCode,
	}
	applied, err := c.applier.ApplyMembershipPaymentEvent(ctx, ev)
	if err != nil {
		return fmt.Errorf("apply %s for membership payment %s: %w", env.EventType, paymentID, err)
	}
	d := applied.Decision
	switch d.Outcome {
	case membership.OutcomeDuplicate:
		return nil
	case membership.OutcomePaymentNotFound:
		slog.Error("memberships: payment event references an unknown membership payment",
			"payment_id", paymentID, "event_id", env.EventID, "event_type", env.EventType)
		return nil
	case membership.OutcomeMismatch:
		// Recorded and committed with no effect. This must page.
		slog.Error("memberships: PAYMENT MISMATCH — nothing applied",
			"payment_id", paymentID, "event_id", env.EventID, "event_type", env.EventType,
			"event_amount_minor", ev.AmountMinor, "detail", d.Detail)
		return nil
	}
	slog.Info("memberships: payment event applied",
		"event_type", env.EventType, "payment_id", paymentID, "event_id", env.EventID, "outcome", d.Outcome)
	return nil
}
