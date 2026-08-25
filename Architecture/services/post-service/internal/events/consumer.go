package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader *kafka.Reader
	db     *pgxpool.Pool
}

func NewConsumer(brokers []string, topic string, db *pgxpool.Pool) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "post-service-group",
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

	if envelope.EventType != events.EventUserDeletionRequested {
		return true // not ours; nothing to do
	}

	stall := 2 * time.Second
	const maxStall = 60 * time.Second
	for {
		err := c.handleUserDeletionRequested(ctx, envelope.Payload)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		log.Printf("Error handling user.deletion_requested (holding offset, retry in %s): %v\n",
			stall, err)
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
