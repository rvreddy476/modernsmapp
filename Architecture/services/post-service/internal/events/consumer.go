package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/post-service/internal/purge"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader *kafka.Reader
	db     *pgxpool.Pool
	// lifecycle handles user.deactivated / deletion_scheduled / reactivated /
	// deletion_cancelled / purge_requested (see internal/purge). Optional.
	lifecycle *purge.Handler
	// subscriptions handles UserUnfollowed (graph-service, social.events.v1):
	// a Tube subscription is a follow plus a bell, so when the follow goes
	// away through any door the subscription goes with it. Optional.
	subscriptions subscriptionRemover
}

// subscriptionRemover is the one store write the UserUnfollowed handler
// needs, an interface so the consumer test drives it with a fake.
type subscriptionRemover interface {
	// DeleteSubscriptionByOwner removes subscriber -> owner's channel and
	// emits tube.channel.unsubscribed when a row went.
	DeleteSubscriptionByOwner(ctx context.Context, ownerUserID, subscriberID uuid.UUID) (bool, error)
}

// WithLifecycleHandler wires the account-control (hide / purge) handler.
func (c *Consumer) WithLifecycleHandler(h *purge.Handler) *Consumer {
	c.lifecycle = h
	return c
}

// WithSubscriptionStore wires the channel-subscription store so
// UserUnfollowed drops the matching subscription.
func (c *Consumer) WithSubscriptionStore(s subscriptionRemover) *Consumer {
	c.subscriptions = s
	return c
}

// NewConsumer builds the identity-events consumer. The group id is distinct
// from every other post-service consumer group (e.g. the engagement topic's
// "post-service-group") so this subscription's offsets never collide with
// an unrelated one on the same broker.
func NewConsumer(brokers []string, topic string, db *pgxpool.Pool) *Consumer {
	return NewConsumerWithGroup(brokers, topic, "post-service-identity-group", db)
}

// NewConsumerWithGroup is NewConsumer with an explicit group id, for a
// second instance of this loop on another topic (the graph events on
// social.events.v1 carry UserUnfollowed; identity.events.v1 does not).
// Sharing a group id across topics would let one topic's commits be
// mistaken for the other's, so each instance names its own.
func NewConsumerWithGroup(brokers []string, topic, groupID string, db *pgxpool.Pool) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
	})
	return &Consumer{reader: r, db: db}
}

// Start consumes the identity topic until ctx is cancelled.
//
// Re-review P0-5: this used ReadMessage, which commits the offset before
// the handler runs, and then only LOGGED a failed deletion. A transient
// PostgreSQL or outbox failure therefore left a deleted account's posts
// undeleted in the canonical database, with the request already committed
// and no redelivery — permanently.
//
// Search-service's author fence does not cover this. It protects the
// search index; it does nothing for the posts table or any other
// post-service read surface.
//
// The loop now fetches, handles, and commits only on success, and never
// advances past an unresolved deletion — Kafka offsets are cumulative, so
// committing a later message would silently commit the failed one too.
func (c *Consumer) Start(ctx context.Context) {
	log.Println("Starting Kafka consumer...")
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("post-service consumer shutting down")
				return
			}
			log.Printf("Error reading message: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if !c.handleUntilDurable(ctx, m) {
			return // shutting down; leave the offset for redelivery
		}

		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := c.reader.CommitMessages(commitCtx, m); err != nil {
			log.Printf("Warning: offset commit failed, message will be redelivered: %v\n", err)
		}
		cancel()
	}
}

// handleUntilDurable processes one message, retrying in place until it
// succeeds. Reports false only on shutdown.
//
// A malformed envelope is treated as handled: it can never succeed, and
// blocking the partition forever on an undecodable message would stop
// every subsequent deletion.
func (c *Consumer) handleUntilDurable(ctx context.Context, m kafka.Message) bool {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		log.Printf("Error unmarshalling event (skipping, cannot ever succeed): %v\n", err)
		return true
	}

	if envelope.EventType == events.UserUnfollowed {
		if c.subscriptions == nil {
			return true // this instance is not wired for graph events
		}
		// Same hold-the-offset contract as deletion: a subscription that
		// outlives its follow keeps pushing uploads to someone who left,
		// so a transient store failure must be retried, never skipped.
		return c.retryUntilDurable(ctx, events.UserUnfollowed, func() error {
			return c.handleUserUnfollowed(ctx, envelope.Payload)
		})
	}

	if envelope.EventType != events.EventUserDeletionRequested {
		// Not the legacy (kept-for-compatibility, no longer emitted)
		// deletion event. If it's one of the account-lifecycle events
		// (deactivate/delete-schedule/reactivate/cancel/purge), the
		// lifecycle handler retries it in place with its own backoff —
		// same "never advance the offset past an unresolved event"
		// guarantee this loop gives handleUserDeletionRequested below.
		if c.lifecycle != nil && purge.Handles(envelope.EventType) {
			return c.lifecycle.HandleUntilDurable(ctx, envelope.EventType, envelope.Payload)
		}
		return true // not ours; nothing to do
	}

	return c.retryUntilDurable(ctx, events.EventUserDeletionRequested, func() error {
		return c.handleUserDeletionRequested(ctx, envelope.Payload)
	})
}

// retryUntilDurable runs handle until it succeeds, backing off from 2s to
// 60s, holding the offset the whole time. Reports false only on shutdown.
func (c *Consumer) retryUntilDurable(ctx context.Context, name string, handle func() error) bool {
	stall := 2 * time.Second
	const maxStall = 60 * time.Second
	for {
		err := handle()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		log.Printf("Error handling %s (holding offset, retry in %s): %v\n", name, stall, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(stall):
		}
		if stall < maxStall {
			stall *= 2
			if stall > maxStall {
				stall = maxStall
			}
		}
	}
}

// handleUserUnfollowed drops follower -> followee's channel subscription.
// A malformed id can never succeed and is reported as an error so the
// retry loop surfaces it; a missing row or channel is a clean no-op, which
// makes redelivery of an already-applied event harmless.
func (c *Consumer) handleUserUnfollowed(ctx context.Context, payload json.RawMessage) error {
	var p events.UserUnfollowedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("user unfollowed: decode: %w", err)
	}
	followerID, err := uuid.Parse(p.FollowerID)
	if err != nil {
		return fmt.Errorf("user unfollowed: bad follower_id %q: %w", p.FollowerID, err)
	}
	followeeID, err := uuid.Parse(p.FolloweeID)
	if err != nil {
		return fmt.Errorf("user unfollowed: bad followee_id %q: %w", p.FolloweeID, err)
	}
	deleted, err := c.subscriptions.DeleteSubscriptionByOwner(ctx, followeeID, followerID)
	if err != nil {
		return fmt.Errorf("user unfollowed: drop subscription: %w", err)
	}
	if deleted {
		log.Printf("Dropped channel subscription %s -> %s on unfollow\n", followerID, followeeID)
	}
	return nil
}

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return fmt.Errorf("user deletion: bad user_id %q: %w", p.UserID, err)
	}

	// M2-P0-7: account deletion previously ran a bare mass UPDATE of
	// deleted_at. It never bumped search_rev and never wrote an
	// eligibility event, so the search index learned about the deletion
	// only through a separate hard delete-by-author — which erased every
	// revision marker and left nothing to stop a stale PostCreated or
	// approval from recreating the erased account's content.
	//
	// Deletion now goes through the SAME transactional choke point as
	// every other transition: one transaction per post, bumping search_rev
	// and writing the outbox event atomically with the state change. That
	// makes the per-post removals ordered and replay-safe like everything
	// else, and search-service's author fence is then a second layer
	// rather than the only one.
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("user deletion: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`UPDATE posts SET deleted_at = NOW()
		 WHERE author_id = $1 AND deleted_at IS NULL
		 RETURNING id`, userID)
	if err != nil {
		return fmt.Errorf("user deletion: soft-delete posts: %w", err)
	}
	var postIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("user deletion: scan post id: %w", err)
		}
		postIDs = append(postIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("user deletion: iterate posts: %w", err)
	}

	for _, id := range postIDs {
		if err := postgres.BumpSearchRevAndEmitTx(ctx, tx, id); err != nil {
			return fmt.Errorf("user deletion: emit eligibility for %s: %w", id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("user deletion: commit: %w", err)
	}

	log.Printf("Soft-deleted %d posts for user %s (eligibility events emitted)\n", len(postIDs), p.UserID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
