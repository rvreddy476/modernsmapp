package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// PublishOutboxEvents polls the outbox table and publishes pending events to Kafka.
// This should be called periodically (e.g., every 5 seconds).
func (s *Service) PublishOutboxEvents(ctx context.Context) error {
	if s.producer == nil {
		return nil
	}

	outboxEvents, err := s.pgStore.GetUnpublishedOutboxEvents(ctx, 100)
	if err != nil {
		return err
	}

	for _, evt := range outboxEvents {
		envelope := events.NewEnvelope(ctx, evt.EventType, nil, evt.Payload)
		if err := s.producer.PublishRaw(ctx, envelope); err != nil {
			slog.Error("outbox: failed to publish event",
				"event_id", evt.ID, "event_type", evt.EventType, "error", err)
			continue
		}

		if err := s.pgStore.MarkOutboxEventPublished(ctx, evt.ID); err != nil {
			slog.Error("outbox: failed to mark event published",
				"event_id", evt.ID, "error", err)
		}
	}

	return nil
}

// InsertOutboxEvent inserts a reel lifecycle event into the outbox.
func (s *Service) InsertOutboxEvent(ctx context.Context, eventType, aggregateType string, aggregateID uuid.UUID, payload interface{}) error {
	return s.pgStore.InsertOutboxEvent(ctx, eventType, aggregateType, aggregateID, payload)
}

// StartOutboxWorker starts a background goroutine that publishes outbox events.
func (s *Service) StartOutboxWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		cleanupTicker := time.NewTicker(1 * time.Hour)
		defer cleanupTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.PublishOutboxEvents(ctx); err != nil {
					slog.Error("outbox worker: publish error", "error", err)
				}
			case <-cleanupTicker.C:
				// Clean up old published events (retain 48 hours)
				if count, err := s.pgStore.CleanupOldOutboxEvents(ctx, 48); err != nil {
					slog.Error("outbox worker: cleanup error", "error", err)
				} else if count > 0 {
					slog.Info("outbox worker: cleaned up old events", "count", count)
				}

				// Clean up expired idempotency keys
				if count, err := s.pgStore.CleanupExpiredIdempotencyKeys(ctx); err != nil {
					slog.Error("outbox worker: idempotency cleanup error", "error", err)
				} else if count > 0 {
					slog.Info("outbox worker: cleaned up expired idempotency keys", "count", count)
				}
			}
		}
	}()
}

// ─── Reel Lifecycle Event Helpers ───────────────────────────────────

// EmitReelDraftCreated emits a ReelDraftCreated event via the outbox.
func (s *Service) EmitReelDraftCreated(ctx context.Context, draftID, authorID uuid.UUID) {
	payload := map[string]string{
		"draft_id":  draftID.String(),
		"author_id": authorID.String(),
	}
	if err := s.InsertOutboxEvent(ctx, events.ReelDraftCreated, "draft", draftID, payload); err != nil {
		slog.Error("emit ReelDraftCreated failed", "draft_id", draftID, "error", err)
	}
}

// EmitReelPublishRequested emits a ReelPublishRequested event via the outbox.
func (s *Service) EmitReelPublishRequested(ctx context.Context, reelID, authorID uuid.UUID) {
	payload := map[string]string{
		"reel_id":   reelID.String(),
		"author_id": authorID.String(),
	}
	if err := s.InsertOutboxEvent(ctx, events.ReelPublishRequested, "reel", reelID, payload); err != nil {
		slog.Error("emit ReelPublishRequested failed", "reel_id", reelID, "error", err)
	}
}

// EmitReelPublished emits a ReelPublished event via the outbox.
func (s *Service) EmitReelPublished(ctx context.Context, reelID, authorID uuid.UUID, caption string, hashtags []string) {
	payload := struct {
		ReelID   string   `json:"reel_id"`
		AuthorID string   `json:"author_id"`
		Caption  string   `json:"caption"`
		Hashtags []string `json:"hashtags"`
	}{
		ReelID:   reelID.String(),
		AuthorID: authorID.String(),
		Caption:  caption,
		Hashtags: hashtags,
	}
	if err := s.InsertOutboxEvent(ctx, events.ReelPublished, "reel", reelID, payload); err != nil {
		slog.Error("emit ReelPublished failed", "reel_id", reelID, "error", err)
	}
}

// EmitReelDeleted emits a ReelDeleted event via the outbox.
func (s *Service) EmitReelDeleted(ctx context.Context, reelID, authorID uuid.UUID) {
	payload := map[string]string{
		"reel_id":   reelID.String(),
		"author_id": authorID.String(),
	}
	if err := s.InsertOutboxEvent(ctx, events.ReelDeleted, "reel", reelID, payload); err != nil {
		slog.Error("emit ReelDeleted failed", "reel_id", reelID, "error", err)
	}
}

// ─── Reel Viewed (for analytics) ───────────────────────────────────

// ReelViewedPayload is the event payload for reel view tracking.
type ReelViewedPayload struct {
	ReelID    string `json:"reel_id"`
	ViewerID  string `json:"viewer_id"`
	SessionID string `json:"session_id"`
	WatchedMs int64  `json:"watched_ms"`
	Surface   string `json:"surface"`
}

// EmitReelViewed publishes a reel view event directly (not outbox — high volume).
func (s *Service) EmitReelViewed(ctx context.Context, payload ReelViewedPayload) {
	if s.producer == nil {
		return
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}
	envelope := events.NewEnvelope(ctx, events.ReelViewed, &payload.ViewerID, payloadBytes)
	if err := s.producer.PublishRaw(ctx, envelope); err != nil {
		slog.Error("emit ReelViewed failed", "reel_id", payload.ReelID, "error", err)
	}
}
