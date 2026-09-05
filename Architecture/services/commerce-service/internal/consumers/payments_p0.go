package consumers

// The payment-event consumer, rewritten for A3 / review R-1 and LB-5.
//
// Two defects, both of which lose real money:
//
// R-1 — the durability gap. The shared Kafka consumer performs a Redis
// SETNX BEFORE invoking the handler and commits the offset after. If
// commerce dies between the SETNX and the database write, the restarted
// consumer finds the key, treats the event as already handled, and commits
// the offset. Razorpay has captured ₹10,000, the order stays unpaid forever,
// and nothing anywhere records that it was owed. Redis is a cache being used
// as a transaction log.
//
// LB-5 — the verification gap. The previous handler unmarshalled
// `AmountMinor` into a struct field and never compared it to anything. A
// payment for 1 paise, or for a different order entirely, marked the order
// paid as long as payments-service had verified the signature.
//
// The fix for both is the same move: the dedupe record, the verification and
// the effect all happen inside ONE PostgreSQL transaction, in the store.
//
// B1 — what the paragraph above USED to claim, and why it was wrong. This
// file previously said "Redis stays as a fast path that can be wrong without
// consequence, because the database is now the authority." That was false in
// the only direction that matters. The shared consumer's pre-handler SETNX
// meant a Redis key could outlive a rolled-back transaction, and the next
// delivery was skipped on the strength of it — so the cache could veto the
// authority. Two changes close it:
//
//	1. shared/kafka now writes the dedupe key only AFTER the handler
//	   succeeds, so a key can no longer precede its effect;
//	2. this consumer passes no Redis client at all. Its dedupe authority is
//	   the `payment_events` inbox row, written in the same transaction as the
//	   effect. There is no second opinion to get wrong.
//
// RetryForever is set because a transient failure on a captured payment must
// not become a parked DLQ item after three attempts. Genuinely unprocessable
// input is wrapped in kafka.Permanent by the handler below and reaches the
// DLQ immediately; everything else is retried until PostgreSQL accepts it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// P0PaymentsConsumer applies payment events durably.
type P0PaymentsConsumer struct {
	store    *postgres.Store
	consumer *sharedkafka.Consumer
	obs      Observer
}

// Observer surfaces the money-integrity counters. Kept as an interface so
// the consumer does not import the metrics registry directly and can be
// driven by a fake in tests.
type Observer interface {
	AmountMismatch(orderID uuid.UUID, eventMinor, orderMinor int64)
	DuplicateSuppressed(eventID string)
	Applied(eventType string)
}

// NewP0PaymentsConsumer builds the consumer.
//
// B1: the `rdb` parameter is accepted and deliberately DISCARDED. It is kept
// in the signature so main.go and every existing call site continue to
// compile unchanged, and so that a future edit that "restores" the argument
// has to delete this comment to do it. Money events do not dedupe against a
// cache; they dedupe against the `payment_events` inbox row that commits with
// their effect.
func NewP0PaymentsConsumer(
	store *postgres.Store,
	brokers []string,
	_ *redis.Client,
	m *metrics.KafkaConsumerMetrics,
	obs Observer,
) *P0PaymentsConsumer {
	c := &P0PaymentsConsumer{store: store, obs: obs}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:  brokers,
			GroupID:  "commerce-payments",
			Topic:    "social.events.v1",
			DLQTopic: "social.events.v1.dlq",
			// A captured payment may not be abandoned to the DLQ because
			// PostgreSQL was briefly unreachable. Poison is separated by the
			// handler with kafka.Permanent, not by an attempt count.
			RetryForever: true,
		},
		nil, m, c.handle,
	)
	return c
}

func (c *P0PaymentsConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }
func (c *P0PaymentsConsumer) Close() error              { return c.consumer.Close() }

// paymentPayload mirrors what payments-service publishes.
//
// AmountMinor is the only amount field read. The deprecated float `amount`
// mirror is deliberately NOT declared here: if it is not in the struct, no
// future edit can accidentally start comparing against it.
type paymentPayload struct {
	ID            string      `json:"id"`
	PayerID       string      `json:"payer_id"`
	PayeeID       string      `json:"payee_id"`
	ReferenceType string      `json:"reference_type"`
	ReferenceID   string      `json:"reference_id"`
	AmountMinor   money.Paise `json:"amount_minor"`
	Currency      string      `json:"currency"`
	Method        string      `json:"method"`
	Status        string      `json:"status"`
	ProviderRef   string      `json:"provider_ref,omitempty"`
}

func (c *P0PaymentsConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded:
	default:
		return nil
	}

	// R-1: the envelope's event id becomes the DURABLE inbox key. An empty
	// one cannot dedupe anything, and worse, one empty key would occupy the
	// primary key and make every later event look like a duplicate. Refuse
	// rather than guess.
	if env.EventID == "" {
		slog.Error("commerce: payment event has no event_id; refusing to apply it without a dedupe key",
			"event_type", env.EventType)
		return fmt.Errorf("payment event has no event_id")
	}

	var p paymentPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		// A malformed payload will never parse, so retrying forever is
		// pointless — but it must be visible, not silently dropped the way
		// the old handler dropped it with a Warn and a nil return.
		slog.Error("commerce: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}

	if env.EventType == events.EventPaymentRefunded {
		return c.applyRefund(ctx, env, p)
	}

	// Only order references concern commerce. food-service's intents share
	// this topic, and applying one of those to an order would be exactly the
	// cross-domain confusion D4 exists to prevent.
	if p.ReferenceType != "order" {
		return nil
	}
	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		slog.Warn("commerce: payment event has an unparseable order reference",
			"reference_id", p.ReferenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	payerID, _ := uuid.Parse(p.PayerID)

	ev := postgres.PaymentEvent{
		EventID:     env.EventID,
		EventType:   env.EventType,
		IntentID:    p.ID,
		OrderID:     orderID,
		AmountMinor: p.AmountMinor,
		Currency:    p.Currency,
		PayerID:     payerID,
		ProviderRef: p.ProviderRef,
	}

	switch env.EventType {
	case events.EventPaymentSucceeded:
		err = c.store.ApplyPaymentSucceeded(ctx, ev)
	case events.EventPaymentFailed:
		err = c.store.ApplyPaymentFailed(ctx, ev)
	}

	switch {
	case err == nil:
		if c.obs != nil {
			c.obs.Applied(env.EventType)
		}
		slog.Info("commerce: payment event applied",
			"event_type", env.EventType, "order_id", orderID, "event_id", env.EventID)
		return nil

	case errors.Is(err, postgres.ErrDuplicatePaymentEvt):
		// Genuinely already applied, proven by a database row rather than a
		// Redis key that may have outlived the work it stood for.
		if c.obs != nil {
			c.obs.DuplicateSuppressed(env.EventID)
		}
		return nil

	case errors.Is(err, postgres.ErrAmountMismatch):
		// LB-5 / C6. This is the alarm that must page. The order is NOT
		// marked paid: a payment whose amount, currency, payer or intent
		// does not match the order is either a bug in the payment path or
		// an attempt to settle a ₹10,000 order with ₹1.
		//
		// Returning poison rather than retrying: the event will never
		// become valid, and leaving it to spin would bury the signal.
		if c.obs != nil {
			c.obs.AmountMismatch(orderID, ev.AmountMinor.Int64(), 0)
		}
		slog.Error("commerce: PAYMENT AMOUNT MISMATCH — order not marked paid",
			"order_id", orderID, "event_id", env.EventID,
			"event_amount_minor", ev.AmountMinor.Int64(), "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))

	case errors.Is(err, postgres.ErrOrderNotFoundP0):
		slog.Error("commerce: payment event references an unknown order",
			"order_id", orderID, "event_id", env.EventID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))

	default:
		// Transient. Return the error so the consumer retries — the inbox
		// row rolled back with the rest of the transaction, so a retry is
		// safe and will be applied exactly once.
		return fmt.Errorf("apply %s for order %s: %w", env.EventType, orderID, err)
	}
}

func (c *P0PaymentsConsumer) applyRefund(ctx context.Context, env *events.EventEnvelope, p paymentPayload) error {
	// A refund event is keyed on the intent, because payments can refund an
	// intent commerce did not initiate.
	if p.ID == "" {
		return nil
	}
	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		// Not an order reference (a food refund, say). Nothing to do.
		return nil
	}
	if err := c.store.SettleRefund(ctx, orderID, p.ID); err != nil {
		return fmt.Errorf("settle refund for order %s: %w", orderID, err)
	}
	slog.Info("commerce: refund settled", "order_id", orderID, "intent_id", p.ID)
	return nil
}
