package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// identityEventSettingsChanged is identity user-service's
// user.settings_changed, emitted on every committed PUT /v1/users/me/settings
// with payload {user_id, privacy_version, account_visibility?, occurred_at}.
const identityEventSettingsChanged = "user.settings_changed"

// previewInvalidator is the slice of notification-service this consumer uses.
type previewInvalidator interface {
	InvalidateChatPreview(ctx context.Context, userID uuid.UUID) error
}

// IdentitySettingsConsumer drops the cached show_message_preview of a user
// whose settings changed, so turning message previews off is honoured on the
// next chat push instead of after the cache TTL. Its own consumer group on
// identity.events.v1: the account-lifecycle consumer's lag never delays it,
// and the cache it clears is shared (Redis), so one replica receiving the
// event is enough.
type IdentitySettingsConsumer struct {
	reader  *kafka.Reader
	service previewInvalidator
}

func NewIdentitySettingsConsumer(brokers []string, topic, groupID string, dialer *kafka.Dialer, svc previewInvalidator) *IdentitySettingsConsumer {
	return &IdentitySettingsConsumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers, Topic: topic, GroupID: groupID, Dialer: dialer,
			MinBytes: 1, MaxBytes: 10e6, MaxWait: time.Second,
		}),
		service: svc,
	}
}

func (c *IdentitySettingsConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("identity settings consumer: read failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if err := c.processMessage(ctx, m); err != nil {
			slog.Warn("identity settings consumer: invalidate failed", "error", err)
		}
	}
}

func (c *IdentitySettingsConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var env struct {
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(m.Value, &env); err != nil {
		return nil // undecodable — skip, never block the partition
	}
	if env.EventType != identityEventSettingsChanged {
		return nil
	}
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return nil
	}
	return c.service.InvalidateChatPreview(ctx, userID)
}

func (c *IdentitySettingsConsumer) Close() error { return c.reader.Close() }
