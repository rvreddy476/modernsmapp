package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// statusPublisher is the part of the producer the consumer needs: a status
// change made here must reach search and the other readers of
// community.updated, or an archived community stays listed there.
type statusPublisher interface {
	PublishCommunityUpdated(ctx context.Context, communityID, actorID uuid.UUID, name, communityType, status string) error
}

type Consumer struct {
	reader    *kafka.Reader
	store     *store.Store
	rdb       *redis.Client
	publisher statusPublisher
}

// WithPublisher sets where status changes are announced. Without one an
// archive still happens and is logged as unannounced.
func (c *Consumer) WithPublisher(p statusPublisher) *Consumer {
	c.publisher = p
	return c
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
	slog.Info("community-service consumer listening on platform-events")
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

	// Find communities where user is the sole owner — archive them
	orphanedCommunities, err := c.store.ListCommunitiesWhereUserIsOnlyOwner(ctx, userID)
	if err != nil {
		slog.Warn("failed to find orphaned communities", "user_id", userID, "error", err)
	} else {
		for _, communityID := range orphanedCommunities {
			if err := c.store.ArchiveCommunity(ctx, communityID); err != nil {
				slog.Warn("failed to archive orphaned community", "community_id", communityID, "error", err)
				continue
			}
			c.announceStatus(ctx, communityID, userID)
		}
	}

	// Remove user from all communities
	if err := c.store.RemoveUserFromAll(ctx, userID); err != nil {
		slog.Warn("failed to remove user from all communities", "user_id", userID, "error", err)
	}

	slog.Info("GDPR deletion completed for user", "user_id", userID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}

// announceStatus publishes community.updated with the community's status after
// a status change made here (the GDPR archive). Before, the archive emitted
// nothing, so search kept listing a community whose pages answer 404.
func (c *Consumer) announceStatus(ctx context.Context, communityID, actorID uuid.UUID) {
	if c.publisher == nil {
		slog.Warn("community archived without a community.updated event: no publisher", "community_id", communityID)
		return
	}
	comm, err := c.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		slog.Warn("archived community could not be re-read for its event", "community_id", communityID, "error", err)
		return
	}
	if err := c.publisher.PublishCommunityUpdated(ctx, communityID, actorID, comm.Name, comm.CommunityType, comm.Status); err != nil {
		slog.Warn("failed to publish community.updated for an archived community", "community_id", communityID, "error", err)
	}
}
