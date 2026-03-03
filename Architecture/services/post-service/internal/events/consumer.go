package events

import (
	"context"
	"encoding/json"
	"log"

	"github.com/atpost/shared/events"
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

	// Soft-delete all posts by this user
	_, err := c.db.Exec(ctx,
		`UPDATE posts SET deleted_at = NOW() WHERE author_id = $1 AND deleted_at IS NULL`,
		p.UserID,
	)
	if err != nil {
		return err
	}

	log.Printf("Soft-deleted posts for user %s\n", p.UserID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
