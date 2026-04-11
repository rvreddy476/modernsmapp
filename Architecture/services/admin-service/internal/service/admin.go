package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Service struct {
	store              *postgres.Store
	kafkaWriter        *kafka.Writer
	miniAppSessionAuth MiniAppSessionIssuer
}

func New(store *postgres.Store, kafkaBrokers string, miniAppSessionAuth MiniAppSessionIssuer) *Service {
	return NewWithDialer(store, kafkaBrokers, nil, miniAppSessionAuth)
}

func NewWithDialer(store *postgres.Store, kafkaBrokers string, dialer *kafka.Dialer, miniAppSessionAuth MiniAppSessionIssuer) *Service {
	brokers := strings.Split(kafkaBrokers, ",")
	cfg := kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
	}
	if dialer != nil {
		cfg.Dialer = dialer
	}
	w := kafka.NewWriter(cfg)
	return &Service{
		store:              store,
		kafkaWriter:        w,
		miniAppSessionAuth: miniAppSessionAuth,
	}
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

// GetDashboard returns aggregate stats for the admin dashboard.
func (s *Service) GetDashboard(ctx context.Context) (*postgres.DashboardStats, error) {
	return s.store.GetDashboardStats(ctx)
}

// GetAuditLogs returns paginated audit log entries.
func (s *Service) GetAuditLogs(ctx context.Context, limit, offset int) ([]postgres.AuditLog, int, error) {
	return s.store.GetAuditLogs(ctx, limit, offset)
}

// ListSuspensions returns paginated active suspensions.
func (s *Service) ListSuspensions(ctx context.Context, limit, offset int) ([]postgres.Suspension, int, error) {
	return s.store.GetSuspensions(ctx, limit, offset)
}

// UnsuspendUser removes a user's suspension and logs the action.
func (s *Service) UnsuspendUser(ctx context.Context, actor string, userID uuid.UUID) error {
	if err := s.store.UnsuspendUser(ctx, userID); err != nil {
		return fmt.Errorf("unsuspend failed: %w", err)
	}

	// Audit log
	if err := s.store.LogAction(ctx, actor, "UNSUSPEND_USER", "user", userID.String(), nil); err != nil {
		fmt.Printf("Audit log failed: %v\n", err)
	}

	// Emit UserUnsuspended event
	payload := events.UserUnsuspendedPayload{
		UserID:        userID.String(),
		AdminID:       actor,
		UnsuspendedAt: time.Now(),
	}
	return s.emitEvent(ctx, events.UserUnsuspended, userID.String(), payload)
}

// ListReports returns paginated reports, optionally filtered by status.
func (s *Service) ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.Report, int, error) {
	return s.store.GetReports(ctx, status, limit, offset)
}

func (s *Service) emitEvent(ctx context.Context, eventType, key string, payload interface{}) error {
	pBytes, _ := json.Marshal(payload)
	envelope := events.NewEnvelope(ctx, eventType, nil, pBytes)
	eBytes, _ := json.Marshal(envelope)

	return s.kafkaWriter.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: eBytes,
	})
}
