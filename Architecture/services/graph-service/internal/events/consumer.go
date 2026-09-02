package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
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
	// onAccountPublic runs when a user.settings_changed event reports
	// account_visibility=public: the service auto-accepts every pending follow
	// request toward that user. A callback rather than a *service.Service
	// because service imports this package (import cycle).
	onAccountPublic func(ctx context.Context, userID uuid.UUID) error
}

// WithAccountPublicHook wires the private→public auto-accept.
func (c *Consumer) WithAccountPublicHook(fn func(ctx context.Context, userID uuid.UUID) error) *Consumer {
	c.onAccountPublic = fn
	return c
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
//
// Private accounts: when the event carries account_visibility=public, every
// pending follow request toward the user is auto-accepted (chunks of 100,
// each producing UserFollowed). The event does not carry the OLD value, so
// this runs on every public-visibility event; a public account has no
// pending requests, so a redundant run is a single empty SELECT.
func (c *Consumer) handleUserSettingsChanged(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		UserID            string `json:"user_id"`
		AccountVisibility string `json:"account_visibility"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	if p.UserID == "" {
		return nil
	}
	if c.rdb != nil {
		if err := c.rdb.Del(ctx, "privacy:"+p.UserID).Err(); err != nil {
			return err
		}
	}
	if p.AccountVisibility != "public" || c.onAccountPublic == nil {
		return nil
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return fmt.Errorf("settings_changed: bad user_id %q: %w", p.UserID, err)
	}
	return c.onAccountPublic(ctx, userID)
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
