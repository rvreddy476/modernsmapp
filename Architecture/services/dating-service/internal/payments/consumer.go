package payments

// The payment-event consumer.
//
// A pass or a Boost is granted ONLY here, from a payments-service event, never
// from the purchase call or a status poll. The inbox row, the decision and the
// entitlement change commit in one PostgreSQL transaction in the store
// (ApplyPremiumPaymentEvent, over paymentevents.ApplyOnce), so:
//
//   - no Redis client is passed to the shared consumer: the dedupe authority is
//     the inbox row that commits with the effect;
//   - RetryForever: a captured payment must not be parked in the DLQ because
//     PostgreSQL blinked. Only genuinely unprocessable input is Permanent.
//
// Three filters run before anything reaches the store: the event must be one
// of the three consumed payment types, stamped for application `dating`
// (ForApplication drops another application; an event that states no
// application is dropped here too, because dating's application postdates
// payments stamping it), and reference type dating_premium.

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

// Applier is the store. OutcomeDuplicate and OutcomePurchaseNotFound come back
// as outcomes with a nil error; an error is transient and is retried.
type Applier interface {
	ApplyPremiumPaymentEvent(ctx context.Context, ev Event) (Applied, error)
}

// Consumer applies payment events for premium purchases.
type Consumer struct {
	paymentevents.NopHandler
	store    Applier
	notify   func(context.Context, Applied)
	consumer *sharedkafka.Consumer
}

// ConsumerGroup and Topic are the Kafka coordinates.
const (
	ConsumerGroup = "dating-payments"
	Topic         = "social.events.v1"
	DLQTopic      = "social.events.v1.dlq"
)

// NewConsumer builds the Kafka consumer. notify runs after a committed effect
// (it emits the dating.premium.* events); it may be nil.
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
		slog.Error("dating: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

// fields is the part of a payment payload a dating decision reads.
type fields struct {
	intentID      string
	payerID       string
	referenceType string
	referenceID   string
	applicationID string
	amountMinor   int64
	currency      string
	status        string
}

func paymentFields(p paymentevents.Payment) fields {
	return fields{intentID: p.ID, payerID: p.PayerID, referenceType: p.ReferenceType, referenceID: p.ReferenceID,
		applicationID: p.ApplicationID, amountMinor: p.AmountMinor, currency: p.Currency, status: p.Status}
}

// OnSucceeded applies payment.succeeded.
func (c *Consumer) OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Succeeded) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

// OnFailed applies payment.failed.
func (c *Consumer) OnFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Failed) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

// OnRefunded applies payment.refunded: `intent_id` names the intent when `id`
// is absent, and amount_minor is the refund's.
func (c *Consumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.apply(ctx, env, fields{intentID: ev.Intent(), referenceType: ev.ReferenceType,
		referenceID: ev.ReferenceID, applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, status: ev.Status})
}

func (c *Consumer) apply(ctx context.Context, env *events.EventEnvelope, p fields) error {
	// Commerce's and food's payments share this topic. Only dating_premium
	// references concern dating.
	if p.referenceType != RefTypeDatingPremium {
		return nil
	}
	// ForApplication already dropped another application. An event that does
	// not state one predates payments stamping it, and so cannot be dating's.
	if strings.TrimSpace(p.applicationID) == "" {
		slog.Warn("dating: dating_premium payment event states no application; ignored",
			"event_type", env.EventType, "event_id", env.EventID, "reference_id", p.referenceID)
		return nil
	}
	// The event id is the durable dedupe key. An empty one cannot dedupe, and
	// retrying it forever would stall the partition, so it goes to the DLQ.
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("dating: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.referenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	purchaseID, err := uuid.Parse(p.referenceID)
	if err != nil {
		slog.Error("dating: payment event has an unparseable dating_premium reference",
			"event_id", env.EventID, "reference_id", p.referenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.payerID) // uuid.Nil when absent; Decide refuses a nil payer on capture

	ev := Event{
		EventID:     env.EventID,
		EventType:   env.EventType,
		IntentID:    p.intentID,
		PurchaseID:  purchaseID,
		PayerID:     payer,
		AmountMinor: p.amountMinor,
		Currency:    p.currency,
		Status:      p.status,
	}
	applied, err := c.store.ApplyPremiumPaymentEvent(ctx, ev)
	if err != nil {
		// Transient: the inbox row rolled back with everything else.
		return fmt.Errorf("apply %s for premium purchase %s: %w", env.EventType, purchaseID, err)
	}

	d := applied.Decision
	switch d.Outcome {
	case OutcomeDuplicate:
		return nil
	case OutcomePurchaseNotFound:
		// Recorded and committed (a purchase purged with its account, or one
		// that never existed); not retried.
		slog.Error("dating: payment event references an unknown premium purchase",
			"purchase_id", purchaseID, "event_id", env.EventID, "event_type", env.EventType)
		return nil
	case OutcomeAmountMismatch:
		// Recorded and committed with no entitlement effect. This must page.
		slog.Error("dating: PAYMENT MISMATCH — premium not granted or revoked",
			"purchase_id", purchaseID, "event_id", env.EventID, "event_type", env.EventType,
			"event_amount_minor", ev.AmountMinor, "detail", d.Detail)
		return nil
	}
	slog.Info("dating: payment event applied",
		"event_type", env.EventType, "purchase_id", purchaseID, "event_id", env.EventID, "outcome", d.Outcome)
	if d.Effect != EffectNone && c.notify != nil {
		c.notify(ctx, applied)
	}
	return nil
}
