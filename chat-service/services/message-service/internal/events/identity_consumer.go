package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Identity event types (defined locally to avoid cross-module dependency)
const (
	identityUserRegistered     = "UserRegistered"
	identityUserProfileUpdated = "UserProfileUpdated"
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

// ProfileUpserter is the interface for upserting user profiles.
type ProfileUpserter interface {
	UpsertUserProfile(ctx context.Context, userID uuid.UUID, displayName string, avatarMediaID *uuid.UUID) error
}

// IdentityConsumer consumes identity events from Kafka and maintains a local profile cache.
type IdentityConsumer struct {
	reader  *kafka.Reader
	store   ProfileUpserter
	log     *slog.Logger
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
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				c.log.Info("identity consumer context closed")
				return
			}
			c.log.Error("error reading identity message", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var envelope identityEnvelope
		if err := json.Unmarshal(m.Value, &envelope); err != nil {
			c.log.Warn("error unmarshalling identity event", "err", err)
			continue
		}

		switch envelope.EventType {
		case identityUserRegistered:
			c.handleUserRegistered(ctx, envelope.Payload)
		case identityUserProfileUpdated:
			c.handleUserProfileUpdated(ctx, envelope.Payload)
		}
	}
}

func (c *IdentityConsumer) handleUserRegistered(ctx context.Context, payload json.RawMessage) {
	var p userRegisteredPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling user registered payload", "err", err)
		return
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		c.log.Warn("invalid user id in user registered event", "user_id", p.UserID)
		return
	}

	displayName := p.FirstName + " " + p.LastName
	if displayName == " " || displayName == "" {
		displayName = "User " + userID.String()[:8]
	}

	if err := c.store.UpsertUserProfile(ctx, userID, displayName, nil); err != nil {
		c.log.Error("failed to upsert user profile from registration", "err", err, "user_id", userID)
	} else {
		c.log.Info("cached user profile from registration", "user_id", userID)
	}
}

func (c *IdentityConsumer) handleUserProfileUpdated(ctx context.Context, payload json.RawMessage) {
	var p userProfileUpdatedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling user profile updated payload", "err", err)
		return
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		c.log.Warn("invalid user id in profile updated event", "user_id", p.UserID)
		return
	}

	var avatarMediaID *uuid.UUID
	if p.AvatarMediaID != nil {
		if parsed, err := uuid.Parse(*p.AvatarMediaID); err == nil {
			avatarMediaID = &parsed
		}
	}

	if err := c.store.UpsertUserProfile(ctx, userID, p.DisplayName, avatarMediaID); err != nil {
		c.log.Error("failed to upsert user profile from update", "err", err, "user_id", userID)
	} else {
		c.log.Info("cached user profile from update", "user_id", userID)
	}
}
