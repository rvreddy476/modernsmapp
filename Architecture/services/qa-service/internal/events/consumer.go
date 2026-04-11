package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader *kafka.Reader
	store  *store.Store
	rdb    *redis.Client
}

func NewConsumer(brokers []string, groupID string, s *store.Store, rdb *redis.Client) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, s, rdb, nil)
}

func NewConsumerWithDialer(brokers []string, groupID string, s *store.Store, rdb *redis.Client, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    "platform-events",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &Consumer{reader: reader, store: s, rdb: rdb}
}

func (c *Consumer) Start(ctx context.Context) {
	slog.Info("qa-service consumer listening on platform-events")
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("consumer shutting down")
				return
			}
			slog.Error("consumer read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.processMessage(ctx, m); err != nil {
			slog.Warn("failed to process message", "error", err)
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.EventUserDeletionRequested:
		return c.handleUserDeletionRequested(ctx, envelope)
	case events.EventCommunityCreated:
		return c.handleCommunityCreated(ctx, envelope)
	case events.EventCommunityUpdated:
		return c.handleCommunityUpdated(ctx, envelope)
	case events.EventCommunityDeleted:
		return c.handleCommunityDeleted(ctx, envelope)
	case events.EventCommunityMemberJoined:
		return c.handleCommunityMemberJoined(ctx, envelope)
	case events.EventCommunityMemberLeft:
		return c.handleCommunityMemberLeft(ctx, envelope)
	case events.EventCommunityMemberBanned:
		return c.handleCommunityMemberBanned(ctx, envelope)
	case events.EventCommunityMemberRoleChanged:
		return c.handleCommunityMemberRoleChanged(ctx, envelope)
	}

	return nil
}

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserDeletionRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}

	slog.Info("processing GDPR deletion for user in qa-service", "user_id", userID)

	if err := c.store.AnonymizeUser(ctx, userID); err != nil {
		slog.Warn("failed to anonymize user in qa-service", "user_id", userID, "error", err)
		return err
	}

	slog.Info("GDPR deletion completed for user in qa-service", "user_id", userID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
