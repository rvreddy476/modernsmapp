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
	ItemType     string          `json:"item_type"` // "channel_update"
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

	// handle and commit are seams over processMessage and the reader's
	// commit, so the retry/commit ordering is testable without a broker.
	handle  func(context.Context, kafka.Message) error
	commit  func(context.Context, kafka.Message) error
	backoff time.Duration
}

// NewChannelUpdateConsumer creates a consumer for channel update feed injection.
func NewChannelUpdateConsumer(brokers []string, timelineStore *scylla.TimelineStore, rdb *redis.Client) *ChannelUpdateConsumer {
	return NewChannelUpdateConsumerWithDialer(brokers, timelineStore, rdb, nil)
}

// NewChannelUpdateConsumerWithDialer creates a consumer for channel update feed injection.
func NewChannelUpdateConsumerWithDialer(brokers []string, timelineStore *scylla.TimelineStore, rdb *redis.Client, dialer *kafka.Dialer) *ChannelUpdateConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		GroupID: "feed-channel-inject-group",
		Topic:   "atpost.channel.feed-inject",
		// MinBytes 1 + a short MaxWait: with MinBytes 10 KB and the 10 s
		// default wait, a quiet topic delivered each update ten seconds late.
		MinBytes: 1,
		MaxBytes: 10e6, // 10MB
		MaxWait:  500 * time.Millisecond,
		Dialer:   dialer,
	})
	c := &ChannelUpdateConsumer{
		reader:        reader,
		timelineStore: timelineStore,
		rdb:           rdb,
		backoff:       time.Second,
	}
	c.handle = c.processMessage
	c.commit = func(ctx context.Context, m kafka.Message) error { return c.reader.CommitMessages(ctx, m) }
	return c
}

// channelInjectAttempts bounds the retries of one message. There is no DLQ
// for this topic and a channel update is not worth stalling the partition
// for, so after the last attempt the message is logged and committed.
const channelInjectAttempts = 5

// Start begins consuming messages in a blocking loop. Run this in a goroutine.
//
// At-least-once: the offset is committed only after the message was applied
// (or its retries were exhausted). It used to be ReadMessage, which commits
// before returning, so a failed timeline insert was lost; and the loop
// returned on its first read error, ending consumption for the life of the
// pod.
func (c *ChannelUpdateConsumer) Start(ctx context.Context) {
	slog.Info("channel update feed-inject consumer started",
		"topic", "atpost.channel.feed-inject",
		"group", "feed-channel-inject-group",
	)

	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("channel update consumer stopped (context cancelled)")
				return
			}
			slog.Error("channel update consumer fetch error; retrying", "error", err)
			if !c.sleep(ctx, c.backoff) {
				return
			}
			continue
		}
		if !c.apply(ctx, m) {
			return // ctx cancelled mid-retry; the offset stays for redelivery
		}
	}
}

// apply runs one message to completion — handled, or retries spent — and
// then commits it. It returns false only when ctx is cancelled.
func (c *ChannelUpdateConsumer) apply(ctx context.Context, m kafka.Message) bool {
	wait := c.backoff
	for attempt := 1; ; attempt++ {
		err := c.handle(ctx, m)
		if err == nil {
			break
		}
		if attempt >= channelInjectAttempts {
			slog.Error("channel update consumer: giving up on message after retries",
				"error", err, "attempts", attempt,
				"offset", m.Offset, "partition", m.Partition)
			break
		}
		slog.Warn("channel update consumer: retrying message",
			"error", err, "attempt", attempt, "offset", m.Offset, "partition", m.Partition)
		if !c.sleep(ctx, wait) {
			return false
		}
		wait *= 2
	}
	// Commit before fetching the next message: a crash before this point
	// replays the message (harmless, the insert upserts) instead of losing it.
	wait = c.backoff
	for {
		err := c.commit(ctx, m)
		if err == nil {
			return true
		}
		slog.Error("channel update consumer: offset commit failed; retrying", "error", err, "offset", m.Offset)
		if !c.sleep(ctx, wait) {
			return false
		}
		if wait < 30*time.Second {
			wait *= 2
		}
	}
}

func (c *ChannelUpdateConsumer) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
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

	// Dedup: skip if this item was already injected for this user. The key
	// is a RECEIPT, written below only after the insert succeeded — written
	// first (as SETNX, before the insert) it made a failed insert look done
	// to the redelivery. A Redis error just means the insert runs again,
	// which is harmless: AddToHomeTimeline upserts on (user, post).
	dedupKey := "feed:inject:dedup:" + p.TargetUserID + ":" + p.ItemID
	if n, err := c.rdb.Exists(ctx, dedupKey).Result(); err != nil {
		slog.Warn("channel update consumer: redis dedup check failed", "error", err)
	} else if n > 0 {
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
	if err := c.rdb.Set(ctx, dedupKey, "1", 24*time.Hour).Err(); err != nil {
		slog.Warn("channel update consumer: could not write dedup receipt", "error", err)
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
