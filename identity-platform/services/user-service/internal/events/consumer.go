package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

// UserHandler is the interface the consumer uses to act on events.
type UserHandler interface {
	CreateUser(ctx context.Context, id uuid.UUID, under18 bool) error
}

// isMinor reports whether a "YYYY-MM-DD" date of birth places the user under
// 18 today. An empty or unparseable DOB is treated as not-a-minor — spec §22
// open-decision #8 leaves missing-DOB handling to a later flow.
func isMinor(dob string) bool {
	if dob == "" {
		return false
	}
	t, err := time.Parse("2006-01-02", dob)
	if err != nil {
		return false
	}
	return time.Now().Before(t.AddDate(18, 0, 0))
}

// Consumer reads events from Kafka and applies them to the user-service.
type Consumer struct {
	reader  *kafka.Reader
	db      *pgxpool.Pool
	handler UserHandler
	log     *slog.Logger
}

const userConsumerName = "user-service"

// NewConsumer constructs a Kafka consumer for the user-service.
func NewConsumer(brokers []string, topic, groupID string, db *pgxpool.Pool, handler UserHandler, logger *slog.Logger) *Consumer {
	return NewConsumerWithDialer(brokers, topic, groupID, nil, db, handler, logger)
}

func NewConsumerWithDialer(brokers []string, topic, groupID string, dialer *kafka.Dialer, db *pgxpool.Pool, handler UserHandler, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
		Dialer:  dialer,
	})
	return &Consumer{reader: r, db: db, handler: handler, log: logger}
}

// Start runs the consumer loop until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("starting user-service kafka consumer")
	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("user-service kafka consumer stopped")
				return
			}
			c.log.Error("failed to read kafka message", "err", err)
			continue
		}
		if err := c.handle(ctx, msg); err != nil {
			c.log.Error("failed to handle kafka message", "err", err, "offset", msg.Offset)
		}
	}
}

// Close closes the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.reader.Close()
}

func (c *Consumer) handle(ctx context.Context, msg kafka.Message) error {
	var env sharedEvents.EventEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		c.log.Warn("failed to unmarshal event envelope", "err", err)
		return nil // non-retryable — skip malformed message
	}

	eventID := env.EventID

	// Before processing: check if already processed (inbox dedup)
	var exists bool
	err := c.db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM usr.inbox_events WHERE consumer_name=$1 AND event_id=$2)",
		userConsumerName, eventID,
	).Scan(&exists)
	if err == nil && exists {
		c.log.Debug("skipping already-processed event", "event_id", eventID, "event_type", env.EventType)
		return nil // already processed, skip
	}
	if err != nil {
		c.log.Warn("inbox dedup check failed, processing anyway", "err", err, "event_id", eventID)
	}

	if err := c.dispatch(ctx, env); err != nil {
		return err
	}

	// After processing: mark as done
	c.db.Exec(ctx,
		"INSERT INTO usr.inbox_events(consumer_name, event_id) VALUES($1,$2) ON CONFLICT DO NOTHING",
		userConsumerName, eventID,
	)
	return nil
}

func (c *Consumer) dispatch(ctx context.Context, env sharedEvents.EventEnvelope) error {
	switch env.EventType {
	case sharedEvents.UserRegistered:
		var p sharedEvents.UserRegisteredPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			c.log.Warn("failed to unmarshal UserRegistered payload", "err", err, "event_id", env.EventID)
			return nil
		}
		userID, err := uuid.Parse(p.UserID)
		if err != nil {
			c.log.Warn("invalid user_id in UserRegistered event", "err", err, "event_id", env.EventID)
			return nil
		}
		if err := c.handler.CreateUser(ctx, userID, isMinor(p.DOB)); err != nil {
			return err
		}
		c.log.Info("created user record for new registration", "user_id", userID)
	default:
		// Ignore event types not relevant to user-service
	}
	return nil
}
