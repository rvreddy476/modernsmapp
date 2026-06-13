package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
)

// rupeesToPaise converts the API's rupees-major amount to the paise-minor
// units Razorpay (and other Indian payment gateways) require. `math.Round`
// pins ₹100.50 to 10050 paise rather than 10049 from float-truncation —
// reconciliation breaks otherwise. The API contract at the HTTP boundary
// is rupees-major: every site that ships an amount across that boundary
// must stay in that unit.
func rupeesToPaise(amountRupees float64) int64 {
	return int64(math.Round(amountRupees * 100))
}

type Service struct {
	store   *postgres.Store
	writer  *kafka.Writer
	gateway gateway.PaymentGateway
}

func New(store *postgres.Store, kafkaBrokers string, gw gateway.PaymentGateway) *Service {
	return NewWithDialer(store, kafkaBrokers, gw, nil)
}

func NewWithDialer(store *postgres.Store, kafkaBrokers string, gw gateway.PaymentGateway, dialer *kafka.Dialer) *Service {
	return &Service{
		store: store,
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  strings.Split(kafkaBrokers, ","),
			Topic:    "social.events.v1",
			Balancer: &kafka.LeastBytes{},
			Dialer:   dialer,
		}),
		gateway: gw,
	}
}

type InitiateInput struct {
	PayerID       uuid.UUID
	PayeeID       uuid.UUID
	ReferenceType string
	ReferenceID   uuid.UUID
	// Amount is the legacy rupees-major float entry point. Audit
	// P7-deep: new callers should populate AmountMinor (paise-minor
	// int64) directly. When only Amount is set, the service computes
	// AmountMinor via rupeesToPaise. When both are set, AmountMinor
	// wins (the float copy is kept on the row for the deprecated
	// `amount` column).
	Amount         float64
	AmountMinor    int64
	Currency       string
	Method         string
	IdempotencyKey string
}

func (s *Service) InitiatePayment(ctx context.Context, in InitiateInput) (*postgres.PaymentIntent, error) {
	// Audit P7-deep: AmountMinor (paise-minor int64) is the new source
	// of truth. Resolve it once at the entry point — every downstream
	// reference (gateway CreateOrder, CreateHold, the row's
	// AmountMinorRaw column) uses the resolved int64. The legacy
	// rupees-major float copy is preserved on the row only because the
	// deprecated `amount` NUMERIC column still has analytics readers;
	// it is NEVER used for arithmetic downstream of this resolution.
	amountMinor := in.AmountMinor
	if amountMinor == 0 {
		if in.Amount <= 0 {
			return nil, fmt.Errorf("amount must be positive")
		}
		amountMinor = rupeesToPaise(in.Amount)
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	// Keep the float mirror in sync with whichever side the caller
	// supplied so the deprecated `amount` column is never inconsistent
	// with `amount_minor`.
	amountRupees := in.Amount
	if amountRupees <= 0 {
		amountRupees = float64(amountMinor) / 100.0
	}

	validMethods := map[string]bool{"upi": true, "card": true, "wallet": true, "cod": true, "escrow": true}
	if !validMethods[in.Method] {
		return nil, fmt.Errorf("invalid payment method: %s", in.Method)
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = uuid.New().String()
	}

	var providerRef string
	if in.Method != "cod" && in.Method != "wallet" && s.gateway != nil {
		// Razorpay's CreateOrder expects paise-minor. amountMinor is
		// already the resolved paise value — no float math at this
		// boundary (audit P7-deep).
		order, err := s.gateway.CreateOrder(ctx, amountMinor, "INR", in.IdempotencyKey)
		if err != nil {
			slog.Error("payment: gateway CreateOrder failed", "error", err)
		} else {
			providerRef = order.ID
		}
	}

	res, err := s.store.CreateIntent(ctx, postgres.PaymentIntent{
		PayerID:        in.PayerID,
		PayeeID:        in.PayeeID,
		ReferenceType:  in.ReferenceType,
		ReferenceID:    in.ReferenceID,
		Amount:         amountRupees,
		AmountMinorRaw: amountMinor,
		Currency:       orDefault(in.Currency, "INR"),
		Method:         in.Method,
		ProviderRef:    providerRef,
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}

	if res.WasExisting {
		return res.Intent, nil
	}

	if in.Method == "escrow" {
		// Holds are recorded in paise-minor so they line up with the
		// provider's amount; amountMinor is the resolved paise value.
		if holdErr := s.store.CreateHold(ctx, res.Intent.ID, amountMinor, orDefault(in.Currency, "INR"), "order_delivered"); holdErr != nil {
			slog.Error("payment: CreateHold failed", "intent_id", res.Intent.ID, "error", holdErr)
		}
	}

	s.publishEvent(ctx, "payment.initiated", res.Intent.PayerID.String(), res.Intent)
	return res.Intent, nil
}

func (s *Service) GetIntent(ctx context.Context, id uuid.UUID) (*postgres.PaymentIntent, error) {
	return s.store.GetIntent(ctx, id)
}

func (s *Service) UpdateStatus(ctx context.Context, id uuid.UUID, oldStatus, newStatus, providerRef string, actorID uuid.UUID) (*postgres.PaymentIntent, error) {
	if err := s.store.UpdateStatus(ctx, id, oldStatus, newStatus, providerRef, actorID); err != nil {
		return nil, err
	}
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	eventType := "payment.status_changed"
	if newStatus == "succeeded" {
		eventType = "payment.succeeded"
	} else if newStatus == "failed" {
		eventType = "payment.failed"
	}
	s.publishEvent(ctx, eventType, actorID.String(), intent)
	return intent, nil
}

// ErrRefundNotAuthorized is returned when the actor isn't entitled to
// refund this intent. Audit P1: previously InitiateRefund did no
// authorization check — any caller with X-User-Id could refund any
// payment in the table, moving money out of any seller's account.
// Errors surfaced by VerifyIntent so HTTP callers can distinguish a bad
// signature (which must produce a 401/400, never a 200) from a missing
// intent or a misconfigured gateway.
var (
	ErrSignatureVerificationFailed = fmt.Errorf("razorpay signature verification failed")
	ErrAmountMismatch              = fmt.Errorf("payment amount does not match intent")
	ErrProviderRefMismatch         = fmt.Errorf("razorpay order id does not match intent")
	ErrGatewayNotConfigured        = fmt.Errorf("payment gateway not configured")
)

var ErrRefundNotAuthorized = fmt.Errorf("not authorized to refund this payment")

// ErrRefundAmountExceedsIntent is returned when the caller asks to
// refund more than the intent's outstanding refundable balance. Audit
// P6: previously the InitiateRefund signature took no amount and
// blanket-flipped status to 'refunded', so commerce-service computing a
// per-line return refund worth ₹X on an order paid as ₹Y > X would
// refund the entire ₹Y. The amount cap is now enforced both at the
// service layer (with this error) and atomically at the DB layer (the
// store's WHERE clause).
var ErrRefundAmountExceedsIntent = fmt.Errorf("refund amount exceeds intent")

// ErrHoldReleaseNotAuthorized is the equivalent for escrow holds.
// Audit P4: ReleaseHold previously took an arbitrary X-User-Id string
// and skipped any ownership check, so a buyer could release the
// seller's escrow before delivery.
var ErrHoldReleaseNotAuthorized = fmt.Errorf("not authorized to release this hold")

// resolveRefundAmount validates an InitiateRefund request against the
// stored intent and returns the paise-minor refund amount the store
// should apply, plus the intent's total amount in paise-minor (the
// store uses the latter as the upper bound for its atomic UPDATE).
//
// Extracted as a pure function so the audit P6 + P7 validation —
// status check, ownership check, amount cap, full-vs-partial selection —
// is unit-testable without a real Postgres pool. Errors map 1:1 to the
// surfaceable Err* constants InitiateRefund returns.
func resolveRefundAmount(intent *postgres.PaymentIntent, actorID uuid.UUID, amountMinor int64) (refundMinor, intentAmountMinor int64, err error) {
	// Allow refunds on succeeded (first refund) and partially_refunded
	// (subsequent partial top-ups until fully refunded).
	if intent.Status != "succeeded" && intent.Status != "partially_refunded" {
		return 0, 0, fmt.Errorf("can only refund succeeded payments, current status: %s", intent.Status)
	}
	if actorID != intent.PayerID && actorID != intent.PayeeID {
		return 0, 0, ErrRefundNotAuthorized
	}

	intentAmountMinor = intent.AmountMinor()
	remaining := intentAmountMinor - intent.RefundedAmountMinor
	if remaining <= 0 {
		return 0, 0, fmt.Errorf("intent already fully refunded")
	}

	// amountMinor == 0 means "refund the whole remaining balance". This
	// preserves the historical semantics of the no-amount signature.
	refundMinor = amountMinor
	if refundMinor == 0 {
		refundMinor = remaining
	}
	if refundMinor < 0 {
		return 0, 0, fmt.Errorf("refund amount must be non-negative")
	}
	if refundMinor > remaining {
		return 0, 0, ErrRefundAmountExceedsIntent
	}
	return refundMinor, intentAmountMinor, nil
}

// computeRefundStatus mirrors the CASE inside store.ApplyRefund so the
// state machine can be unit-tested without a DB. If applying `refundMinor`
// brings the running total up to (or above) `intentAmountMinor`, the
// status flips to 'refunded' (full); otherwise 'partially_refunded'.
//
// Kept as a sibling helper rather than a method on PaymentIntent to
// emphasise it's the projection of the SQL CASE expression — change one,
// change both.
func computeRefundStatus(currentRefundedMinor, refundMinor, intentAmountMinor int64) string {
	if currentRefundedMinor+refundMinor >= intentAmountMinor {
		return "refunded"
	}
	return "partially_refunded"
}

// InitiateRefund refunds a succeeded payment.
//
// Authorization (audit P1): the actor must be either the payer (buyer
// initiating a chargeback) or the payee (seller-initiated refund).
// Cross-user refunds previously worked because the only check was
// "status == succeeded".
//
// Amount (audit P6 + P7): amountMinor is paise-minor int64.
//   - amountMinor == 0 → full refund of (intent_amount_minor - already_refunded).
//   - amountMinor > remaining refundable → ErrRefundAmountExceedsIntent.
//   - amountMinor == remaining → status flips to 'refunded'.
//   - amountMinor <  remaining → status flips to 'partially_refunded';
//     subsequent refunds accumulate in refunded_amount_minor.
//
// Razorpay is called with the same paise amount (its refund API supports
// partial refunds — https://razorpay.com/docs/api/refunds/create-instant/).
// The gateway call is best-effort with a slog.Error: a successful DB
// transition without a successful provider call still publishes the
// payment.refunded Kafka event because commerce-service's refund
// consumer needs to flip its return row regardless of provider status
// (the gateway leg can be reconciled separately). The webhook is the
// canonical signal for provider settlement.
func (s *Service) InitiateRefund(ctx context.Context, id, actorID uuid.UUID, amountMinor int64, reason string) (*postgres.PaymentIntent, error) {
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	refundMinor, intentAmountMinor, err := resolveRefundAmount(intent, actorID, amountMinor)
	if err != nil {
		return nil, err
	}

	newStatus, newRefundedTotal, err := s.store.ApplyRefund(ctx, id, refundMinor, intentAmountMinor, actorID)
	if err != nil {
		return nil, err
	}

	// Best-effort gateway leg. Razorpay's refund API accepts an
	// `amount` in paise; passing it tells the provider this is a partial
	// refund. If the provider call fails we still return success on the
	// DB transition because the commerce-side bookkeeping needs to move
	// forward; provider settlement is reconciled via webhook.
	if s.gateway != nil && intent.ProviderRef != "" && intent.Method != "cod" && intent.Method != "wallet" {
		if _, gwErr := s.gateway.InitiateRefund(ctx, intent.ProviderRef, refundMinor); gwErr != nil {
			slog.Error("payment: gateway InitiateRefund failed",
				"intent_id", id, "amount_minor", refundMinor, "error", gwErr)
		}
	}

	intent.Status = newStatus
	intent.RefundedAmountMinor = newRefundedTotal
	// Publish the full intent so commerce-service's refund consumer can
	// locate the order via reference_type / reference_id and mark the
	// return refund. The same event type is emitted for both partial
	// and full refunds; consumers branch on status if needed.
	s.publishEvent(ctx, "payment.refunded", actorID.String(), intent)
	return intent, nil
}

func (s *Service) ListByReference(ctx context.Context, refType string, refID uuid.UUID) ([]postgres.PaymentIntent, error) {
	return s.store.ListByReference(ctx, refType, refID)
}

// VerifyResult is returned by VerifyIntent. Verified is only ever true
// when the signature, the provider order id, and (when supplied) the
// amount all match the stored intent.
type VerifyResult struct {
	Verified    bool      `json:"verified"`
	IntentID    uuid.UUID `json:"intent_id"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	ProviderRef string    `json:"provider_ref"`
}

// VerifyIntent is the synchronous gateway-verification path commerce-service
// uses to confirm a payment immediately after the customer completes Razorpay
// checkout. The webhook remains the canonical async signal; this exists so
// the order page can transition without waiting for webhook delivery.
//
// Checks, in order:
//  1. The intent's stored provider_ref matches the razorpay_order_id the
//     client returned (no cross-order replay).
//  2. The supplied amount_minor (if > 0) matches the intent amount in paise
//     (prevents a low-amount payment confirming a high-amount order).
//  3. The Razorpay signature HMAC-verifies for (order_id|payment_id).
//
// On success the intent is transitioned pending → succeeded if it is still
// pending — idempotent, the state-machine rejects re-applying succeeded.
// The webhook still publishes the canonical payment.succeeded event; verify
// does not double-publish.
func (s *Service) VerifyIntent(ctx context.Context, id uuid.UUID, rzpOrderID, rzpPaymentID, rzpSignature string, amountMinor int64) (*VerifyResult, error) {
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.gateway == nil {
		return nil, ErrGatewayNotConfigured
	}

	// Audit P7-deep: AmountMinor() reads the new int64 column; the
	// float fallback only fires for legacy pre-migration rows.
	intentAmountMinor := intent.AmountMinor()

	if intent.ProviderRef == "" || intent.ProviderRef != rzpOrderID {
		return nil, ErrProviderRefMismatch
	}
	if amountMinor > 0 && amountMinor != intentAmountMinor {
		return nil, ErrAmountMismatch
	}
	if !s.gateway.VerifySignature(rzpOrderID, rzpPaymentID, rzpSignature) {
		return nil, ErrSignatureVerificationFailed
	}

	// Idempotent transition. If the webhook beat us, the state-machine
	// returns ErrInvalidStatusTransition and we leave the intent as-is.
	_ = s.store.UpdateStatus(ctx, id, "pending", "succeeded", rzpPaymentID, intent.PayerID)
	current, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	return &VerifyResult{
		Verified:    true,
		IntentID:    id,
		Status:      current.Status,
		AmountMinor: intentAmountMinor,
		ProviderRef: current.ProviderRef,
	}, nil
}

// ReleaseHold marks an escrow hold released. Audit P4: actor must
// match the payee (seller — the party the hold is protecting) or the
// payer (buyer — for buyer-initiated escrow release flows). Empty or
// non-UUID actor strings are rejected.
func (s *Service) ReleaseHold(ctx context.Context, intentID uuid.UUID, releasedBy string) error {
	intent, err := s.store.GetIntent(ctx, intentID)
	if err != nil {
		return err
	}
	actor, err := uuid.Parse(releasedBy)
	if err != nil {
		return ErrHoldReleaseNotAuthorized
	}
	if actor != intent.PayeeID && actor != intent.PayerID {
		return ErrHoldReleaseNotAuthorized
	}
	return s.store.ReleaseHold(ctx, intentID, releasedBy)
}

// MarkWebhookSeen is the dedup gate the webhook handler calls before
// applying the status update. Audit P3: returns (fresh=true) the first
// time an event_id arrives and (false) on every retry, so duplicate
// deliveries from Razorpay don't re-publish Kafka events.
func (s *Service) MarkWebhookSeen(ctx context.Context, eventID, eventType, providerRef string) (bool, error) {
	return s.store.RecordWebhookEventIfNew(ctx, eventID, eventType, providerRef)
}

// UpdateStatusByProviderRef is invoked from the webhook handler. The
// state machine inside the store rejects forbidden transitions (audit
// P2), so a late payment.captured arriving after refund.processed no
// longer reverts the status. We publish a Kafka event only when the
// transition actually applied — the store returns
// ErrInvalidStatusTransition / ErrPaymentNotFound for the no-op cases.
func (s *Service) UpdateStatusByProviderRef(ctx context.Context, providerRef, newStatus, paymentID string) {
	err := s.store.UpdateStatusByProviderRef(ctx, providerRef, newStatus, paymentID)
	if err != nil {
		// Quiet log for the expected no-op cases; loud for everything else.
		if errors.Is(err, postgres.ErrInvalidStatusTransition) || errors.Is(err, postgres.ErrPaymentNotFound) {
			slog.Info("payment: webhook status update skipped",
				"provider_ref", providerRef, "new_status", newStatus, "reason", err.Error())
			return
		}
		slog.Error("payment: UpdateStatusByProviderRef failed", "provider_ref", providerRef, "error", err)
		return
	}
	// Publish only when the row actually changed.
	eventType := "payment.status_changed"
	switch newStatus {
	case "succeeded":
		eventType = "payment.succeeded"
	case "failed":
		eventType = "payment.failed"
	case "refunded":
		eventType = "payment.refunded"
	}
	// Look up the intent so the event payload carries reference_type +
	// reference_id, matching the shape consumers expect. Falls back to
	// the legacy bare-keys payload only on a lookup miss so a transient
	// DB blip can't silently swallow the event.
	if intent, lookupErr := s.store.GetIntentByProviderRef(ctx, providerRef); lookupErr == nil && intent != nil {
		s.publishEvent(ctx, eventType, "", intent)
		return
	}
	s.publishEvent(ctx, eventType, "", map[string]any{
		"provider_ref": providerRef,
		"payment_id":   paymentID,
		"new_status":   newStatus,
	})
}

// ApplyWebhookRefund settles a refund.processed webhook from Razorpay.
//
// Closes the follow-up the partial-refund commit flagged: the old
// webhook arm did UpdateStatusByProviderRef(providerRef, "refunded"),
// which (a) ignored the refund amount entirely so partials got booked
// as fulls, and (b) tried partially_refunded → refunded
// unconditionally — the post-P6 state machine correctly refuses that
// when refunded_amount_minor hasn't caught up.
//
// Idempotency:
//   - Refund-level: store.RecordRefundIfFresh INSERTs the
//     refund_provider_ref ON CONFLICT DO NOTHING. A retry returns
//     "not fresh" and we short-circuit without re-applying. The
//     existing webhook_events dedup only catches identical event_ids;
//     Razorpay can re-deliver the same refund with a new event_id.
//   - DB-level: ApplyRefund's `refunded_amount_minor + $2 <= $3`
//     WHERE clause is the second line of defense — if dedup somehow
//     misses, the cap still refuses to oversubscribe.
//
// Best-effort logging on failures so the webhook handler can still
// 200 — Razorpay's retry loop will redeliver on a 5xx, but a refund
// that genuinely can't be booked (intent not found, amount overflows
// cap) is a permanent failure that re-trying doesn't fix.
func (s *Service) ApplyWebhookRefund(ctx context.Context, paymentProviderRef, refundProviderRef string, amountMinor int64) {
	if amountMinor <= 0 {
		slog.Warn("webhook refund: skipping zero/negative amount",
			"refund_id", refundProviderRef, "amount_minor", amountMinor)
		return
	}
	intent, err := s.store.GetIntentByProviderRef(ctx, paymentProviderRef)
	if err != nil || intent == nil {
		slog.Warn("webhook refund: intent not found for provider_ref",
			"payment_id", paymentProviderRef, "refund_id", refundProviderRef, "error", err)
		return
	}

	fresh, err := s.store.RecordRefundIfFresh(ctx, refundProviderRef, intent.ID, amountMinor)
	if err != nil {
		slog.Error("webhook refund: dedup record failed",
			"intent_id", intent.ID, "refund_id", refundProviderRef, "error", err)
		return
	}
	if !fresh {
		slog.Info("webhook refund: replay skipped",
			"intent_id", intent.ID, "refund_id", refundProviderRef)
		return
	}

	intentAmountMinor := intent.AmountMinor()
	if amountMinor > intentAmountMinor-intent.RefundedAmountMinor {
		// Refund exceeds the remaining balance — the cap WHERE clause
		// in ApplyRefund will refuse, but we log the impossible case
		// loudly. A legitimate over-refund means provider/local state
		// drifted; needs an ops investigation rather than a retry.
		slog.Error("webhook refund: amount exceeds remaining intent balance",
			"intent_id", intent.ID, "refund_id", refundProviderRef,
			"requested_minor", amountMinor,
			"remaining_minor", intentAmountMinor-intent.RefundedAmountMinor)
		return
	}

	newStatus, newRefundedTotal, err := s.store.ApplyRefund(ctx, intent.ID, amountMinor, intentAmountMinor, uuid.Nil)
	if err != nil {
		slog.Error("webhook refund: ApplyRefund failed",
			"intent_id", intent.ID, "refund_id", refundProviderRef, "error", err)
		return
	}

	intent.Status = newStatus
	intent.RefundedAmountMinor = newRefundedTotal
	s.publishEvent(ctx, "payment.refunded", "", intent)
	slog.Info("webhook refund: applied",
		"intent_id", intent.ID, "refund_id", refundProviderRef,
		"amount_minor", amountMinor, "new_status", newStatus,
		"refunded_total_minor", newRefundedTotal)
}

func (s *Service) publishEvent(ctx context.Context, eventType, key string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal event", "event_type", eventType, "error", err)
		return
	}
	// Wrap the payload in the shared EventEnvelope so consumers using the
	// shared/kafka.Consumer can decode it. The actor (key) goes into
	// ActorUserID when it parses as a UUID; otherwise it's left nil.
	envelope := events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		OccurredAt: time.Now().UTC(),
		Payload:    data,
	}
	if key != "" {
		if _, err := uuid.Parse(key); err == nil {
			k := key
			envelope.ActorUserID = &k
		}
	}
	value, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("failed to marshal envelope", "event_type", eventType, "error", err)
		return
	}
	if err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:     []byte(key),
		Value:   value,
		Headers: []kafka.Header{{Key: "event_type", Value: []byte(eventType)}},
	}); err != nil {
		slog.Error("failed to publish event", "event_type", eventType, "error", err)
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
