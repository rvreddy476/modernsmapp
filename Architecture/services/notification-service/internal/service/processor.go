package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/google/uuid"
)

// NotificationEvent is the unified input for the template-based notification pipeline.
// Kafka consumers build this struct from event payloads and hand it to ProcessNotificationEvent.
type NotificationEvent struct {
	EventType   string            `json:"event_type"`
	RecipientID string            `json:"recipient_id"`
	ActorID     string            `json:"actor_id"`
	ActorName   string            `json:"actor_name"`
	TargetID    string            `json:"target_id"`   // post_id, group_id, etc.
	TargetType  string            `json:"target_type"`  // post, group, channel, community
	DeepLink    string            `json:"deep_link"`
	Vars        map[string]string `json:"vars"`         // template variables (actor, post_preview, group, etc.)
	Timestamp   time.Time         `json:"timestamp"`
}

// ProcessNotificationEvent is the main entry point for the template-based notification pipeline.
// It resolves the template, attempts aggregation, and either updates an existing notification
// or creates a new one in ScyllaDB.
func (s *Service) ProcessNotificationEvent(ctx context.Context, event NotificationEvent) error {
	tmpl := GetTemplate(event.EventType)

	// Ensure actor name is available in vars for title rendering.
	if event.Vars == nil {
		event.Vars = make(map[string]string)
	}
	if _, ok := event.Vars["actor"]; !ok && event.ActorName != "" {
		event.Vars["actor"] = event.ActorName
	}

	// Step 1: Try aggregation
	shouldCreateNew, existingNotifID, newCount := TryAggregate(ctx, s.rdb, event.RecipientID, event.EventType, event.TargetID, event.ActorID)

	if !shouldCreateNew && existingNotifID != "" {
		// Aggregated — publish updated title over Redis pub/sub so connected clients see it.
		if tmpl.AggregateTitle != "" {
			aggTitle := RenderAggregateTitle(tmpl.AggregateTitle, newCount, event.Vars)
			channel := fmt.Sprintf("notify:%s", event.RecipientID)
			payload, _ := json.Marshal(map[string]interface{}{
				"type": "notification_update",
				"payload": map[string]interface{}{
					"notification_id": existingNotifID,
					"title":           aggTitle,
					"count":           newCount,
				},
			})
			if err := s.rdb.Publish(ctx, channel, payload).Err(); err != nil {
				slog.Warn("processor: failed to publish aggregation update", "error", err)
			}
		}
		// No new row — badge delta only.
		return nil
	}

	// Step 2: Parse IDs
	recipientUUID, err := uuid.Parse(event.RecipientID)
	if err != nil {
		return fmt.Errorf("processor: invalid recipient_id %q: %w", event.RecipientID, err)
	}
	actorUUID, _ := uuid.Parse(event.ActorID)
	entityUUID, _ := uuid.Parse(event.TargetID)

	// Step 3: Create new notification in ScyllaDB via the existing store
	notifID := uuid.New()
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	n := &scylla.Notification{
		UserID:         recipientUUID,
		NotificationID: notifID,
		Type:           event.EventType,
		ActorUserID:    actorUUID,
		EntityType:     event.TargetType,
		EntityID:       entityUUID,
		DeepLink:       event.DeepLink,
		IsRead:         false,
		CreatedAt:      ts,
	}

	if err := s.scyllaStore.CreateNotification(ctx, n); err != nil {
		slog.Error("processor: failed to create notification", "error", err)
		return fmt.Errorf("processor: scylla write failed: %w", err)
	}

	// Step 4: Start aggregation window
	StartAggregation(ctx, s.rdb, event.RecipientID, event.EventType, event.TargetID, event.ActorID, notifID.String())

	// Step 5: Increment unread counter
	_ = IncrementUnread(ctx, s.rdb, "notification", event.RecipientID, "", event.RecipientID)

	// Step 6: Publish to Redis pub/sub for real-time delivery
	title := RenderTitle(tmpl.TitleTemplate, event.Vars)
	body := ""
	if tmpl.BodyTemplate != "" {
		body = RenderTitle(tmpl.BodyTemplate, event.Vars)
	}

	channel := fmt.Sprintf("notify:%s", event.RecipientID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "notification",
		"payload": map[string]interface{}{
			"notification_id": notifID.String(),
			"event_type":      event.EventType,
			"title":           title,
			"body":            body,
			"icon":            tmpl.Icon,
			"priority":        tmpl.Priority,
			"deep_link":       event.DeepLink,
			"actor_id":        event.ActorID,
			"actor_name":      event.ActorName,
			"created_at":      ts,
		},
	})
	if err := s.rdb.Publish(ctx, channel, payload).Err(); err != nil {
		slog.Warn("processor: failed to publish notification", "error", err)
	}

	// Step 7: Send push notification if eligible
	if tmpl.PushEligible && s.pusher != nil && s.pgStore != nil {
		s.sendTemplatePush(ctx, recipientUUID, tmpl, title, body, event)
	}

	slog.Info("notification created",
		"id", notifID.String(),
		"type", event.EventType,
		"recipient", event.RecipientID,
		"priority", tmpl.Priority,
	)

	return nil
}

// sendTemplatePush sends a push notification, respecting user preferences and quiet hours
// unless the template overrides them.
func (s *Service) sendTemplatePush(ctx context.Context, recipientID uuid.UUID, tmpl NotificationTemplate, title, body string, event NotificationEvent) {
	tokens, err := s.pgStore.GetUserDevices(ctx, recipientID)
	if err != nil || len(tokens) == 0 {
		return
	}

	prefs, _ := s.pgStore.GetPreferences(ctx, recipientID)
	if prefs != nil && !tmpl.OverridePrefs {
		if !prefs.PushEnabled {
			return
		}
		quietStart := ""
		quietEnd := ""
		if prefs.QuietHoursStart != nil {
			quietStart = *prefs.QuietHoursStart
		}
		if prefs.QuietHoursEnd != nil {
			quietEnd = *prefs.QuietHoursEnd
		}
		if !tmpl.OverrideMute && isQuietHours(quietStart, quietEnd) {
			return
		}
	}

	data := map[string]string{
		"type":      event.EventType,
		"deep_link": event.DeepLink,
		"icon":      tmpl.Icon,
		"priority":  tmpl.Priority,
	}
	for _, t := range tokens {
		if err := s.pusher.Send(ctx, t.PushToken, t.Platform, title, body, data); err != nil {
			slog.Warn("processor: push send failed", "error", err, "platform", t.Platform)
		}
	}
}
