package consumers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ScyllaLikeConsumer writes like/unlike events to ScyllaDB (sharded post_likes + user_likes)
// and updates engagement_counters via LWT.
type ScyllaLikeConsumer struct {
	session *gocql.Session
	rdb     *redis.Client
	base    *engagement.BaseConsumer
}

// NewScyllaLikeConsumer creates a new ScyllaDB like writer consumer.
func NewScyllaLikeConsumer(session *gocql.Session, rdb *redis.Client) *ScyllaLikeConsumer {
	return &ScyllaLikeConsumer{
		session: session,
		rdb:     rdb,
		base:    engagement.NewBaseConsumer(rdb, "scylla-like"),
	}
}

// Start begins the consumer loop. Blocks until ctx is canceled.
func (c *ScyllaLikeConsumer) Start(ctx context.Context, brokers []string, topic string) {
	reader := engagement.NewKafkaReader(brokers, topic, "eng-scylla-like")
	defer reader.Close()

	engagement.ConsumerLoop(ctx, reader, c.base, c.handleEvent)
}

func (c *ScyllaLikeConsumer) handleEvent(ctx context.Context, event *engagement.EngagementEvent) error {
	switch event.EventType {
	case engagement.EventPostLiked, engagement.EventPostUnliked:
		return c.handleLike(ctx, event)
	case engagement.EventCommentLiked, engagement.EventCommentUnliked:
		return c.handleCommentLike(ctx, event)
	case engagement.EventCommentDisliked, engagement.EventCommentUndisliked:
		return c.handleCommentDislike(ctx, event)
	case engagement.EventPostShared:
		return c.handleShare(ctx, event)
	case engagement.EventPostBookmarked, engagement.EventPostUnbookmarked:
		return c.handleBookmark(ctx, event)
	default:
		return nil // ignore events this consumer doesn't handle
	}
}

func (c *ScyllaLikeConsumer) handleLike(ctx context.Context, event *engagement.EngagementEvent) error {
	ts := event.ActionTS.UnixMicro()
	shard := engagement.LikeShard(event.UserID)

	if event.IsSet {
		// INSERT into post_likes (sharded) + user_likes with USING TIMESTAMP
		if err := c.session.Query(`
			INSERT INTO post_likes (post_id, shard, user_id, created_at)
			VALUES (?, ?, ?, ?) USING TIMESTAMP ?`,
			event.PostID, shard, event.UserID, event.ActionTS, ts,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("insert post_likes: %w", err)
		}

		if err := c.session.Query(`
			INSERT INTO user_likes (user_id, post_id, created_at)
			VALUES (?, ?, ?) USING TIMESTAMP ?`,
			event.UserID, event.PostID, event.ActionTS, ts,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("insert user_likes: %w", err)
		}
	} else {
		// DELETE with USING TIMESTAMP for LWW
		c.session.Query(`
			DELETE FROM post_likes USING TIMESTAMP ?
			WHERE post_id = ? AND shard = ? AND user_id = ?`,
			ts, event.PostID, shard, event.UserID,
		).WithContext(ctx).Exec()

		c.session.Query(`
			DELETE FROM user_likes USING TIMESTAMP ?
			WHERE user_id = ? AND created_at = ? AND post_id = ?`,
			ts, event.UserID, event.ActionTS, event.PostID,
		).WithContext(ctx).Exec()
	}

	// Update engagement_counters via LWT
	delta := 1
	if !event.IsSet {
		delta = -1
	}
	if err := incrementCounter(c.session, "post", event.PostID, "likes", delta); err != nil {
		log.Printf("[scylla-like] counter update failed for %s: %v", event.PostID, err)
	}

	// Track hot post
	c.rdb.SAdd(ctx, "hot:posts", event.PostID.String())

	return nil
}

func (c *ScyllaLikeConsumer) handleCommentLike(ctx context.Context, event *engagement.EngagementEvent) error {
	ts := event.ActionTS.UnixMicro()

	if event.IsSet {
		if err := c.session.Query(`
			INSERT INTO comment_likes (comment_id, user_id, created_at)
			VALUES (?, ?, ?) USING TIMESTAMP ?`,
			event.TargetID, event.UserID, event.ActionTS, ts,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("insert comment_likes: %w", err)
		}
	} else {
		c.session.Query(`
			DELETE FROM comment_likes USING TIMESTAMP ?
			WHERE comment_id = ? AND user_id = ?`,
			ts, event.TargetID, event.UserID,
		).WithContext(ctx).Exec()
	}

	delta := 1
	if !event.IsSet {
		delta = -1
	}
	incrementCounter(c.session, "comment", event.TargetID, "likes", delta)

	return nil
}

func (c *ScyllaLikeConsumer) handleCommentDislike(ctx context.Context, event *engagement.EngagementEvent) error {
	ts := event.ActionTS.UnixMicro()

	if event.IsSet {
		if err := c.session.Query(`
			INSERT INTO comment_dislikes (comment_id, user_id, created_at)
			VALUES (?, ?, ?) USING TIMESTAMP ?`,
			event.TargetID, event.UserID, event.ActionTS, ts,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("insert comment_dislikes: %w", err)
		}
	} else {
		c.session.Query(`
			DELETE FROM comment_dislikes USING TIMESTAMP ?
			WHERE comment_id = ? AND user_id = ?`,
			ts, event.TargetID, event.UserID,
		).WithContext(ctx).Exec()
	}

	delta := 1
	if !event.IsSet {
		delta = -1
	}
	incrementCounter(c.session, "comment", event.TargetID, "dislikes", delta)

	return nil
}

func (c *ScyllaLikeConsumer) handleShare(ctx context.Context, event *engagement.EngagementEvent) error {
	ts := event.ActionTS.UnixMicro()
	shard := engagement.ShareShard(event.UserID)

	if err := c.session.Query(`
		INSERT INTO post_shares (post_id, shard, user_id, created_at, share_type, quote_text)
		VALUES (?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`,
		event.PostID, shard, event.UserID, event.ActionTS, event.ShareType, event.QuoteText, ts,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("insert post_shares: %w", err)
	}

	if err := c.session.Query(`
		INSERT INTO user_shares (user_id, post_id, created_at, share_type)
		VALUES (?, ?, ?, ?) USING TIMESTAMP ?`,
		event.UserID, event.PostID, event.ActionTS, event.ShareType, ts,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("insert user_shares: %w", err)
	}

	incrementCounter(c.session, "post", event.PostID, "shares", 1)
	c.rdb.SAdd(ctx, "hot:posts", event.PostID.String())

	return nil
}

func (c *ScyllaLikeConsumer) handleBookmark(ctx context.Context, event *engagement.EngagementEvent) error {
	ts := event.ActionTS.UnixMicro()

	if event.IsSet {
		if err := c.session.Query(`
			INSERT INTO user_bookmarks (user_id, collection, created_at, post_id)
			VALUES (?, 'default', ?, ?) USING TIMESTAMP ?`,
			event.UserID, event.ActionTS, event.PostID, ts,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("insert user_bookmarks: %w", err)
		}

		if err := c.session.Query(`
			INSERT INTO bookmark_check (user_id, post_id, collection)
			VALUES (?, ?, 'default') USING TIMESTAMP ?`,
			event.UserID, event.PostID, ts,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("insert bookmark_check: %w", err)
		}
	} else {
		c.session.Query(`
			DELETE FROM user_bookmarks USING TIMESTAMP ?
			WHERE user_id = ? AND collection = 'default' AND created_at = ? AND post_id = ?`,
			ts, event.UserID, event.ActionTS, event.PostID,
		).WithContext(ctx).Exec()

		c.session.Query(`
			DELETE FROM bookmark_check USING TIMESTAMP ?
			WHERE user_id = ? AND post_id = ?`,
			ts, event.UserID, event.PostID,
		).WithContext(ctx).Exec()
	}

	delta := 1
	if !event.IsSet {
		delta = -1
	}
	incrementCounter(c.session, "post", event.PostID, "bookmarks", delta)

	return nil
}

// incrementCounter performs a read-then-CAS update on engagement_counters.
// Retries up to 3 times on CAS failures.
func incrementCounter(session *gocql.Session, targetType string, targetID uuid.UUID, counterType string, delta int) error {
	for retries := 0; retries < 3; retries++ {
		var current int64
		if err := session.Query(`
			SELECT count FROM engagement_counters
			WHERE target_type = ? AND target_id = ? AND counter_type = ?`,
			targetType, targetID, counterType,
		).Scan(&current); err != nil {
			// Row doesn't exist yet, initialize it
			current = 0
		}

		newCount := current + int64(delta)
		if newCount < 0 {
			newCount = 0
		}

		var applied bool
		if current == 0 && delta > 0 {
			// First like: try INSERT IF NOT EXISTS
			if err := session.Query(`
				INSERT INTO engagement_counters (target_type, target_id, counter_type, count, updated_at)
				VALUES (?, ?, ?, ?, toTimestamp(now()))
				IF NOT EXISTS`,
				targetType, targetID, counterType, newCount,
			).Scan(&applied, nil, nil, nil, nil, nil); err != nil {
				// If scan fails, try the UPDATE path
				applied = false
			}
			if applied {
				return nil
			}
			// Row was created concurrently, fall through to UPDATE
		}

		if err := session.Query(`
			UPDATE engagement_counters
			SET count = ?, updated_at = toTimestamp(now())
			WHERE target_type = ? AND target_id = ? AND counter_type = ?
			IF count = ?`,
			newCount, targetType, targetID, counterType, current,
		).Scan(&applied, nil); err != nil {
			return fmt.Errorf("counter CAS: %w", err)
		}

		if applied {
			return nil
		}

		// CAS failed, retry with fresh read
		time.Sleep(time.Duration(retries*10) * time.Millisecond)
	}

	return fmt.Errorf("counter update failed after 3 retries for %s:%s:%s", targetType, targetID, counterType)
}
