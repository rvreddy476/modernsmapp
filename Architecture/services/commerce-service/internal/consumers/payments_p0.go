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
//
// Decoding and type dispatch are shared/paymentevents; the inbox, the money
// check (paymentevents.CheckCapture) and the effect stay in the store.
// payment.refund_failed is not consumed here: commerce's own refund worker
// owns the command's fate.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// P0PaymentsConsumer applies payment events durably.
type P0PaymentsConsumer struct {
	paymentevents.NopHandler
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

func (c *P0PaymentsConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.EventPaymentSucceeded, events.EventPaymentFailed, events.EventPaymentRefunded,
		events.EventPaymentRefundFailed, postgres.EventPaymentOwedCapture:
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

	var err error
	if env.EventType == postgres.EventPaymentOwedCapture {
		// Not one of the shared payment types: payments-service's own
		// account of a payment it is returning (owed_captures.go).
		err = applyOwedCaptureWith(ctx, c.store, c.obs, env)
	} else {
		err = paymentevents.Dispatch(ctx, env, c)
	}
	if errors.Is(err, paymentevents.ErrMalformed) {
		// A malformed payload will never parse, so retrying forever is
		// pointless — but it must be visible, not silently dropped the way
		// the old handler dropped it with a Warn and a nil return.
		slog.Error("commerce: unparseable payment payload; sending to DLQ",
			"event_type", env.EventType, "event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable payment event %s", env.EventID))
	}
	return err
}

// OnSucceeded applies payment.succeeded.
func (c *P0PaymentsConsumer) OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Succeeded) error {
	offer := SucceededOffer(env)
	captured := SucceededCaptured(env)
	return c.applyPayment(ctx, env, ev.Payment, func(ctx context.Context, e postgres.PaymentEvent) error {
		e.Offer = offer
		e.CapturedMinor = captured
		return c.store.ApplyPaymentSucceeded(ctx, e)
	})
}

// SucceededCaptured reads captured_minor, the money the provider captured,
// from a payment.succeeded payload. payments-service states it on a bank-offer
// capture (the only capture whose money differs from amount_minor). nil when
// the payload does not state it or it does not decode; the amount check never
// uses it.
func SucceededCaptured(env *events.EventEnvelope) *money.Paise {
	if env == nil || len(env.Payload) == 0 {
		return nil
	}
	var k struct {
		CapturedMinor *int64 `json:"captured_minor"`
	}
	if err := json.Unmarshal(env.Payload, &k); err != nil || k.CapturedMinor == nil || *k.CapturedMinor <= 0 {
		return nil
	}
	p := money.Paise(*k.CapturedMinor)
	return &p
}

// OnRefundFailed records that payments-service parked a refund (an order's
// own refund command, or an owed payment's refund), so the buyer is told it
// is delayed. No money and no command state changes.
func (c *P0PaymentsConsumer) OnRefundFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.RefundFailed) error {
	return applyRefundFailedWith(ctx, c.store, c.obs, env, ev)
}

// refundFailedRecorder is the store half of OnRefundFailed.
type refundFailedRecorder interface {
	ApplyRefundFailed(ctx context.Context, ev postgres.RefundFailedEvent) error
}

func applyRefundFailedWith(ctx context.Context, store refundFailedRecorder, obs Observer, env *events.EventEnvelope,
	p paymentevents.RefundFailed) error {
	if p.ReferenceType != "order" && p.ReferenceType != "refund_required" {
		return nil
	}
	intent := p.IntentID
	if intent == "" {
		intent = p.ID
	}
	err := store.ApplyRefundFailed(ctx, postgres.RefundFailedEvent{
		EventID: env.EventID, ReferenceType: p.ReferenceType, ReferenceID: p.ReferenceID,
		IntentID: intent, AmountMinor: p.AmountMinor, ReasonCode: p.ReasonCode,
	})
	switch {
	case err == nil:
		if obs != nil {
			obs.Applied(env.EventType)
		}
		slog.Warn("commerce: a refund is parked at payments-service; the buyer is told it is delayed",
			"reference_type", p.ReferenceType, "reference_id", p.ReferenceID, "intent_id", intent,
			"reason_code", p.ReasonCode, "event_id", env.EventID)
		return nil
	case errors.Is(err, postgres.ErrDuplicatePaymentEvt):
		if obs != nil {
			obs.DuplicateSuppressed(env.EventID)
		}
		return nil
	default:
		return fmt.Errorf("record refund failure %s: %w", env.EventID, err)
	}
}

// owedCaptureRecorder is the store half of payment.owed_capture.
type owedCaptureRecorder interface {
	ApplyOwedCapture(ctx context.Context, ev postgres.OwedCaptureEvent) error
}

// owedCapturePayload is payment.owed_capture's payload as commerce reads it.
type owedCapturePayload struct {
	ID               string    `json:"id"`
	IntentID         string    `json:"intent_id"`
	RefundRequiredID string    `json:"refund_required_id"`
	Reason           string    `json:"reason"`
	AmountMinor      int64     `json:"amount_minor"`
	Currency         string    `json:"currency"`
	RefundedMinor    int64     `json:"refunded_minor"`
	Resolved         bool      `json:"resolved"`
	DetectedAt       time.Time `json:"detected_at"`
	ReferenceType    string    `json:"reference_type"`
	ReferenceID      string    `json:"reference_id"`
}

// applyOwedCaptureWith records payment.owed_capture for a commerce order. A
// payload that does not decode, or names no row, is poison (DLQ, paged); an
// order commerce does not have is poison too.
func applyOwedCaptureWith(ctx context.Context, store owedCaptureRecorder, obs Observer, env *events.EventEnvelope) error {
	var p owedCapturePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return fmt.Errorf("%w: %s event %s: %v", paymentevents.ErrMalformed, env.EventType, env.EventID, err)
	}
	if p.ReferenceType != "order" {
		return nil // another domain's payment
	}
	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		slog.Warn("commerce: owed-capture event has an unparseable order reference", "reference_id", p.ReferenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable owed-capture event %s", env.EventID))
	}
	rowID, err := strconv.ParseInt(p.RefundRequiredID, 10, 64)
	if err != nil || rowID <= 0 {
		slog.Error("commerce: owed-capture event names no refund_required row", "event_id", env.EventID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable owed-capture event %s", env.EventID))
	}
	intent := p.IntentID
	if intent == "" {
		intent = p.ID
	}
	err = store.ApplyOwedCapture(ctx, postgres.OwedCaptureEvent{
		EventID: env.EventID, OrderID: orderID, IntentID: intent, RefundRequiredID: rowID,
		Reason: p.Reason, AmountMinor: p.AmountMinor, Currency: p.Currency,
		RefundedMinor: p.RefundedMinor, Resolved: p.Resolved, DetectedAt: p.DetectedAt,
	})
	switch {
	case err == nil:
		if obs != nil {
			obs.Applied(env.EventType)
		}
		return nil
	case errors.Is(err, postgres.ErrDuplicatePaymentEvt):
		if obs != nil {
			obs.DuplicateSuppressed(env.EventID)
		}
		return nil
	case errors.Is(err, postgres.ErrOrderNotFoundP0), errors.Is(err, postgres.ErrAmountMismatch):
		slog.Error("commerce: owed-capture event cannot be recorded", "order_id", orderID,
			"event_id", env.EventID, "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable owed-capture event %s", env.EventID))
	default:
		return fmt.Errorf("record owed capture for order %s: %w", orderID, err)
	}
}

// succeededOfferKeys are the keys payments-service adds to payment.succeeded
// for a capture accepted through a registered bank offer (its migration 014).
// amount_minor stays the order value; these say what the buyer actually paid.
type succeededOfferKeys struct {
	CapturedMinor      *int64 `json:"captured_minor"`
	OfferID            string `json:"offer_id"`
	OfferTitle         string `json:"offer_title"`
	OfferDiscountMinor *int64 `json:"offer_discount_minor"`
	OfferFundedBy      string `json:"offer_funded_by"`
}

// SucceededOffer reads the bank-offer keys from a payment.succeeded payload,
// or nil when the payment used no offer. It decodes the payload a second time
// rather than widening shared/paymentevents, which food-service also reads.
// A payload whose offer keys do not decode is treated as carrying no offer:
// the capture check has already decided the payment, and the offer is
// display.
func SucceededOffer(env *events.EventEnvelope) *postgres.PaymentOffer {
	if env == nil || len(env.Payload) == 0 {
		return nil
	}
	var k succeededOfferKeys
	if err := json.Unmarshal(env.Payload, &k); err != nil {
		return nil
	}
	if k.OfferID == "" || k.CapturedMinor == nil || k.OfferDiscountMinor == nil {
		return nil
	}
	return &postgres.PaymentOffer{
		OfferID:       k.OfferID,
		Title:         k.OfferTitle,
		DiscountMinor: money.Paise(*k.OfferDiscountMinor),
		FundedBy:      k.OfferFundedBy,
		CapturedMinor: money.Paise(*k.CapturedMinor),
	}
}

// OnFailed applies payment.failed.
func (c *P0PaymentsConsumer) OnFailed(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Failed) error {
	return c.applyPayment(ctx, env, ev.Payment, c.store.ApplyPaymentFailed)
}

// OnRefunded settles a refund.
func (c *P0PaymentsConsumer) OnRefunded(ctx context.Context, env *events.EventEnvelope, ev paymentevents.Refunded) error {
	return c.applyRefund(ctx, env, ev)
}

func (c *P0PaymentsConsumer) applyPayment(ctx context.Context, env *events.EventEnvelope, p paymentevents.Payment,
	apply func(context.Context, postgres.PaymentEvent) error) error {
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
		AmountMinor: money.Paise(p.AmountMinor),
		Currency:    p.Currency,
		PayerID:     payerID,
		ProviderRef: p.ProviderRef,
	}

	err = apply(ctx, ev)
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

// refundSettler is the store half of applyRefund, an interface so the
// filter and error mapping can be tested without a database.
type refundSettler interface {
	SettleRefund(ctx context.Context, ev postgres.RefundEvent) (postgres.RefundSettlement, error)
}

// applyRefund settles payment.refunded (audit 2026-10-30 M2).
//
// The previous version's only filter was "reference_id parses as a UUID", so
// every food, dating, rider, doorstep and membership refund (all UUID
// references) ran a settlement against a non-existent order id. It now acts
// only on reference_type "order", like applyPayment, and the store checks the
// refund with paymentevents.CheckRefund, records its amount, decides full
// versus partial from the cumulative sum and dedupes by event and refund id.
func (c *P0PaymentsConsumer) applyRefund(ctx context.Context, env *events.EventEnvelope, p paymentevents.Refunded) error {
	return applyRefundWith(ctx, c.store, c.obs, env, p)
}

func applyRefundWith(ctx context.Context, store refundSettler, obs Observer, env *events.EventEnvelope, p paymentevents.Refunded) error {
	if p.ReferenceType != "order" { // M2: commerce orders only
		return nil
	}
	orderID, err := uuid.Parse(p.ReferenceID)
	if err != nil {
		slog.Warn("commerce: refund event has an unparseable order reference",
			"reference_id", p.ReferenceID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable refund event %s", env.EventID))
	}
	ev := postgres.RefundEvent{
		EventID:        env.EventID,
		OrderID:        orderID,
		IntentID:       p.Intent(),
		RefundID:       p.ProviderRefundID,
		AmountMinor:    money.Paise(p.AmountMinor),
		ProviderStatus: p.Status,
		ReturnedMinor:  refundedReturned(env),
	}
	res, err := store.SettleRefund(ctx, ev)
	switch {
	case err == nil:
		if res.Duplicate {
			if obs != nil {
				obs.DuplicateSuppressed(env.EventID)
			}
			return nil
		}
		if obs != nil {
			obs.Applied(env.EventType)
		}
		slog.Info("commerce: refund settled", "order_id", orderID, "intent_id", ev.IntentID,
			"refund_minor", ev.AmountMinor.Int64(), "refunded_total_minor", res.RefundedTotal.Int64(),
			"fully_refunded", res.Full, "payment_status", res.PaymentStatus)
		return nil
	case errors.Is(err, postgres.ErrDuplicatePaymentEvt):
		if obs != nil {
			obs.DuplicateSuppressed(env.EventID)
		}
		return nil
	case errors.Is(err, postgres.ErrAmountMismatch):
		if obs != nil {
			obs.AmountMismatch(orderID, ev.AmountMinor.Int64(), 0)
		}
		slog.Error("commerce: REFUND MISMATCH — refund not applied to the order",
			"order_id", orderID, "event_id", env.EventID,
			"refund_minor", ev.AmountMinor.Int64(), "error", err)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable refund event %s", env.EventID))
	case errors.Is(err, postgres.ErrOrderNotFoundP0):
		slog.Error("commerce: refund event references an unknown order",
			"order_id", orderID, "event_id", env.EventID)
		return sharedkafka.Permanent(fmt.Errorf("unprocessable refund event %s", env.EventID))
	default:
		return fmt.Errorf("settle refund for order %s: %w", orderID, err)
	}
}

// refundedReturned reads amount_returned_minor (the money returned, below
// amount_minor on a bank-offer intent) from a payment.refunded payload; nil
// when absent or unreadable.
func refundedReturned(env *events.EventEnvelope) *int64 {
	if env == nil || len(env.Payload) == 0 {
		return nil
	}
	var k struct {
		Returned *int64 `json:"amount_returned_minor"`
	}
	if err := json.Unmarshal(env.Payload, &k); err != nil || k.Returned == nil || *k.Returned <= 0 {
		return nil
	}
	return k.Returned
}
