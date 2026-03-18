package consumers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// FeedInjectPayload describes a single item to inject into a user's home timeline.
type FeedInjectPayload struct {
	TargetUserID string          `json:"target_user_id"`
	ItemType     string          `json:"item_type"`   // "channel_update"
	ItemID       string          `json:"item_id"`
	SourceType   string          `json:"source_type"` // "channel"
	SourceID     string          `json:"source_id"`
	Score        int64           `json:"score"`
	PublishedAt  string          `json:"published_at"`
	PreviewJSON  json.RawMessage `json:"preview_json"`
}

// FeedInjectEvent is the envelope for feed-inject messages on the
// atpost.channel.feed-inject topic.
type FeedInjectEvent struct {
	EventType string            `json:"event_type"`
	Payload   FeedInjectPayload `json:"payload"`
}

// ChannelUpdateConsumer listens for channel update feed-inject events and
// writes them into the user's ScyllaDB home timeline.
type ChannelUpdateConsumer struct {
	reader        *kafka.Reader
	timelineStore *scylla.TimelineStore
	rdb           *redis.Client
}

// NewChannelUpdateConsumer creates a consumer for channel update feed injection.
func NewChannelUpdateConsumer(brokers []string, timelineStore *scylla.TimelineStore, rdb *redis.Client) *ChannelUpdateConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  "feed-channel-inject-group",
		Topic:    "atpost.channel.feed-inject",
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})
	return &ChannelUpdateConsumer{
		reader:        reader,
		timelineStore: timelineStore,
		rdb:           rdb,
	}
}

// Start begins consuming messages in a blocking loop. Run this in a goroutine.
func (c *ChannelUpdateConsumer) Start(ctx context.Context) {
	slog.Info("channel update feed-inject consumer started",
		"topic", "atpost.channel.feed-inject",
		"group", "feed-channel-inject-group",
	)

	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("channel update consumer stopped (context cancelled)")
				return
			}
			slog.Error("channel update consumer read error", "error", err)
			return
		}

		if err := c.processMessage(ctx, m); err != nil {
			slog.Error("channel update consumer: failed to process message",
				"error", err,
				"offset", m.Offset,
				"partition", m.Partition,
			)
		}
	}
}

func (c *ChannelUpdateConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var event FeedInjectEvent
	if err := json.Unmarshal(m.Value, &event); err != nil {
		slog.Warn("channel update consumer: invalid message JSON", "error", err)
		return nil // skip malformed messages
	}

	p := event.Payload

	if p.TargetUserID == "" || p.ItemID == "" {
		slog.Warn("channel update consumer: missing target_user_id or item_id, skipping")
		return nil
	}

	targetUserID, err := uuid.Parse(p.TargetUserID)
	if err != nil {
		slog.Warn("channel update consumer: invalid target_user_id", "target_user_id", p.TargetUserID, "error", err)
		return nil
	}

	itemID, err := uuid.Parse(p.ItemID)
	if err != nil {
		slog.Warn("channel update consumer: invalid item_id", "item_id", p.ItemID, "error", err)
		return nil
	}

	sourceID, err := uuid.Parse(p.SourceID)
	if err != nil {
		slog.Warn("channel update consumer: invalid source_id", "source_id", p.SourceID, "error", err)
		return nil
	}

	// Parse published_at; fall back to now if missing/invalid
	publishedAt := time.Now().UTC()
	if p.PublishedAt != "" {
		if t, parseErr := time.Parse(time.RFC3339, p.PublishedAt); parseErr == nil {
			publishedAt = t
		}
	}

	// Dedup: skip if we already processed this item for this user (Redis key expires in 24h)
	dedupKey := "feed:inject:dedup:" + p.TargetUserID + ":" + p.ItemID
	set, err := c.rdb.SetNX(ctx, dedupKey, "1", 24*time.Hour).Result()
	if err != nil {
		slog.Warn("channel update consumer: redis dedup check failed", "error", err)
		// Continue anyway — duplicate insert to ScyllaDB is idempotent
	} else if !set {
		// Already processed
		return nil
	}

	// Insert into the user's home timeline using the existing ScyllaDB pattern.
	// item_id maps to post_id, source_id maps to author_id, item_type maps to content_type.
	if err := c.timelineStore.AddToHomeTimeline(ctx, targetUserID, itemID, sourceID, publishedAt, p.ItemType); err != nil {
		slog.Error("channel update consumer: failed to insert into home timeline",
			"target_user_id", p.TargetUserID,
			"item_id", p.ItemID,
			"error", err,
		)
		return err
	}

	slog.Info("channel update injected into feed",
		"target_user_id", p.TargetUserID,
		"item_type", p.ItemType,
		"item_id", p.ItemID,
		"source_type", p.SourceType,
		"source_id", p.SourceID,
	)
	return nil
}

// Close shuts down the Kafka reader.
func (c *ChannelUpdateConsumer) Close() error {
	return c.reader.Close()
}
