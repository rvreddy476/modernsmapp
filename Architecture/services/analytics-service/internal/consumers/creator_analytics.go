package consumers

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/atpost/shared/events"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// CreatorAnalyticsConsumer processes reel/content engagement events
// (likes, comments, shares, saves) and writes normalized events into
// analytics.events_raw for the hourly aggregator.
type CreatorAnalyticsConsumer struct {
	pg   *pgxpool.Pool
	rdb  *redis.Client
	base *BaseConsumer
}

func NewCreatorAnalyticsConsumer(pg *pgxpool.Pool, rdb *redis.Client) *CreatorAnalyticsConsumer {
	return &CreatorAnalyticsConsumer{
		pg:   pg,
		rdb:  rdb,
		base: NewBaseConsumer(rdb, "analytics-creator"),
	}
}

// Start launches the Kafka consumer loop. Blocks until ctx is cancelled.
func (c *CreatorAnalyticsConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  "analytics-creator",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6, // 10 MB
		Dialer:   dialer,
	})
	defer reader.Close()

	log.Println("[CreatorAnalyticsConsumer] started")

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[CreatorAnalyticsConsumer] fetch error: %v", err)
			continue
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(msg.Value, &envelope); err != nil {
			log.Printf("[CreatorAnalyticsConsumer] unmarshal error: %v", err)
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if !isCreatorAnalyticsEvent(envelope.EventType) {
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		// Dedup
		if c.base.IsDuplicate(ctx, envelope.EventID) {
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if err := c.processEvent(ctx, &envelope); err != nil {
			log.Printf("[CreatorAnalyticsConsumer] process error for %s: %v", envelope.EventType, err)
		}

		_ = reader.CommitMessages(ctx, msg)
	}
}

func isCreatorAnalyticsEvent(eventType string) bool {
	switch eventType {
	case events.ReelViewed, events.EventReelLiked,
		events.EventReelCommented,
		events.ReelShared, events.ReelSaved,
		events.ReelCommentCreated:
		return true
	}
	return false
}

func (c *CreatorAnalyticsConsumer) processEvent(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.ReelViewed:
		return c.handleReelViewed(ctx, env)
	case events.EventReelLiked:
		return c.handleReelLiked(ctx, env)
	case events.EventReelCommented, events.ReelCommentCreated:
		return c.handleReelCommented(ctx, env)
	case events.ReelShared:
		return c.handleReelShared(ctx, env)
	case events.ReelSaved:
		return c.handleReelSaved(ctx, env)
	}
	return nil
}

func (c *CreatorAnalyticsConsumer) handleReelViewed(ctx context.Context, env *events.EventEnvelope) error {
	var p events.ReelViewedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	return c.insertRawEvent(ctx, p.ViewerID, "reel_view", p.ReelID, "", env.OccurredAt)
}

func (c *CreatorAnalyticsConsumer) handleReelLiked(ctx context.Context, env *events.EventEnvelope) error {
	// EventReelLiked uses VideoEngagementPayload-style structure
	var p struct {
		ReelID    string `json:"reel_id"`
		UserID    string `json:"user_id"`
		CreatorID string `json:"creator_id"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	return c.insertRawEvent(ctx, p.UserID, "reel_like", p.ReelID, p.CreatorID, env.OccurredAt)
}

func (c *CreatorAnalyticsConsumer) handleReelCommented(ctx context.Context, env *events.EventEnvelope) error {
	var p events.ReelCommentCreatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	return c.insertRawEvent(ctx, p.AuthorID, "reel_comment", p.ReelID, "", env.OccurredAt)
}

func (c *CreatorAnalyticsConsumer) handleReelShared(ctx context.Context, env *events.EventEnvelope) error {
	var p events.ReelSharedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	return c.insertRawEvent(ctx, p.UserID, "reel_share", p.ReelID, "", env.OccurredAt)
}

func (c *CreatorAnalyticsConsumer) handleReelSaved(ctx context.Context, env *events.EventEnvelope) error {
	var p events.ReelSavedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	return c.insertRawEvent(ctx, p.UserID, "reel_save", p.ReelID, "", env.OccurredAt)
}

// insertRawEvent writes the engagement event into analytics.events_raw.
func (c *CreatorAnalyticsConsumer) insertRawEvent(ctx context.Context, userID, eventType, contentID, creatorID string, ts time.Time) error {
	payload := map[string]string{
		"content_id": contentID,
	}
	if creatorID != "" {
		payload["creator_id"] = creatorID
	}
	payloadJSON, _ := json.Marshal(payload)

	_, err := c.pg.Exec(ctx, `
		INSERT INTO analytics.events_raw (id, user_id, type, payload, ts)
		VALUES (gen_random_uuid(), $1::uuid, $2, $3::jsonb, $4)`,
		userID, eventType, payloadJSON, ts,
	)
	return err
}
