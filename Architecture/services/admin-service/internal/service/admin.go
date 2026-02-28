package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/facebook-like/admin-service/internal/store/postgres"
	"github.com/facebook-like/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Service struct {
	store       *postgres.Store
	kafkaWriter *kafka.Writer
}

func New(store *postgres.Store, kafkaBrokers string) *Service {
	w := &kafka.Writer{
		Addr:     kafka.TCP(kafkaBrokers),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
	}
	return &Service{store: store, kafkaWriter: w}
}

// TakedownContent
func (s *Service) TakedownContent(ctx context.Context, actor string, entityType, entityID, reason string) error {
	// 1. Audit Log
	if err := s.store.LogAction(ctx, actor, "TAKEDOWN", entityType, entityID, map[string]string{"reason": reason}); err != nil {
		return fmt.Errorf("audit log failed: %w", err)
	}

	// 2. Emit ContentTakenDown event
	payload := events.ContentTakenDownPayload{
		EntityType: entityType,
		EntityID:   entityID,
		Reason:     reason,
		AdminID:    actor,
		DeletedAt:  time.Now(),
	}
	return s.emitEvent(ctx, events.ContentTakenDown, entityID, payload)
}

// SuspendUser
func (s *Service) SuspendUser(ctx context.Context, actor string, userID uuid.UUID, until time.Time, reason string) error {
	// 1. Store Suspension
	if err := s.store.SuspendUser(ctx, userID, until, reason); err != nil {
		return fmt.Errorf("db failed: %w", err)
	}

	// 2. Audit Log
	if err := s.store.LogAction(ctx, actor, "SUSPEND_USER", "user", userID.String(), map[string]interface{}{"until": until, "reason": reason}); err != nil {
		// Non-fatal but bad
		fmt.Printf("Audit log failed: %v\n", err)
	}

	// 3. Emit UserSuspended event
	payload := events.UserSuspendedPayload{
		UserID:      userID.String(),
		Until:       until,
		Reason:      reason,
		AdminID:     actor,
		SuspendedAt: time.Now(),
	}
	return s.emitEvent(ctx, events.UserSuspended, userID.String(), payload)
}

func (s *Service) emitEvent(ctx context.Context, eventType, key string, payload interface{}) error {
	pBytes, _ := json.Marshal(payload)
	envelope := events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		Payload:    pBytes,
		OccurredAt: time.Now(),
	}
	eBytes, _ := json.Marshal(envelope)

	return s.kafkaWriter.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: eBytes,
	})
}
