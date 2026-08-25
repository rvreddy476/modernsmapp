package events

import (
	"context"
	"encoding/json"
	"log"

	"github.com/atpost/shared/events"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// EventUserSettingsChanged mirrors identity user-service's producer constant.
// The payload carries only {user_id, privacy_version} — an invalidation
// signal, never the values themselves.
const EventUserSettingsChanged = "user.settings_changed"

type Consumer struct {
	reader *kafka.Reader
	db     *pgxpool.Pool
	rdb    *redis.Client
}

// NewConsumer builds the identity-events consumer.
//
// rdb powers the production-chat privacy-cache invalidation (directive §5.1):
// user.settings_changed drops privacy:<user_id> so the next permission check
// re-reads the canonical snapshot instead of waiting out the 3-second TTL.
// The TTL stays as the fallback for a lost event.
func NewConsumer(brokers []string, topic string, dialer *kafka.Dialer, db *pgxpool.Pool, rdb *redis.Client) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "graph-service-group",
		Dialer:  dialer,
	})
	return &Consumer{reader: r, db: db, rdb: rdb}
}

func (c *Consumer) Start(ctx context.Context) {
	log.Println("Starting Kafka consumer...")
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("Error reading message: %v\n", err)
			break
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(m.Value, &envelope); err != nil {
			log.Printf("Error unmarshalling event: %v\n", err)
			continue
		}

		switch envelope.EventType {
		case events.EventUserDeletionRequested:
			if err := c.handleUserDeletionRequested(ctx, envelope.Payload); err != nil {
				log.Printf("Error handling user.deletion_requested: %v\n", err)
			}
		case EventUserSettingsChanged:
			if err := c.handleUserSettingsChanged(ctx, envelope.Payload); err != nil {
				log.Printf("Error handling user.settings_changed: %v\n", err)
			}
		default:
			// Ignore other events
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

	// Remove all follow/friend/block relationships for the deleted user
	_, err := c.db.Exec(ctx,
		`DELETE FROM follows WHERE follower_id = $1 OR followee_id = $1`,
		p.UserID,
	)
	if err != nil {
		return err
	}

	log.Printf("Removed graph relationships for user %s\n", p.UserID)
	return nil
}

// handleUserSettingsChanged drops the cached privacy snapshot so permission
// checks stop serving pre-change values. Idempotent — a duplicate or late
// event deletes an already-absent key.
func (c *Consumer) handleUserSettingsChanged(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	if p.UserID == "" || c.rdb == nil {
		return nil
	}
	return c.rdb.Del(ctx, "privacy:"+p.UserID).Err()
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
