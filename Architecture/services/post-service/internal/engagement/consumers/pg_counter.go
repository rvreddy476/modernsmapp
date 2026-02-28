package consumers

import (
	"context"
	"log"

	"github.com/facebook-like/post-service/internal/engagement"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// PGCounterConsumer updates PostgreSQL post_engagement_counts table.
// Uses engagement_event_log for dedup — single transaction: insert event + update counter.
type PGCounterConsumer struct {
	db   *pgxpool.Pool
	base *engagement.BaseConsumer
}

// NewPGCounterConsumer creates a new PG counter consumer.
func NewPGCounterConsumer(db *pgxpool.Pool, rdb *redis.Client) *PGCounterConsumer {
	return &PGCounterConsumer{
		db:   db,
		base: engagement.NewBaseConsumer(rdb, "pg-counter"),
	}
}

// Start begins the consumer loop. Blocks until ctx is canceled.
func (c *PGCounterConsumer) Start(ctx context.Context, brokers []string, topic string) {
	reader := engagement.NewKafkaReader(brokers, topic, "eng-pg-counter")
	defer reader.Close()

	engagement.ConsumerLoop(ctx, reader, c.base, c.handleEvent)
}

func (c *PGCounterConsumer) handleEvent(ctx context.Context, event *engagement.EngagementEvent) error {
	switch event.EventType {
	case engagement.EventPostLiked, engagement.EventPostUnliked:
		return c.updateCounter(ctx, event, "like_count")
	case engagement.EventCommentCreated, engagement.EventCommentDeleted:
		return c.updateCounter(ctx, event, "comment_count")
	case engagement.EventPostShared:
		return c.updateCounter(ctx, event, "share_count")
	case engagement.EventPostBookmarked, engagement.EventPostUnbookmarked:
		return c.updateCounter(ctx, event, "bookmark_count")
	default:
		return nil
	}
}

func (c *PGCounterConsumer) updateCounter(ctx context.Context, event *engagement.EngagementEvent, column string) error {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Dedup: insert event_id, skip if already exists
	tag, err := tx.Exec(ctx, `
		INSERT INTO engagement_event_log (event_id, event_type, target_id)
		VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, event.EventType, event.PostID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Duplicate event, skip
		return nil
	}

	delta := 1
	if !event.IsSet {
		delta = -1
	}

	// Ensure row exists (in case trigger hasn't fired yet)
	_, err = tx.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id)
		VALUES ($1) ON CONFLICT (post_id) DO NOTHING`,
		event.PostID,
	)
	if err != nil {
		log.Printf("[pg-counter] ensure row: %v", err)
	}

	// Update the specific counter column
	query := `UPDATE post_engagement_counts SET ` + column + ` = GREATEST(` + column + ` + $2, 0), updated_at = now() WHERE post_id = $1`
	_, err = tx.Exec(ctx, query, event.PostID, delta)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
