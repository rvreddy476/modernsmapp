package events

import (
	"context"
	"encoding/json"
	"errors"
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

// Start deliberately blocks the fetched partition until every deletion
// effect is durable. Because each effect is idempotent, a crash after any
// subset is safe: Kafka redelivers the message and the full sequence repeats.
func (c *Consumer) Start(ctx context.Context) {
	slog.Info("group-service consumer listening on platform-events")
	for {
		message, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("consumer shutting down")
				return
			}
			slog.Error("consumer fetch error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if err := c.handleUntilDurable(ctx, message); err != nil {
			return
		}
		if err := c.reader.CommitMessages(ctx, message); err != nil {
			slog.Error("consumer commit error", "error", err)
		}
	}
}

func (c *Consumer) handleUntilDurable(ctx context.Context, message kafka.Message) error {
	for {
		permanent, err := c.processMessage(ctx, message)
		if err == nil || permanent {
			if permanent && err != nil {
				slog.Warn("discarding permanently invalid group event", "error", err)
			}
			return nil
		}
		slog.Error("group event not durable; partition remains blocked", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, message kafka.Message) (bool, error) {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(message.Value, &envelope); err != nil {
		return true, err
	}
	if envelope.EventType != events.EventUserDeletionRequested {
		return true, nil
	}
	if err := c.handleUserDeletionRequested(ctx, envelope); err != nil {
		var invalid invalidDeletionEvent
		return errors.As(err, &invalid), err
	}
	return false, nil
}

type invalidDeletionEvent struct{ error }

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserDeletionRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return invalidDeletionEvent{err}
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return invalidDeletionEvent{err}
	}

	slog.Info("processing GDPR deletion for user", "user_id", userID)
	orphanedGroups, err := c.store.ListGroupsWhereUserIsOnlyAdmin(ctx, userID)
	if err != nil {
		return err
	}
	for _, groupID := range orphanedGroups {
		if err := c.store.ArchiveGroup(ctx, groupID); err != nil {
			return err
		}
	}
	if err := c.store.RemoveUserFromAllGroups(ctx, userID); err != nil {
		return err
	}
	if err := c.store.CancelUserInvites(ctx, userID); err != nil {
		return err
	}
	if err := c.store.CancelUserJoinRequests(ctx, userID); err != nil {
		return err
	}
	slog.Info("GDPR deletion completed for user", "user_id", userID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
