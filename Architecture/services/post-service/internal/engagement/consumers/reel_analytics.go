package consumers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	kafka "github.com/segmentio/kafka-go"
)

// ReelAnalyticsConsumer processes reel view and engagement events for analytics.
type ReelAnalyticsConsumer struct {
	db  *pgxpool.Pool
	rdb *redis.Client
}

func NewReelAnalyticsConsumer(db *pgxpool.Pool, rdb *redis.Client) *ReelAnalyticsConsumer {
	return &ReelAnalyticsConsumer{db: db, rdb: rdb}
}

func (c *ReelAnalyticsConsumer) Start(ctx context.Context, brokers []string, topic string) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     "reel-analytics-consumer",
		StartOffset: kafka.LastOffset,
		MaxWait:     3 * time.Second,
	})
	defer reader.Close()

	slog.Info("reel analytics consumer started", "topic", topic)

	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("reel analytics: read error", "error", err)
			continue
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(msg.Value, &envelope); err != nil {
			slog.Error("reel analytics: unmarshal envelope error", "error", err)
			continue
		}

		switch envelope.EventType {
		case events.ReelViewed:
			c.handleReelViewed(ctx, envelope.Payload)
		case events.ReelCommentCreated:
			c.handleReelComment(ctx, envelope.Payload)
		case events.ReelShared:
			c.handleReelShared(ctx, envelope.Payload)
		}
	}
}

func (c *ReelAnalyticsConsumer) handleReelViewed(ctx context.Context, payload json.RawMessage) {
	var p events.ReelViewedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Error("reel analytics: unmarshal ReelViewed", "error", err)
		return
	}

	// Aggregate watch-time per reel in Redis for real-time dashboards
	key := "reel:watchtime:" + p.ReelID
	c.rdb.IncrBy(ctx, key, p.WatchedMs)
	c.rdb.Expire(ctx, key, 24*time.Hour)

	// Track daily unique viewers in a HyperLogLog
	hlKey := "reel:viewers:" + p.ReelID + ":" + time.Now().UTC().Format("2006-01-02")
	c.rdb.PFAdd(ctx, hlKey, p.ViewerID)
	c.rdb.Expire(ctx, hlKey, 48*time.Hour)
}

func (c *ReelAnalyticsConsumer) handleReelComment(ctx context.Context, payload json.RawMessage) {
	var p events.ReelCommentCreatedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Error("reel analytics: unmarshal ReelComment", "error", err)
		return
	}

	// Increment comment count in Redis for real-time
	key := "reel:comments:today:" + p.ReelID
	c.rdb.Incr(ctx, key)
	c.rdb.Expire(ctx, key, 24*time.Hour)
}

func (c *ReelAnalyticsConsumer) handleReelShared(ctx context.Context, payload json.RawMessage) {
	var p events.ReelSharedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Error("reel analytics: unmarshal ReelShared", "error", err)
		return
	}

	// Increment share count in Redis for real-time
	key := "reel:shares:today:" + p.ReelID
	c.rdb.Incr(ctx, key)
	c.rdb.Expire(ctx, key, 24*time.Hour)
}
