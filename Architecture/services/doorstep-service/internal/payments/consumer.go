package payments

// The payment-event consumer (the rider/food pattern).
//
// No Redis client is passed to the shared consumer: the dedupe authority is
// the doorstep.payment_inbox row that commits with the effect. RetryForever:
// a captured payment must never be parked in the DLQ because PostgreSQL
// blinked; only genuinely unprocessable input is Permanent.
//
// Filters before the store: one of the four payment types; stamped for
// application doorstep (ForApplication drops another application, and an
// event that states none is dropped here too, because Doorstep postdates
// payments stamping applications); reference type doorstep_booking.
// doorstep_extras events are left unclaimed for the visit lane (A5) — no
// extras intent exists before it.

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

// Kafka coordinates: payments-service publishes on the shared topic.
const (
	ConsumerGroup = "doorstep-payments"
	Topic         = "social.events.v1"
	DLQTopic      = "social.events.v1.dlq"
)

// Applied is what the store reports after committing.
type Applied struct {
	Decision  Decision
	BookingID uuid.UUID
	// RefundIDs are refund rows created in the transaction, to submit to
	// payments-service after commit (the resubmit worker retries any that
	// fail).
	RefundIDs []uuid.UUID
}

// Applier is the store. Duplicate and not-found come back as outcomes with
// a nil error; an error is transient and is retried.
type Applier interface {
	ApplyPaymentEvent(ctx context.Context, ev Event) (Applied, error)
}

// Consumer applies payment events for Doorstep bookings.
type Consumer struct {
	paymentevents.NopHandler
	store    Applier
	after    func(context.Context, Applied)
	consumer *sharedkafka.Consumer
}

// NewConsumer builds the Kafka consumer. after runs once the effect has
// committed (refund submission); it may be nil.
func NewConsumer(store Applier, brokers []string, m *metrics.KafkaConsumerMetrics, after func(context.Context, Applied)) *Consumer {
	c := &Consumer{store: store, after: after}
	c.consumer = sharedkafka.NewConsumer(sharedkafka.ConsumerConfig{
		Brokers: brokers, GroupID: ConsumerGroup, Topic: Topic, DLQTopic: DLQTopic, RetryForever: true,
	}, nil, m, c.Handle)
	return c
}

// NewHandler builds a Consumer without Kafka (tests, and the in-process
// dispatch of the integration suite).
func NewHandler(store Applier, after func(context.Context, Applied)) *Consumer {
	return &Consumer{store: store, after: after}
}

// Start runs the consumer until ctx ends.
func (c *Consumer) Start(ctx context.Context) { c.consumer.Start(ctx) }

// Close stops the reader.
func (c *Consumer) Close() error { return c.consumer.Close() }

var consumedTypes = paymentevents.Only(events.EventPaymentSucceeded, events.EventPaymentFailed,
	events.EventPaymentRefunded, events.EventPaymentRefundFailed)

var errNoEventID = errors.New("payment event has no event_id")

// Handle is the shared/kafka handler.
func (c *Consumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	err := paymentevents.Dispatch(ctx, env, c, consumedTypes, paymentevents.ForApplication(ApplicationID))
	if errors.Is(err, paymentevents.ErrMalformed) {
		slog.Error("doorstep: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

type fields struct {
	intentID, payerID, referenceType, referenceID, applicationID string
	amountMinor                                                  int64
	currency, status, providerRef, commandID, reason             string
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

// OnRefunded applies payment.refunded (intent_id names the intent when id
// is absent; amount_minor is the refund's).
func (c *Consumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.apply(ctx, env, fields{intentID: ev.Intent(), referenceType: ev.ReferenceType, referenceID: ev.ReferenceID,
		applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, status: ev.Status, commandID: ev.CommandID})
}

// OnRefundFailed applies payment.refund_failed.
func (c *Consumer) OnRefundFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.RefundFailed) error {
	intent := ev.ID
	if intent == "" {
		intent = ev.IntentID
	}
	return c.apply(ctx, env, fields{intentID: intent, referenceType: ev.ReferenceType, referenceID: ev.ReferenceID,
		applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, currency: ev.Currency, status: ev.Status,
		commandID: ev.CommandID, reason: strings.TrimSpace(ev.ReasonCode + " " + ev.Reason)})
}

func (c *Consumer) apply(ctx context.Context, env *events.EventEnvelope, p fields) error {
	// Dispatch admits an event that states no application; Doorstep does not.
	if p.applicationID != ApplicationID {
		return nil
	}
	if p.referenceType != RefBooking {
		return nil // doorstep_extras: the visit lane (A5)
	}
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("doorstep: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.referenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	bookingID, err := uuid.Parse(p.referenceID)
	if err != nil {
		slog.Error("doorstep: payment event has an unparseable booking reference", "event_id", env.EventID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.payerID) // Nil when absent; CheckCapture refuses a nil payer
	ev := Event{EventID: env.EventID, EventType: env.EventType, IntentID: p.intentID, BookingID: bookingID,
		PayerID: payer, AmountMinor: p.amountMinor, Currency: p.currency, Status: p.status,
		ProviderRef: p.providerRef, CommandID: p.commandID, Reason: p.reason}
	applied, err := c.store.ApplyPaymentEvent(ctx, ev)
	if err != nil {
		return fmt.Errorf("apply %s for doorstep booking %s: %w", env.EventType, bookingID, err)
	}
	switch applied.Decision.Outcome {
	case OutcomeDuplicate:
		return nil
	case OutcomeBookingNotFound:
		slog.Error("doorstep: payment event references an unknown booking", "booking_id", bookingID, "event_id", env.EventID)
		return sharedkafka.Permanent(fmt.Errorf("payment event %s: booking %s not found", env.EventID, bookingID))
	case OutcomeMismatch:
		slog.Error("doorstep: PAYMENT MISMATCH — booking not confirmed, flagged for attention",
			"booking_id", bookingID, "event_id", env.EventID, "event_type", env.EventType, "detail", applied.Decision.Detail)
	case OutcomeRefundFailed:
		slog.Error("doorstep: REFUND FAILED at payments — booking flagged for attention",
			"booking_id", bookingID, "event_id", env.EventID, "detail", applied.Decision.Detail)
	case OutcomeLateCaptureRefund:
		slog.Warn("doorstep: late capture on a booking that will not happen — full refund requested",
			"booking_id", bookingID, "event_id", env.EventID)
	default:
		slog.Info("doorstep: payment event applied", "event_type", env.EventType, "booking_id", bookingID,
			"event_id", env.EventID, "outcome", applied.Decision.Outcome)
	}
	if c.after != nil {
		c.after(ctx, applied)
	}
	return nil
}
