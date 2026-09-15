package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/google/uuid"
)

// SafetyAlert is one safety notification (Dating plan lane D8): a panic page
// to a responder or trusted contact, or a live-location share. Title and
// Body are the push copy; they must never contain coordinates unless the
// user opted in to share them with this recipient.
type SafetyAlert struct {
	Recipient  uuid.UUID
	Actor      uuid.UUID
	NotifType  string
	EntityType string
	EntityID   uuid.UUID
	DeepLink   string
	Title      string
	Body       string
	CreatedAt  time.Time
	// Identity is the stable per-recipient key ("dating_panic:<id>:contact:<user>")
	// that makes a redelivered event a no-op.
	Identity string
}

// CreateSafetyAlertNotification delivers a safety alert on every channel.
// Safety alerts ignore category preferences, mutes and quiet hours: the only
// thing that stops one is an account suppression (deactivated or deletion
// scheduled). Delivery is idempotent on Identity: a second call finds the
// inbox row and sends nothing.
func (s *Service) CreateSafetyAlertNotification(ctx context.Context, a SafetyAlert) error {
	if a.Recipient == uuid.Nil || a.Identity == "" || a.NotifType == "" {
		return fmt.Errorf("safety alert needs a recipient, a type and an identity")
	}
	if s.recipientSuppressed(ctx, a.Recipient, a.NotifType) {
		return nil
	}
	createdAt := a.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	n := &scylla.Notification{
		UserID:         a.Recipient,
		NotificationID: scylla.DeterministicNotificationID(a.Identity),
		TS:             scylla.DeterministicTS(createdAt, a.Identity),
		Type:           a.NotifType,
		ActorUserID:    a.Actor,
		EntityType:     a.EntityType,
		EntityID:       a.EntityID,
		DeepLink:       a.DeepLink,
		CreatedAt:      createdAt,
	}
	if s.scyllaStore == nil {
		return fmt.Errorf("safety alert: inbox store not configured")
	}
	applied, err := s.scyllaStore.CreateNotificationIfNotExists(ctx, n)
	if err != nil {
		return fmt.Errorf("safety alert inbox write: %w", err)
	}
	if !applied {
		return nil
	}
	if s.rdb != nil {
		payload, _ := json.Marshal(map[string]any{"type": "notification", "payload": n})
		if err := s.rdb.Publish(ctx, fmt.Sprintf("notify:%s", a.Recipient), payload).Err(); err != nil {
			slog.Warn("safety alert realtime publish failed", "type", a.NotifType, "error", err)
		}
	}
	if s.pusher == nil || s.pgStore == nil {
		slog.Warn("safety alert stored but push is not configured", "type", a.NotifType, "entity_id", a.EntityID)
		return nil
	}
	tokens, err := s.pgStore.GetUserDevices(ctx, a.Recipient)
	if err != nil {
		return fmt.Errorf("safety alert device lookup: %w", err)
	}
	data := map[string]string{
		"type":         a.NotifType,
		"entity_id":    a.EntityID.String(),
		"deep_link":    a.DeepLink,
		"collapse_key": a.Identity,
	}
	for _, t := range tokens {
		if err := s.pusher.Send(ctx, t.PushToken, t.Platform, a.Title, a.Body, data); err != nil {
			slog.Warn("safety alert push failed", "type", a.NotifType, "platform", t.Platform, "error", err)
		}
	}
	return nil
}

// ClaimEventDedup records a processed event id; true exactly once per id.
// Without Postgres every claim succeeds (dev/test posture).
func (s *Service) ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error) {
	if s.pgStore == nil {
		return true, nil
	}
	return s.pgStore.ClaimEventDedup(ctx, id)
}

// RecordOpsAlert writes an operator alert row.
func (s *Service) RecordOpsAlert(ctx context.Context, a postgres.OpsAlert) error {
	if s.pgStore == nil {
		return fmt.Errorf("ops alert not stored: postgres not configured")
	}
	_, err := s.pgStore.InsertOpsAlert(ctx, a)
	return err
}
