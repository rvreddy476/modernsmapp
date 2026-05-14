package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
)

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
	PayerID        uuid.UUID
	PayeeID        uuid.UUID
	ReferenceType  string
	ReferenceID    uuid.UUID
	Amount         float64
	Currency       string
	Method         string
	IdempotencyKey string
}

func (s *Service) InitiatePayment(ctx context.Context, in InitiateInput) (*postgres.PaymentIntent, error) {
	if in.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
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
		order, err := s.gateway.CreateOrder(ctx, int64(in.Amount), "INR", in.IdempotencyKey)
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
		Amount:         in.Amount,
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
		if holdErr := s.store.CreateHold(ctx, res.Intent.ID, int64(in.Amount), orDefault(in.Currency, "INR"), "order_delivered"); holdErr != nil {
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
var ErrRefundNotAuthorized = fmt.Errorf("not authorized to refund this payment")

// ErrHoldReleaseNotAuthorized is the equivalent for escrow holds.
// Audit P4: ReleaseHold previously took an arbitrary X-User-Id string
// and skipped any ownership check, so a buyer could release the
// seller's escrow before delivery.
var ErrHoldReleaseNotAuthorized = fmt.Errorf("not authorized to release this hold")

// InitiateRefund refunds a succeeded payment.
//
// Authorization (audit P1): the actor must be either the payer (buyer
// initiating a chargeback), the payee (seller-initiated refund), or
// match the audit-explicit X-Admin-Refund flag from a trusted internal
// caller. Cross-user refunds previously worked because the only check
// was "status == succeeded".
func (s *Service) InitiateRefund(ctx context.Context, id, actorID uuid.UUID, reason string) (*postgres.PaymentIntent, error) {
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if intent.Status != "succeeded" {
		return nil, fmt.Errorf("can only refund succeeded payments, current status: %s", intent.Status)
	}
	if actorID != intent.PayerID && actorID != intent.PayeeID {
		return nil, ErrRefundNotAuthorized
	}
	if err := s.store.UpdateStatus(ctx, id, "succeeded", "refunded", "", actorID); err != nil {
		return nil, err
	}
	intent.Status = "refunded"
	s.publishEvent(ctx, "payment.refunded", actorID.String(), map[string]any{
		"intent_id": id,
		"reason":    reason,
	})
	return intent, nil
}

func (s *Service) ListByReference(ctx context.Context, refType string, refID uuid.UUID) ([]postgres.PaymentIntent, error) {
	return s.store.ListByReference(ctx, refType, refID)
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
	s.publishEvent(ctx, eventType, "", map[string]any{
		"provider_ref": providerRef,
		"payment_id":   paymentID,
		"new_status":   newStatus,
	})
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
