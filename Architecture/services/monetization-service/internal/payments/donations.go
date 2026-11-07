package payments

// The payment-event consumer for fundraiser donations, on the membership
// consumer's pattern (consumer.go): a donation counts ONLY here, from a
// signed payments-service event, applied once with its inbox row in one
// PostgreSQL transaction (Store.ApplyDonationPaymentEvent). Filters: one of
// the payment event types, stamped for application `fundraisers`, reference
// type fundraiser_donation. RetryForever, Permanent only for unprocessable
// input. Started with the financial writes (MONETIZATION_WRITES_ENABLED).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/monetization-service/internal/donation"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// DonationApplier applies one donation payment event.
type DonationApplier interface {
	ApplyDonationPaymentEvent(ctx context.Context, ev donation.Event) (donation.Applied, error)
}

// DonationConsumerGroup is the donation consumer's own group, so its offsets
// are independent of the membership consumer reading the same topic.
const DonationConsumerGroup = "monetization-fundraiser-payments"

// DonationConsumer applies payment events for fundraiser donations.
type DonationConsumer struct {
	paymentevents.NopHandler
	applier  DonationApplier
	consumer *sharedkafka.Consumer
}

// NewDonationConsumer builds the Kafka consumer.
func NewDonationConsumer(applier DonationApplier, brokers []string, m *metrics.KafkaConsumerMetrics) *DonationConsumer {
	c := &DonationConsumer{applier: applier}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      DonationConsumerGroup,
			Topic:        Topic,
			DLQTopic:     DLQTopic,
			RetryForever: true,
		},
		nil, m, c.Handle,
	)
	return c
}

// NewDonationHandlerForTest builds a DonationConsumer without Kafka.
func NewDonationHandlerForTest(applier DonationApplier) *DonationConsumer {
	return &DonationConsumer{applier: applier}
}

func (c *DonationConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *DonationConsumer) Close() error              { return c.consumer.Close() }

var donationEventTypes = paymentevents.Only(events.EventPaymentSucceeded, events.EventPaymentFailed,
	events.EventPaymentRefunded)

// Handle is the shared/kafka handler.
func (c *DonationConsumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	err := paymentevents.Dispatch(ctx, env, c, donationEventTypes, paymentevents.ForApplication(donation.ApplicationID))
	if errors.Is(err, paymentevents.ErrMalformed) {
		slog.Error("fundraisers: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

func (c *DonationConsumer) OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Succeeded) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

func (c *DonationConsumer) OnFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Failed) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

func (c *DonationConsumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.apply(ctx, env, fields{intentID: ev.Intent(), referenceType: ev.ReferenceType,
		referenceID: ev.ReferenceID, applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, status: ev.Status})
}

func (c *DonationConsumer) apply(ctx context.Context, env *events.EventEnvelope, p fields) error {
	if p.referenceType != donation.RefType {
		return nil
	}
	if strings.TrimSpace(p.applicationID) == "" {
		slog.Warn("fundraisers: fundraiser_donation payment event states no application; ignored",
			"event_type", env.EventType, "event_id", env.EventID, "reference_id", p.referenceID)
		return nil
	}
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("fundraisers: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.referenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	donationID, err := uuid.Parse(p.referenceID)
	if err != nil {
		slog.Error("fundraisers: payment event has an unparseable fundraiser_donation reference",
			"event_id", env.EventID, "reference_id", p.referenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.payerID) // uuid.Nil when absent; CheckCapture refuses a nil payer

	ev := donation.Event{EventID: env.EventID, EventType: env.EventType, IntentID: p.intentID,
		DonationID: donationID, PayerID: payer, AmountMinor: p.amountMinor, Currency: p.currency}
	applied, err := c.applier.ApplyDonationPaymentEvent(ctx, ev)
	if err != nil {
		return fmt.Errorf("apply %s for donation %s: %w", env.EventType, donationID, err)
	}
	switch applied.Decision.Outcome {
	case donation.OutcomeDuplicate:
		return nil
	case donation.OutcomeDonationNotFound:
		slog.Error("fundraisers: payment event references an unknown donation",
			"donation_id", donationID, "event_id", env.EventID, "event_type", env.EventType)
		return nil
	}
	slog.Info("fundraisers: payment event applied",
		"event_type", env.EventType, "donation_id", donationID, "event_id", env.EventID,
		"outcome", applied.Decision.Outcome)
	return nil
}
