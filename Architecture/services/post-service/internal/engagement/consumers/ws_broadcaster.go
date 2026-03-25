package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// WSBroadcasterConsumer publishes engagement updates to Redis pub/sub
// for the ws-gateway to relay to connected WebSocket clients.
type WSBroadcasterConsumer struct {
	rdb  *redis.Client
	base *engagement.BaseConsumer
}

// NewWSBroadcasterConsumer creates a new WebSocket broadcaster consumer.
func NewWSBroadcasterConsumer(rdb *redis.Client) *WSBroadcasterConsumer {
	return &WSBroadcasterConsumer{
		rdb:  rdb,
		base: engagement.NewBaseConsumer(rdb, "ws-broadcast"),
	}
}

// Start begins the consumer loop. Blocks until ctx is canceled.
func (c *WSBroadcasterConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := engagement.NewKafkaReaderWithDialer(brokers, topic, "eng-ws-broadcast", dialer)
	defer reader.Close()

	engagement.ConsumerLoop(ctx, reader, c.base, c.handleEvent)
}

func (c *WSBroadcasterConsumer) handleEvent(ctx context.Context, event *engagement.EngagementEvent) error {
	switch event.EventType {
	case engagement.EventPostLiked, engagement.EventPostUnliked:
		return c.broadcastCountUpdate(ctx, event, "reaction")
	case engagement.EventCommentCreated:
		return c.broadcastCommentUpdate(ctx, event)
	case engagement.EventCommentDeleted:
		return c.broadcastCountUpdate(ctx, event, "comment_deleted")
	case engagement.EventPostShared:
		return c.broadcastCountUpdate(ctx, event, "share")
	case engagement.EventPostBookmarked, engagement.EventPostUnbookmarked:
		// Bookmarks are PRIVATE — no broadcast
		return nil
	default:
		return nil
	}
}

func (c *WSBroadcasterConsumer) broadcastCountUpdate(ctx context.Context, event *engagement.EngagementEvent, updateType string) error {
	// Fetch current counts from Redis
	engKey := fmt.Sprintf("post:eng:%s", event.PostID)
	counters, err := c.rdb.HGetAll(ctx, engKey).Result()
	if err != nil {
		log.Printf("[ws-broadcast] failed to get counters for %s: %v", event.PostID, err)
	}

	signal, _ := json.Marshal(map[string]any{
		"type": "post_update",
		"payload": map[string]any{
			"post_id":     event.PostID.String(),
			"update_type": updateType,
			"actor_id":    event.UserID.String(),
			"likes":       parseCount(counters, "likes"),
			"comments":    parseCount(counters, "comments"),
			"shares":      parseCount(counters, "shares"),
		},
	})

	// Publish to both: global feed channel + per-post room channel
	pipe := c.rdb.Pipeline()
	pipe.Publish(ctx, "feed:post_update", signal)
	pipe.Publish(ctx, fmt.Sprintf("post:%s", event.PostID.String()), signal)
	_, err = pipe.Exec(ctx)
	return err
}

func (c *WSBroadcasterConsumer) broadcastCommentUpdate(ctx context.Context, event *engagement.EngagementEvent) error {
	engKey := fmt.Sprintf("post:eng:%s", event.PostID)
	counters, err := c.rdb.HGetAll(ctx, engKey).Result()
	if err != nil {
		log.Printf("[ws-broadcast] failed to get counters for %s: %v", event.PostID, err)
	}

	signal, _ := json.Marshal(map[string]any{
		"type": "post_update",
		"payload": map[string]any{
			"post_id":     event.PostID.String(),
			"update_type": "comment",
			"actor_id":    event.UserID.String(),
			"comment_id":  event.TargetID.String(),
			"likes":       parseCount(counters, "likes"),
			"comments":    parseCount(counters, "comments"),
			"shares":      parseCount(counters, "shares"),
		},
	})

	// Publish to both: global feed channel + per-post room channel
	pipe := c.rdb.Pipeline()
	pipe.Publish(ctx, "feed:post_update", signal)
	pipe.Publish(ctx, fmt.Sprintf("post:%s", event.PostID.String()), signal)
	_, err = pipe.Exec(ctx)
	return err
}

func parseCount(counters map[string]string, field string) int64 {
	if v, ok := counters[field]; ok {
		var n int64
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}
