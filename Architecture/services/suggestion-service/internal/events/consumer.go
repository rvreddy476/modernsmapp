package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/suggestion-service/internal/service"
	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// permanentError marks a message that can never succeed no matter how many
// times it is redelivered — a malformed envelope, an unparseable uuid.
//
// The distinction matters because the two failure kinds need OPPOSITE
// handling. A datastore outage must not advance the offset, or the safety
// effect is lost; a message that will never decode must advance it, or one
// corrupt record stalls the partition and every block event queued behind it
// is never applied. Silently committing both (the old behaviour) loses data;
// blocking on both stops the safety pipeline.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return "permanent: " + e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// Consumer listens to graph events and updates suggestion state.
type Consumer struct {
	reader *kafka.Reader
	rdb    *redis.Client
	svc    *service.Service
	store  *store.Store
	// LB-2: recognises a redelivered graph safety event so an at-least-once
	// outbox delivery produces ONE logical effect. See dedupe.go.
	dedupe *GraphEventDeduper
	// CLB-1: how long to wait before retrying a message whose durable effect
	// failed. The offset stays put across the wait.
	retryBackoff time.Duration
	maxBackoff   time.Duration
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
	return &Consumer{
		reader: reader, rdb: rdb, svc: svc, store: st,
		// LB-2: at-least-once outbox delivery makes replay recognition the
		// consumer's job. Redis-backed; a nil client processes everything.
		dedupe:       NewGraphEventDeduper(rdb, 0),
		retryBackoff: time.Second,
		maxBackoff:   30 * time.Second,
	}
}

// Start begins consuming events. Blocks until context is cancelled.
//
// CLB-1 — THE OFFSET IS COMMITTED BY THIS LOOP, AND ONLY AFTER THE EFFECT.
//
// This used to call ReadMessage, which for a group reader commits the offset
// BEFORE handing the message back (vendored kafka-go reader.go:786-800). The
// acknowledgement therefore happened before the handler ran, and logging a
// later error could not cause a retry — a datastore outage silently discarded
// the event. FetchMessage does not commit; CommitMessages below does, and only
// once the durable effect has been written.
func (c *Consumer) Start(ctx context.Context) {
	log.Println("[suggestion-consumer] started")
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("[suggestion-consumer] context cancelled, stopping")
				return
			}
			log.Printf("[suggestion-consumer] fetch error: %v", err)
			continue
		}
		if !c.handleUntilDurable(ctx, m) {
			// Shutting down mid-message. The offset is deliberately left
			// uncommitted so the next owner of this partition redelivers it.
			log.Println("[suggestion-consumer] context cancelled before commit, stopping")
			return
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			// The effect is durable and idempotent, so the redelivery this
			// causes is harmless — it will hit the consumer inbox and do
			// nothing.
			log.Printf("[suggestion-consumer] commit error (event will be redelivered): %v", err)
		}
	}
}

// handleUntilDurable retries a message until its effect is durably applied.
// It reports whether the message may now be committed; false means the context
// was cancelled first and the offset must be left where it is.
func (c *Consumer) handleUntilDurable(ctx context.Context, m kafka.Message) bool {
	backoff := c.retryBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	maxBackoff := c.maxBackoff
	if maxBackoff < backoff {
		maxBackoff = backoff
	}
	for attempt := 1; ; attempt++ {
		err := c.processMessage(ctx, m)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}

		var perm *permanentError
		if errors.As(err, &perm) {
			// Committing past a message that can never succeed is the lesser
			// harm: the alternative blocks every later safety event on this
			// partition forever. It is logged at a level an operator can
			// alert on rather than dropped.
			log.Printf("[suggestion-consumer] PERMANENTLY UNDELIVERABLE partition=%d offset=%d: %v "+
				"(committing so the partition is not stalled)", m.Partition, m.Offset, err)
			return true
		}

		log.Printf("[suggestion-consumer] durable effect failed (attempt %d, partition=%d offset=%d), "+
			"offset NOT committed, retrying in %s: %v", attempt, m.Partition, m.Offset, backoff, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
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
		// No redelivery will make this parse. See permanentError.
		return permanent(fmt.Errorf("decode envelope: %w", err))
	}

	// LB-2 / CLB-1: recognise a replay before applying a safety effect.
	//
	// graph-service delivers block/follow events from a durable outbox with
	// AT-LEAST-ONCE semantics — the relay publishes, then marks the row
	// published, and a crash between those two steps redelivers. That ordering
	// is deliberate (the alternative loses events), which makes recognising a
	// duplicate the consumer's job. The publisher sets EventID to the outbox
	// row id precisely so a redelivery is identifiable.
	//
	// Seen() is READ-ONLY and the mark is written only after the handler
	// succeeds (see below). The earlier version claimed the key here, which
	// meant a handler failure marked the event applied and suppressed the
	// redelivery that would have repaired it.
	//
	// A dedupe FAILURE processes the event rather than skipping it: applying a
	// block cooldown twice is wasted work, skipping one leaves a blocked
	// account in someone's suggestions.
	if c.dedupe != nil {
		seen, err := c.dedupe.Seen(ctx, m.Value)
		if err != nil {
			log.Printf("[suggestion-consumer] dedupe check failed (processing anyway): %v", err)
		} else if seen {
			return nil
		}
	}

	if err := c.dispatch(ctx, envelope); err != nil {
		return err
	}

	// The effect is durable now, so — and only now — record it as applied.
	if c.dedupe != nil {
		if err := c.dedupe.MarkApplied(ctx, m.Value); err != nil {
			// Costs a redundant replay, which the durable inbox absorbs.
			log.Printf("[suggestion-consumer] dedupe mark failed (replay will be absorbed): %v", err)
		}
	}
	return nil
}

func (c *Consumer) dispatch(ctx context.Context, envelope events.EventEnvelope) error {
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
	if err := c.svc.CreateCooldownFromEvent(ctx, receiverID, senderID, "decline"); err != nil {
		return fmt.Errorf("decline cooldown %s->%s: %w", payload.ReceiverID, payload.SenderID, err)
	}

	// Also remove from candidate pools
	if err := c.store.RemoveCandidateForViewer(ctx, receiverID, senderID, "friend"); err != nil {
		return fmt.Errorf("remove declined candidate: %w", err)
	}
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
	for _, p := range [2][2]uuid.UUID{{userA, userB}, {userB, userA}} {
		if err := c.svc.CreateCooldownFromEvent(ctx, p[0], p[1], "removed_friend"); err != nil {
			return fmt.Errorf("removed-friend cooldown %s->%s: %w", p[0], p[1], err)
		}
	}

	// Invalidate caches
	c.invalidateCache(ctx, payload.UserA, "friend")
	c.invalidateCache(ctx, payload.UserB, "friend")

	log.Printf("[suggestion-consumer] FriendRemoved: 180d cooldown for %s↔%s", payload.UserA, payload.UserB)
	return nil
}

// handleUserBlocked applies the block safety effect durably, or returns an
// error so the offset is not committed and the broker redelivers.
//
// CLB-1: every datastore result here used to be discarded — two cooldown
// helpers that swallowed their own errors and four candidate deletes whose
// returns were ignored — so the handler reported success even when PostgreSQL
// was unreachable and none of the safety state had been written. The whole
// effect is now one transaction that also records the event in the consumer
// inbox, so it is all-or-nothing and a replay is a no-op.
func (c *Consumer) handleUserBlocked(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserBlockedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanent(fmt.Errorf("UserBlocked payload: %w", err))
	}

	blockerID, err := uuid.Parse(payload.BlockerID)
	if err != nil {
		return permanent(fmt.Errorf("UserBlocked blocker_id %q: %w", payload.BlockerID, err))
	}
	blockedID, err := uuid.Parse(payload.BlockedID)
	if err != nil {
		return permanent(fmt.Errorf("UserBlocked blocked_id %q: %w", payload.BlockedID, err))
	}

	// Permanent both-direction cooldowns AND candidate removal, in one
	// transaction with the applied-event row.
	applied, err := c.store.ApplyUserBlockedEffects(ctx, envelope.EventID, blockerID, blockedID)
	if err != nil {
		// RETRYABLE. Do not commit the offset — this is the loss path.
		return fmt.Errorf("UserBlocked durable effect %s↔%s: %w", payload.BlockerID, payload.BlockedID, err)
	}

	// Cache invalidation happens AFTER the durable transaction. It is
	// rebuildable state with its own expiry, so a Redis failure must not roll
	// back or block a committed safety write.
	c.invalidateCache(ctx, payload.BlockerID, "friend")
	c.invalidateCache(ctx, payload.BlockerID, "follow")
	c.invalidateCache(ctx, payload.BlockedID, "friend")
	c.invalidateCache(ctx, payload.BlockedID, "follow")

	if applied {
		log.Printf("[suggestion-consumer] UserBlocked: permanent cooldown %s↔%s (event %s)",
			payload.BlockerID, payload.BlockedID, envelope.EventID)
	} else {
		log.Printf("[suggestion-consumer] UserBlocked: replay of event %s already applied, no change",
			envelope.EventID)
	}
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
