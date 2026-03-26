package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
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

func (s *Service) InitiateRefund(ctx context.Context, id, actorID uuid.UUID, reason string) (*postgres.PaymentIntent, error) {
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if intent.Status != "succeeded" {
		return nil, fmt.Errorf("can only refund succeeded payments, current status: %s", intent.Status)
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

func (s *Service) ReleaseHold(ctx context.Context, intentID uuid.UUID, releasedBy string) error {
	return s.store.ReleaseHold(ctx, intentID, releasedBy)
}

func (s *Service) UpdateStatusByProviderRef(ctx context.Context, providerRef, newStatus, paymentID string) {
	if err := s.store.UpdateStatusByProviderRef(ctx, providerRef, newStatus, paymentID); err != nil {
		slog.Error("payment: UpdateStatusByProviderRef failed", "provider_ref", providerRef, "error", err)
	}
}

func (s *Service) publishEvent(ctx context.Context, eventType, key string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal event", "event_type", eventType, "error", err)
		return
	}
	if err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:     []byte(key),
		Value:   data,
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
