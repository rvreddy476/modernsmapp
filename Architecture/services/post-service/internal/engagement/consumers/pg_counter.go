package consumers

import (
	"context"
	"fmt"
	"log"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/counters"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// PGCounterConsumer materializes engagement events into the
// post_engagement_counts row. Historically this fired a per-event
// `UPDATE post_engagement_counts SET <col> = <col> + delta` which made
// every like/share/comment/bookmark serialize on the single counter
// row — a celebrity post turned that row into the platform's #1 lock
// contention point.
//
// The new path bumps a sharded Redis counter (counters.Counter) and
// the matching counters.Worker periodically materializes the shard sum
// back to PG. The engagement_event_log dedup table still guards
// against double-applied events. When the counter handle is nil (no
// Redis, e.g. dev) we fall back to the legacy per-event UPDATE so the
// dev loop still works.
type PGCounterConsumer struct {
	db    *pgxpool.Pool
	store *postgres.Store
	base  *engagement.BaseConsumer

	likeCounter     *counters.Counter
	commentCounter  *counters.Counter
	shareCounter    *counters.Counter
	bookmarkCounter *counters.Counter
}

// NewPGCounterConsumer creates a new PG counter consumer. Pass nil for
// any *counters.Counter handle to keep that column on the legacy
// per-event UPDATE path (handy in tests or when the matching flush
// worker isn't wired).
func NewPGCounterConsumer(
	db *pgxpool.Pool,
	store *postgres.Store,
	rdb *redis.Client,
	likeC, commentC, shareC, bookmarkC *counters.Counter,
) *PGCounterConsumer {
	return &PGCounterConsumer{
		db:              db,
		store:           store,
		base:            engagement.NewBaseConsumer(rdb, "pg-counter"),
		likeCounter:     likeC,
		commentCounter:  commentC,
		shareCounter:    shareC,
		bookmarkCounter: bookmarkC,
	}
}

// Start begins the consumer loop. Blocks until ctx is canceled.
func (c *PGCounterConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := engagement.NewKafkaReaderWithDialer(brokers, topic, "eng-pg-counter", dialer)
	defer reader.Close()

	engagement.ConsumerLoop(ctx, reader, c.base, c.handleEvent)
}

func (c *PGCounterConsumer) handleEvent(ctx context.Context, event *engagement.EngagementEvent) error {
	var col string
	var counter *counters.Counter
	switch event.EventType {
	case engagement.EventPostLiked, engagement.EventPostUnliked:
		col, counter = "like_count", c.likeCounter
	case engagement.EventCommentCreated, engagement.EventCommentDeleted:
		col, counter = "comment_count", c.commentCounter
	case engagement.EventPostShared:
		col, counter = "share_count", c.shareCounter
	case engagement.EventPostBookmarked, engagement.EventPostUnbookmarked:
		col, counter = "bookmark_count", c.bookmarkCounter
	default:
		return nil
	}
	return c.updateCounter(ctx, event, col, counter)
}

func (c *PGCounterConsumer) updateCounter(ctx context.Context, event *engagement.EngagementEvent, column string, counter *counters.Counter) error {
	// Dedup: insert event_id, skip if already exists. We keep this on
	// PG because the dedup needs to survive a Redis flush — the
	// counter shards are buffer, PG is truth.
	tag, err := c.db.Exec(ctx, `
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

	var delta int64 = 1
	if !event.IsSet {
		delta = -1
	}

	// Ensure row exists (in case trigger hasn't fired yet) — the flush
	// worker writes via SetEngagementCount which INSERTs on conflict,
	// but creating the row up front keeps reads from any other path
	// from racing on a missing row.
	_, err = c.db.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id)
		VALUES ($1) ON CONFLICT (post_id) DO NOTHING`,
		event.PostID,
	)
	if err != nil {
		log.Printf("[pg-counter] ensure row: %v", err)
	}

	if counter != nil {
		if err := counter.Inc(ctx, event.PostID.String(), delta); err != nil {
			// Counter path failed — fall through to the legacy UPDATE
			// so the event isn't lost. The dedup row above guarantees
			// we won't double-apply.
			log.Printf("[pg-counter] sharded counter inc failed (%s post=%s): %v — falling back to PG UPDATE",
				column, event.PostID, err)
			return c.store.IncrementEngagementCount(ctx, event.PostID, column, delta)
		}
		return nil
	}

	// Legacy per-event UPDATE path. Same SQL as before the
	// sharded-counter migration so existing tests + dev loops behave
	// identically when Redis is unavailable.
	return c.legacyUpdate(ctx, event.PostID, column, delta)
}

func (c *PGCounterConsumer) legacyUpdate(ctx context.Context, postID interface{}, column string, delta int64) error {
	if !validColumns[column] {
		return fmt.Errorf("invalid engagement column: %s", column)
	}
	query := fmt.Sprintf(
		`UPDATE post_engagement_counts SET %s = GREATEST(%s + $2, 0), updated_at = now() WHERE post_id = $1`,
		column, column,
	)
	_, err := c.db.Exec(ctx, query, postID, delta)
	return err
}

var validColumns = map[string]bool{
	"like_count":     true,
	"comment_count":  true,
	"share_count":    true,
	"bookmark_count": true,
	"repost_count":   true,
}
