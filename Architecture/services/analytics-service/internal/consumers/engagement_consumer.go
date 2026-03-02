package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/facebook-like/analytics-service/internal/scoring"
	"github.com/facebook-like/shared/events"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// EngagementConsumer processes social engagement events (likes, comments)
// from Kafka. It writes normalized events into analytics.events_raw for
// the hourly aggregator and maintains near-real-time CQS updates in Redis.
type EngagementConsumer struct {
	pg   *pgxpool.Pool
	rdb  *redis.Client
	base *BaseConsumer
}

func NewEngagementConsumer(pg *pgxpool.Pool, rdb *redis.Client) *EngagementConsumer {
	return &EngagementConsumer{
		pg:   pg,
		rdb:  rdb,
		base: NewBaseConsumer(rdb, "analytics-engagement"),
	}
}

// Start launches the Kafka consumer loop. Blocks until ctx is cancelled.
func (c *EngagementConsumer) Start(ctx context.Context, brokers []string, topic string) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  "analytics-engagement",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6, // 10 MB
	})
	defer reader.Close()

	log.Println("[EngagementConsumer] started")

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[EngagementConsumer] fetch error: %v", err)
			continue
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(msg.Value, &envelope); err != nil {
			log.Printf("[EngagementConsumer] unmarshal error: %v", err)
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if !isEngagementEvent(envelope.EventType) {
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		// Dedup
		if c.base.IsDuplicate(ctx, envelope.EventID) {
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if err := c.processEvent(ctx, &envelope); err != nil {
			log.Printf("[EngagementConsumer] process error for %s: %v", envelope.EventType, err)
		}

		_ = reader.CommitMessages(ctx, msg)
	}
}

func isEngagementEvent(eventType string) bool {
	switch eventType {
	case events.PostReacted, events.CommentCreated, events.CommentReacted:
		return true
	}
	return false
}

func (c *EngagementConsumer) processEvent(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.PostReacted:
		return c.handlePostReacted(ctx, env)
	case events.CommentCreated:
		return c.handleCommentCreated(ctx, env)
	case events.CommentReacted:
		// Comment reactions don't affect post-level CQS directly
		return nil
	}
	return nil
}

func (c *EngagementConsumer) handlePostReacted(ctx context.Context, env *events.EventEnvelope) error {
	var p events.PostReactedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}

	// 1. Insert into events_raw for hourly aggregator
	if err := c.insertRawEvent(ctx, p.ReactorID, "like", p.PostID, p.PostAuthorID, env.OccurredAt); err != nil {
		log.Printf("[EngagementConsumer] insert raw event error: %v", err)
	}

	// 2. Bump real-time CQS
	return c.bumpCQS(ctx, p.PostID, "likes")
}

func (c *EngagementConsumer) handleCommentCreated(ctx context.Context, env *events.EventEnvelope) error {
	var p events.CommentCreatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}

	// 1. Insert into events_raw for hourly aggregator
	if err := c.insertRawEvent(ctx, p.AuthorID, "comment_create", p.PostID, p.PostAuthorID, env.OccurredAt); err != nil {
		log.Printf("[EngagementConsumer] insert raw event error: %v", err)
	}

	// 2. Bump real-time CQS
	return c.bumpCQS(ctx, p.PostID, "comments")
}

// insertRawEvent writes the engagement event into analytics.events_raw
// so that the hourly aggregator picks it up for content_hourly_agg.
func (c *EngagementConsumer) insertRawEvent(ctx context.Context, userID, eventType, contentID, creatorID string, ts time.Time) error {
	payload := map[string]string{
		"content_id": contentID,
		"creator_id": creatorID,
	}
	payloadJSON, _ := json.Marshal(payload)

	_, err := c.pg.Exec(ctx, `
		INSERT INTO analytics.events_raw (id, user_id, type, payload, ts)
		VALUES (gen_random_uuid(), $1::uuid, $2, $3::jsonb, $4)`,
		userID, eventType, payloadJSON, ts,
	)
	return err
}

// bumpCQS incrementally updates the cached CQS for a post. Every 10
// engagement events it reads the latest aggregate from Postgres, recomputes
// CQS, and caches the result in Redis.
func (c *EngagementConsumer) bumpCQS(ctx context.Context, postID, counterType string) error {
	// Increment real-time engagement counter
	counterKey := fmt.Sprintf("post:rt_engagement:%s:%s", postID, counterType)
	c.rdb.Incr(ctx, counterKey)
	c.rdb.Expire(ctx, counterKey, 24*time.Hour)

	// Tick the total counter; recalculate every 10 events
	totalKey := fmt.Sprintf("post:rt_engagement:%s:total", postID)
	total, err := c.rdb.Incr(ctx, totalKey).Result()
	if err != nil {
		return err
	}
	c.rdb.Expire(ctx, totalKey, 24*time.Hour)

	if total%10 != 0 {
		return nil // skip recalculation for most events
	}

	return c.recalculateCQS(ctx, postID)
}

// recalculateCQS fetches the latest aggregated metrics from PostgreSQL,
// adds real-time deltas from Redis, and caches the updated CQS.
func (c *EngagementConsumer) recalculateCQS(ctx context.Context, postID string) error {
	var metrics scoring.AggregateMetrics
	err := c.pg.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(impressions), 0),
			COALESCE(SUM(likes), 0),
			COALESCE(SUM(comments), 0),
			COALESCE(SUM(shares), 0),
			COALESCE(SUM(saves), 0),
			COALESCE(SUM(follows_from_content), 0),
			COALESCE(SUM(reports), 0),
			COALESCE(SUM(not_interested), 0),
			COALESCE(AVG(avg_percent_viewed), 0)
		FROM analytics.content_hourly_agg
		WHERE content_id = $1::uuid
		  AND hour_bucket > NOW() - INTERVAL '7 days'`,
		postID,
	).Scan(
		&metrics.Impressions, &metrics.Likes, &metrics.Comments,
		&metrics.Shares, &metrics.Saves,
		&metrics.FollowsFromContent, &metrics.Reports, &metrics.NotInterested,
		&metrics.AvgPercentViewed,
	)
	if err != nil {
		// No aggregate data yet — use a minimal estimate so new content
		// still gets a CQS from real-time engagement signals.
		metrics.Impressions = 1
	}

	// Add real-time deltas from Redis that haven't been aggregated yet
	if delta, err := c.rdb.Get(ctx, fmt.Sprintf("post:rt_engagement:%s:likes", postID)).Int64(); err == nil {
		metrics.Likes += delta
	}
	if delta, err := c.rdb.Get(ctx, fmt.Sprintf("post:rt_engagement:%s:comments", postID)).Int64(); err == nil {
		metrics.Comments += delta
	}

	// Compute updated CQS
	cqs := scoring.ComputeCQS(&metrics)

	// Cache to Redis with 2-hour TTL (longer than daily rollup's 1-hour)
	cqsKey := fmt.Sprintf("post:cqs:%s", postID)
	if err := c.rdb.Set(ctx, cqsKey, cqs, 2*time.Hour).Err(); err != nil {
		return fmt.Errorf("set CQS cache: %w", err)
	}

	return nil
}
