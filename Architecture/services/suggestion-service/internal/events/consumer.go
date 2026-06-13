package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/atpost/shared/events"
	"github.com/atpost/suggestion-service/internal/service"
	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// Consumer listens to graph events and updates suggestion state.
type Consumer struct {
	reader *kafka.Reader
	rdb    *redis.Client
	svc    *service.Service
	store  *store.Store
}

// NewConsumer creates a new event consumer.
func NewConsumer(brokers []string, groupID, topic string, rdb *redis.Client, svc *service.Service, st *store.Store) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, topic, rdb, svc, st, nil)
}

// NewConsumerWithDialer creates a new event consumer with an explicit Kafka dialer.
func NewConsumerWithDialer(brokers []string, groupID, topic string, rdb *redis.Client, svc *service.Service, st *store.Store, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &Consumer{reader: reader, rdb: rdb, svc: svc, store: st}
}

// Start begins consuming events. Blocks until context is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	log.Println("[suggestion-consumer] started")
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("[suggestion-consumer] context cancelled, stopping")
				return
			}
			log.Printf("[suggestion-consumer] read error: %v", err)
			continue
		}
		if err := c.processMessage(ctx, m); err != nil {
			log.Printf("[suggestion-consumer] process error: %v", err)
		}
	}
}

// Close closes the consumer.
func (c *Consumer) Close() error {
	return c.reader.Close()
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.ConnectionAccepted:
		return c.handleFriendAccepted(ctx, envelope)
	case events.ConnectionRequested:
		return c.handleFriendRequestSent(ctx, envelope)
	case events.ConnectionDeclined:
		return c.handleFriendDeclined(ctx, envelope)
	case events.ConnectionRemoved:
		return c.handleFriendRemoved(ctx, envelope)
	case events.UserBlocked:
		return c.handleUserBlocked(ctx, envelope)
	case events.UserFollowed:
		return c.handleUserFollowed(ctx, envelope)
	case events.UserUnfollowed:
		return c.handleUserUnfollowed(ctx, envelope)
	case events.UserRegistered:
		return c.handleUserRegistered(ctx, envelope)
	case events.GroupMemberJoined:
		return c.handleGroupJoined(ctx, envelope)
	case events.UserProfileUpdated:
		return c.handleProfileUpdated(ctx, envelope)
	default:
		return nil // ignore unrelated events
	}
}

// ─── Event Handlers ──────────────────────────────────────────

func (c *Consumer) handleFriendAccepted(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.ConnectionAcceptedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	// Invalidate both users' friend caches
	c.invalidateCache(ctx, payload.SenderID, "friend")
	c.invalidateCache(ctx, payload.ReceiverID, "friend")
	log.Printf("[suggestion-consumer] FriendAccepted: invalidated %s, %s", payload.SenderID, payload.ReceiverID)
	return nil
}

func (c *Consumer) handleFriendRequestSent(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.ConnectionRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	// Invalidate sender's cache (candidate now excluded via pending)
	c.invalidateCache(ctx, payload.SenderID, "friend")
	return nil
}

func (c *Consumer) handleFriendDeclined(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.ConnectionDeclinedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	// Create 90-day cooldown: receiver should not see sender as suggestion
	receiverID, err := uuid.Parse(payload.ReceiverID)
	if err != nil {
		return err
	}
	senderID, err := uuid.Parse(payload.SenderID)
	if err != nil {
		return err
	}
	c.svc.CreateCooldownFromEvent(ctx, receiverID, senderID, "decline")

	// Also remove from candidate pools
	c.store.RemoveCandidateForViewer(ctx, receiverID, senderID, "friend")
	c.invalidateCache(ctx, payload.ReceiverID, "friend")
	c.invalidateCache(ctx, payload.SenderID, "friend")

	log.Printf("[suggestion-consumer] FriendDeclined: 90d cooldown for %s→%s", payload.ReceiverID, payload.SenderID)
	return nil
}

func (c *Consumer) handleFriendRemoved(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.ConnectionRemovedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	userA, err := uuid.Parse(payload.UserA)
	if err != nil {
		return err
	}
	userB, err := uuid.Parse(payload.UserB)
	if err != nil {
		return err
	}

	// Create 180-day cooldown for both directions
	c.svc.CreateCooldownFromEvent(ctx, userA, userB, "removed_friend")
	c.svc.CreateCooldownFromEvent(ctx, userB, userA, "removed_friend")

	// Invalidate caches
	c.invalidateCache(ctx, payload.UserA, "friend")
	c.invalidateCache(ctx, payload.UserB, "friend")

	log.Printf("[suggestion-consumer] FriendRemoved: 180d cooldown for %s↔%s", payload.UserA, payload.UserB)
	return nil
}

func (c *Consumer) handleUserBlocked(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserBlockedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	blockerID, err := uuid.Parse(payload.BlockerID)
	if err != nil {
		return err
	}
	blockedID, err := uuid.Parse(payload.BlockedID)
	if err != nil {
		return err
	}

	// Permanent cooldown for both directions
	c.svc.CreateCooldownFromEvent(ctx, blockerID, blockedID, "block")
	c.svc.CreateCooldownFromEvent(ctx, blockedID, blockerID, "block")

	// Remove from candidate pools
	c.store.RemoveCandidateForViewer(ctx, blockerID, blockedID, "friend")
	c.store.RemoveCandidateForViewer(ctx, blockerID, blockedID, "follow")
	c.store.RemoveCandidateForViewer(ctx, blockedID, blockerID, "friend")
	c.store.RemoveCandidateForViewer(ctx, blockedID, blockerID, "follow")

	// Invalidate caches
	c.invalidateCache(ctx, payload.BlockerID, "friend")
	c.invalidateCache(ctx, payload.BlockerID, "follow")
	c.invalidateCache(ctx, payload.BlockedID, "friend")
	c.invalidateCache(ctx, payload.BlockedID, "follow")

	log.Printf("[suggestion-consumer] UserBlocked: permanent cooldown %s↔%s", payload.BlockerID, payload.BlockedID)
	return nil
}

func (c *Consumer) handleUserFollowed(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserFollowedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	followerID, err := uuid.Parse(payload.FollowerID)
	if err != nil {
		return err
	}
	followeeID, err := uuid.Parse(payload.FolloweeID)
	if err != nil {
		return err
	}

	// Check if this creates a mutual follow → add to friend candidates with MUTUAL_FOLLOW reason
	mutualFollows, _ := c.store.BatchCheckMutualFollows(ctx, followerID, []uuid.UUID{followeeID})
	if mutualFollows[followeeID] {
		// Mutual follow detected — invalidate friend caches so they appear as candidates
		c.invalidateCache(ctx, payload.FollowerID, "friend")
		c.invalidateCache(ctx, payload.FolloweeID, "friend")
		log.Printf("[suggestion-consumer] Mutual follow detected: %s↔%s", payload.FollowerID, payload.FolloweeID)
	}

	// Invalidate follow caches
	c.invalidateCache(ctx, payload.FollowerID, "follow")
	return nil
}

func (c *Consumer) handleUserUnfollowed(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserFollowedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	c.invalidateCache(ctx, payload.FollowerID, "follow")
	return nil
}

func (c *Consumer) handleUserRegistered(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserRegisteredPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}

	// Cold-start pipeline: generate initial candidates for new user
	log.Printf("[suggestion-consumer] UserRegistered: triggering cold-start for %s", payload.UserID)

	go func() {
		// Run in background to not block the consumer loop
		if err := c.svc.RunBatchForUser(ctx, userID); err != nil {
			log.Printf("[suggestion-consumer] cold-start batch error for %s: %v", payload.UserID, err)
		} else {
			log.Printf("[suggestion-consumer] cold-start complete for %s", payload.UserID)
		}
	}()

	return nil
}

func (c *Consumer) handleGroupJoined(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.GroupMemberJoinedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	// Joining a group changes the candidate pool — invalidate friend cache
	c.invalidateCache(ctx, payload.UserID, "friend")
	log.Printf("[suggestion-consumer] GroupJoined: invalidated %s", payload.UserID)
	return nil
}

func (c *Consumer) handleProfileUpdated(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserProfileUpdatedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	// Profile update may change community matching — invalidate all caches
	c.invalidateCache(ctx, payload.UserID, "friend")
	c.invalidateCache(ctx, payload.UserID, "follow")
	log.Printf("[suggestion-consumer] ProfileUpdated: invalidated %s", payload.UserID)
	return nil
}

// ─── Cache Invalidation ─────────────────────────────────────

func (c *Consumer) invalidateCache(ctx context.Context, userID string, suggType string) {
	key := fmt.Sprintf("suggestions:%s:%s", userID, suggType)
	c.rdb.Del(ctx, key)
}
