package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/chat-message-service/internal/purge"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Identity event types (defined locally to avoid cross-module dependency)
const (
	identityUserRegistered     = "UserRegistered"
	identityUserProfileUpdated = "UserProfileUpdated"
	// identityUserSettingsChanged invalidates the local chat-policy
	// projection so pause/typing/receipt changes take effect on the next
	// hot-path read instead of the 5-minute TTL (production chat pass §5.1).
	identityUserSettingsChanged = "user.settings_changed"
)

type identityEnvelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type userRegisteredPayload struct {
	UserID    string `json:"user_id"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type userProfileUpdatedPayload struct {
	UserID        string  `json:"user_id"`
	DisplayName   string  `json:"display_name"`
	AvatarMediaID *string `json:"avatar_media_id,omitempty"`
}

// ProfileUpserter is the interface for upserting user profiles and
// invalidating the chat policy projection (production chat pass §5.1).
type ProfileUpserter interface {
	UpsertUserProfile(ctx context.Context, userID uuid.UUID, displayName string, avatarMediaID *uuid.UUID) error
	InvalidateUserPolicy(ctx context.Context, userID uuid.UUID) error
}

// IdentityConsumer consumes identity events from Kafka and maintains a local profile cache.
type IdentityConsumer struct {
	reader *kafka.Reader
	store  ProfileUpserter
	log    *slog.Logger
	// lifecycle handles account control (hide / unhide / purge + ack).
	// Optional; see internal/purge.
	lifecycle *purge.Handler
}

// WithLifecycleHandler wires the account-control handler.
func (c *IdentityConsumer) WithLifecycleHandler(h *purge.Handler) *IdentityConsumer {
	c.lifecycle = h
	return c
}

func NewIdentityConsumer(brokers []string, topic, groupID string, store ProfileUpserter, logger *slog.Logger) *IdentityConsumer {
	return NewIdentityConsumerWithDialer(brokers, topic, groupID, nil, store, logger)
}

func NewIdentityConsumerWithDialer(brokers []string, topic, groupID string, dialer *kafka.Dialer, store ProfileUpserter, logger *slog.Logger) *IdentityConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
		Dialer:  dialer,
	})
	return &IdentityConsumer{reader: r, store: store, log: logger}
}

func (c *IdentityConsumer) Start(ctx context.Context) {
	c.log.Info("starting identity event consumer")
	defer func() {
		if err := c.reader.Close(); err != nil {
			c.log.Warn("failed to close identity kafka reader", "err", err)
		}
	}()
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				c.log.Info("identity consumer context closed")
				return
			}
			c.log.Error("error reading identity message", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if err := c.handleUntilDurable(ctx, m); err != nil {
			return
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			c.log.Error("failed to commit durable identity event", "err", err)
		}
	}
}

func (c *IdentityConsumer) handleUntilDurable(ctx context.Context, message kafka.Message) error {
	for {
		err := c.processMessage(ctx, message)
		if err == nil {
			return nil
		}
		var poison permanentEventError
		if errors.As(err, &poison) {
			c.log.Warn("discarding permanently invalid identity event", "err", err)
			return nil
		}
		c.log.Error("identity event not durable; partition remains blocked", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *IdentityConsumer) processMessage(ctx context.Context, message kafka.Message) error {
	var envelope identityEnvelope
	if err := json.Unmarshal(message.Value, &envelope); err != nil {
		return permanentEventError{err}
	}
	switch envelope.EventType {
	case identityUserRegistered:
		return c.handleUserRegistered(ctx, envelope.Payload)
	case identityUserProfileUpdated:
		return c.handleUserProfileUpdated(ctx, envelope.Payload)
	case identityUserSettingsChanged:
		return c.handleUserSettingsChanged(ctx, envelope.Payload)
	default:
		if c.lifecycle != nil && purge.Handles(envelope.EventType) {
			// handleUntilDurable already retries in place; a permanent
			// decode failure is poison and must not block the partition.
			err := c.lifecycle.Handle(ctx, envelope.EventType, envelope.Payload)
			if errors.Is(err, purge.ErrPermanent) {
				return permanentEventError{err}
			}
			return err
		}
		return nil
	}
}

// handleUserSettingsChanged drops the projected chat policy so the next
// send/typing/receipt path re-fetches the authoritative snapshot. Idempotent.
func (c *IdentityConsumer) handleUserSettingsChanged(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return permanentEventError{err}
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return permanentEventError{err}
	}
	if err := c.store.InvalidateUserPolicy(ctx, userID); err != nil {
		c.log.Error("failed to invalidate chat policy", "err", err, "user_id", userID)
		return err
	}
	return nil
}

func (c *IdentityConsumer) handleUserRegistered(ctx context.Context, payload json.RawMessage) error {
	var p userRegisteredPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling user registered payload", "err", err)
		return permanentEventError{err}
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		c.log.Warn("invalid user id in user registered event", "user_id", p.UserID)
		return permanentEventError{err}
	}

	displayName := p.FirstName + " " + p.LastName
	if displayName == " " || displayName == "" {
		displayName = "User " + userID.String()[:8]
	}

	if err := c.store.UpsertUserProfile(ctx, userID, displayName, nil); err != nil {
		c.log.Error("failed to upsert user profile from registration", "err", err, "user_id", userID)
		return err
	}
	c.log.Info("cached user profile from registration", "user_id", userID)
	return nil
}

func (c *IdentityConsumer) handleUserProfileUpdated(ctx context.Context, payload json.RawMessage) error {
	var p userProfileUpdatedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling user profile updated payload", "err", err)
		return permanentEventError{err}
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		c.log.Warn("invalid user id in profile updated event", "user_id", p.UserID)
		return permanentEventError{err}
	}

	var avatarMediaID *uuid.UUID
	if p.AvatarMediaID != nil {
		if parsed, err := uuid.Parse(*p.AvatarMediaID); err == nil {
			avatarMediaID = &parsed
		} else {
			return permanentEventError{err}
		}
	}

	if err := c.store.UpsertUserProfile(ctx, userID, p.DisplayName, avatarMediaID); err != nil {
		c.log.Error("failed to upsert user profile from update", "err", err, "user_id", userID)
		return err
	}
	c.log.Info("cached user profile from update", "user_id", userID)
	return nil
}
