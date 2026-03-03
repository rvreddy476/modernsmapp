package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/facebook-like/payments-service/internal/store/postgres"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
)

type Service struct {
	store  *postgres.Store
	writer *kafka.Writer
}

func New(store *postgres.Store, kafkaBrokers string) *Service {
	return &Service{
		store: store,
		writer: &kafka.Writer{
			Addr:     kafka.TCP(kafkaBrokers),
			Topic:    "social.events.v1",
			Balancer: &kafka.LeastBytes{},
		},
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

	intent, err := s.store.CreateIntent(ctx, postgres.PaymentIntent{
		PayerID:        in.PayerID,
		PayeeID:        in.PayeeID,
		ReferenceType:  in.ReferenceType,
		ReferenceID:    in.ReferenceID,
		Amount:         in.Amount,
		Currency:       orDefault(in.Currency, "INR"),
		Method:         in.Method,
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	return intent, nil
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

func (s *Service) publishEvent(ctx context.Context, eventType, key string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal event", "event_type", eventType, "error", err)
		return
	}
	if err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: data,
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
