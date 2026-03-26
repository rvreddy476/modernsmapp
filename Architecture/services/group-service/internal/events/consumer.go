package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/group-service/internal/store"
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
	slog.Info("group-service consumer listening on platform-events")
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

	slog.Info("processing GDPR deletion for user", "user_id", userID)

	// Find groups where user is the sole admin — archive them
	orphanedGroups, err := c.store.ListGroupsWhereUserIsOnlyAdmin(ctx, userID)
	if err != nil {
		slog.Warn("failed to find orphaned groups", "user_id", userID, "error", err)
	} else {
		for _, groupID := range orphanedGroups {
			if err := c.store.ArchiveGroup(ctx, groupID); err != nil {
				slog.Warn("failed to archive orphaned group", "group_id", groupID, "error", err)
			}
		}
	}

	// Remove user from all groups
	if err := c.store.RemoveUserFromAllGroups(ctx, userID); err != nil {
		slog.Warn("failed to remove user from all groups", "user_id", userID, "error", err)
	}

	// Cancel pending invites
	if err := c.store.CancelUserInvites(ctx, userID); err != nil {
		slog.Warn("failed to cancel user invites", "user_id", userID, "error", err)
	}

	// Cancel pending join requests
	if err := c.store.CancelUserJoinRequests(ctx, userID); err != nil {
		slog.Warn("failed to cancel user join requests", "user_id", userID, "error", err)
	}

	slog.Info("GDPR deletion completed for user", "user_id", userID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
