package payments

// Momentum Premium on payments-service (application `premium`, reference type
// platform_premium, payments migration 019). Same service token as
// memberships (MONETIZATION_SERVICE_TOKEN_KEY/KID), minted for the premium
// reference type: payments-service must list `premium` in
// SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS and platform_premium in
// SERVICE_CALLER_MONETIZATION_SERVICE_REFTYPES, or it refuses the intent.
//
// A response from this client never grants anything: only the signed
// payment.succeeded event does (PremiumConsumer).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/paymentevents"
	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

// PremiumPayeeID is the payee recorded on a premium intent: the platform.
var PremiumPayeeID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/payee/platform-premium"))

// PremiumIntentInput is server-authored: the amount is the period row's.
type PremiumIntentInput struct {
	PeriodID    uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	Method      string
}

// PremiumAPI is what the premium service calls. *PremiumClient satisfies it.
type PremiumAPI interface {
	CreatePremiumIntent(ctx context.Context, in PremiumIntentInput) (*Intent, error)
}

// PremiumClient talks to payments-service for application premium.
type PremiumClient struct {
	c *paymentsclient.Client
}

var _ PremiumAPI = (*PremiumClient)(nil)

// NewPremiumTokenClient builds the client. Both the key and the kid are
// required; there is no legacy key.
func NewPremiumTokenClient(baseURL, kid, signingKeyB64 string) (*PremiumClient, error) {
	if strings.TrimSpace(kid) == "" || strings.TrimSpace(signingKeyB64) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultPaymentsURL
	}
	c, err := paymentsclient.New(paymentsclient.Config{
		BaseURL:               baseURL,
		Service:               serviceName,
		ReferenceType:         premium.RefType,
		Auth:                  paymentsclient.Auth{TokenKey: strings.TrimSpace(signingKeyB64), TokenKID: strings.TrimSpace(kid)},
		MaxResponseBytes:      maxResponseBytes,
		ValidateMethod:        paymentmethod.Validate,
		VerifyReferenceEcho:   true,
		RequirePositiveRefund: true,
	})
	if err != nil {
		return nil, err
	}
	return &PremiumClient{c: c}, nil
}

// PremiumClientFromEnv builds the client from the environment (the same
// variables as the membership client). ErrNotConfigured when the key or kid
// is unset.
func PremiumClientFromEnv(getenv func(string) string) (*PremiumClient, error) {
	return NewPremiumTokenClient(strings.TrimSpace(getenv(EnvURL)), getenv(EnvTokenKID), getenv(EnvTokenKey))
}

// CreatePremiumIntent opens (or, under the same key, re-reads) the intent for
// one pass.
func (c *PremiumClient) CreatePremiumIntent(ctx context.Context, in PremiumIntentInput) (*Intent, error) {
	method := in.Method
	if method == "" {
		method = DefaultMethod
	}
	out, err := c.c.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID:  premium.ApplicationID,
		ReferenceID:    in.PeriodID,
		PayerID:        in.PayerID,
		PayeeID:        PremiumPayeeID,
		AmountMinor:    in.AmountMinor,
		Currency:       in.Currency,
		Method:         method,
		IdempotencyKey: premium.IntentKey(in.PeriodID),
	})
	if err != nil {
		return nil, err
	}
	i := &Intent{ID: out.ID, Status: out.Status, AmountMinor: out.AmountMinor, Currency: out.Currency,
		ProviderRef: out.ProviderRef, ReferenceID: out.ReferenceID}
	if out.ClientSession != nil {
		i.ClientSession = &ClientSession{Provider: out.ClientSession.Provider, OrderID: out.ClientSession.OrderID,
			KeyID: out.ClientSession.KeyID, MerchantDisplayName: out.ClientSession.MerchantDisplayName}
	}
	return i, nil
}

// ---------------------------------------------------------------------------
// The consumer
// ---------------------------------------------------------------------------

// PremiumApplier applies one premium payment event.
type PremiumApplier interface {
	ApplyPremiumPaymentEvent(ctx context.Context, ev premium.Event) (premium.Outcome, error)
}

// PremiumConsumerGroup is the premium consumer's own group, so its offsets are
// independent of the membership and donation consumers on the same topic.
const PremiumConsumerGroup = "monetization-premium-payments"

// PremiumConsumer applies payment events for Premium passes: the ONLY thing
// that grants a pass. Filters before the store: one of succeeded / failed /
// refunded, stamped for application premium (an event that states none is
// dropped), reference type platform_premium. RetryForever; Permanent only for
// unprocessable input.
type PremiumConsumer struct {
	paymentevents.NopHandler
	applier  PremiumApplier
	consumer *sharedkafka.Consumer
}

// NewPremiumConsumer builds the Kafka consumer.
func NewPremiumConsumer(applier PremiumApplier, brokers []string, m *metrics.KafkaConsumerMetrics) *PremiumConsumer {
	c := &PremiumConsumer{applier: applier}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:      brokers,
			GroupID:      PremiumConsumerGroup,
			Topic:        Topic,
			DLQTopic:     DLQTopic,
			RetryForever: true,
		},
		nil, m, c.Handle,
	)
	return c
}

// NewPremiumHandlerForTest builds a PremiumConsumer without Kafka.
func NewPremiumHandlerForTest(applier PremiumApplier) *PremiumConsumer {
	return &PremiumConsumer{applier: applier}
}

func (c *PremiumConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *PremiumConsumer) Close() error              { return c.consumer.Close() }

var premiumEventTypes = paymentevents.Only(events.EventPaymentSucceeded, events.EventPaymentFailed,
	events.EventPaymentRefunded)

// Handle is the shared/kafka handler.
func (c *PremiumConsumer) Handle(ctx context.Context, env *events.EventEnvelope) error {
	err := paymentevents.Dispatch(ctx, env, c, premiumEventTypes, paymentevents.ForApplication(premium.ApplicationID))
	if errors.Is(err, paymentevents.ErrMalformed) {
		slog.Error("premium: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

func (c *PremiumConsumer) OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Succeeded) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

func (c *PremiumConsumer) OnFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Failed) error {
	return c.apply(ctx, env, paymentFields(ev.Payment))
}

func (c *PremiumConsumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.apply(ctx, env, fields{intentID: ev.Intent(), referenceType: ev.ReferenceType,
		referenceID: ev.ReferenceID, applicationID: ev.ApplicationID, amountMinor: ev.AmountMinor, status: ev.Status})
}

func (c *PremiumConsumer) apply(ctx context.Context, env *events.EventEnvelope, p fields) error {
	if p.referenceType != premium.RefType {
		return nil
	}
	if strings.TrimSpace(p.applicationID) == "" {
		slog.Warn("premium: platform_premium payment event states no application; ignored",
			"event_type", env.EventType, "event_id", env.EventID, "reference_id", p.referenceID)
		return nil
	}
	if strings.TrimSpace(env.EventID) == "" {
		slog.Error("premium: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType, "reference_id", p.referenceID)
		return sharedkafka.Permanent(errNoEventID)
	}
	periodID, err := uuid.Parse(p.referenceID)
	if err != nil {
		slog.Error("premium: payment event has an unparseable platform_premium reference",
			"event_id", env.EventID, "reference_id", p.referenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payer, _ := uuid.Parse(p.payerID) // uuid.Nil when absent; CheckCapture refuses a nil payer

	ev := premium.Event{EventID: env.EventID, EventType: env.EventType, IntentID: p.intentID, PeriodID: periodID,
		PayerID: payer, AmountMinor: p.amountMinor, Currency: p.currency}
	outcome, err := c.applier.ApplyPremiumPaymentEvent(ctx, ev)
	if err != nil {
		return fmt.Errorf("apply %s for premium period %s: %w", env.EventType, periodID, err)
	}
	switch outcome {
	case premium.OutcomeDuplicate:
		return nil
	case premium.OutcomePeriodNotFound:
		slog.Error("premium: payment event references an unknown pass",
			"period_id", periodID, "event_id", env.EventID, "event_type", env.EventType)
		return nil
	case premium.OutcomeMismatch:
		slog.Error("premium: PAYMENT MISMATCH — nothing applied",
			"period_id", periodID, "event_id", env.EventID, "event_type", env.EventType, "event_amount_minor", ev.AmountMinor)
		return nil
	}
	slog.Info("premium: payment event applied",
		"event_type", env.EventType, "period_id", periodID, "event_id", env.EventID, "outcome", outcome)
	return nil
}
