package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/scoring"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// engagementStore is the slice of the PostgreSQL store the consumer
// writes through. The same InsertAcceptedBatch the HTTP ingest path
// uses, so a like has exactly one write path whichever door it came in.
type engagementStore interface {
	GetContentOwnership(ctx context.Context, contentID uuid.UUID) (pgstore.ContentOwnership, error)
	InsertAcceptedBatch(ctx context.Context, events []pgstore.Event) ([]pgstore.Event, error)
}

// EngagementConsumer processes social engagement events (likes, comments)
// from Kafka into analytics.events_raw for the hourly aggregator, and
// keeps the near-real-time CQS estimate in Redis warm.
//
// Before plan Phase 1B it wrote likes with a bare INSERT — a fresh uuid,
// no receipt, no session — while the HTTP ingest path wrote the same
// like with its own receipt. Every web like was counted twice (audit
// M-05). Now both paths go through InsertAcceptedBatch with the same
// receipt shape: (actor, nil session, content, 'like', 'content'), and
// the partial unique index on ingest_receipts folds them onto one row.
// The receipt's event id is "kafka:" + the envelope's event id, which
// is the outbox id and stable across redelivery, so a redelivered
// PostReacted is a duplicate at the database and needs no Redis to say
// so.
type EngagementConsumer struct {
	pg    *pgxpool.Pool
	store engagementStore
	rdb   *redis.Client
	now   func() time.Time
}

func NewEngagementConsumer(pg *pgxpool.Pool, rdb *redis.Client) *EngagementConsumer {
	c := &EngagementConsumer{pg: pg, rdb: rdb, now: time.Now}
	if pg != nil {
		c.store = pgstore.New(pg)
	}
	return c
}

// retryableEngagementError marks a failure that a later attempt can
// clear: the reaction beat PostCreated to us, so the ownership row (and
// the receipt's foreign key target) is not there yet. The record stays
// in flight and is retried, never committed and dropped.
type retryableEngagementError struct{ err error }

func (e retryableEngagementError) Error() string { return e.err.Error() }
func (e retryableEngagementError) Unwrap() error { return e.err }

// Start launches the Kafka consumer loop. Blocks until ctx is cancelled.
func (c *EngagementConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  "analytics-engagement",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6, // 10 MB
		Dialer:   dialer,
	})
	defer reader.Close()

	log.Println("[EngagementConsumer] started (writes through ingest receipts)")

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

		for {
			err := c.processEvent(ctx, &envelope)
			var retryable retryableEngagementError
			if err != nil && errors.As(err, &retryable) {
				log.Printf("[EngagementConsumer] %s %s not yet applicable; retrying same record: %v",
					envelope.EventType, envelope.EventID, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(250 * time.Millisecond):
				}
				continue
			}
			if err != nil {
				log.Printf("[EngagementConsumer] process error for %s: %v", envelope.EventType, err)
			}
			break
		}

		_ = reader.CommitMessages(ctx, msg)
	}
}

func isEngagementEvent(eventType string) bool {
	switch eventType {
	case events.PostReacted, events.CommentCreated, events.CommentReacted,
		events.EventUserDeletionRequested:
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
	case events.EventUserDeletionRequested:
		return c.handleUserDeletionRequested(ctx, env)
	}
	return nil
}

func (c *EngagementConsumer) handleUserDeletionRequested(ctx context.Context, env *events.EventEnvelope) error {
	var p events.UserDeletionRequestedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	_, err := c.pg.Exec(ctx,
		`DELETE FROM analytics.events_raw WHERE user_id = $1::uuid`, p.UserID)
	if err != nil {
		log.Printf("[EngagementConsumer] failed to delete analytics events for user %s: %v", p.UserID, err)
	}
	return err
}

func (c *EngagementConsumer) handlePostReacted(ctx context.Context, env *events.EventEnvelope) error {
	var p events.PostReactedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	key := pgstore.LikeDedupeKey
	inserted, err := c.writeEngagement(ctx, env, model.EventLike, p.ReactorID, p.PostID, &key)
	if err != nil {
		return err
	}
	if !inserted {
		return nil // a redelivery, or the HTTP path got there first
	}
	return c.bumpCQS(ctx, p.PostID, "likes")
}

func (c *EngagementConsumer) handleCommentCreated(ctx context.Context, env *events.EventEnvelope) error {
	var p events.CommentCreatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	// Comments are genuinely repeatable, so no dedupe key beyond the
	// event id itself.
	inserted, err := c.writeEngagement(ctx, env, model.EventCommentCreate, p.AuthorID, p.PostID, nil)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	return c.bumpCQS(ctx, p.PostID, "comments")
}

// writeEngagement is the one write path. Attribution comes from the
// ownership projection, exactly as it does for an HTTP client; the
// payload's post_author_id is not trusted for the same reason a client's
// creator_id is not. Returns whether a row was written (false for a
// duplicate).
func (c *EngagementConsumer) writeEngagement(ctx context.Context, env *events.EventEnvelope, eventType, actor, content string, dedupeKey *string) (bool, error) {
	if c.store == nil {
		return false, errors.New("engagement consumer has no store")
	}
	actorID, err := uuid.Parse(actor)
	if err != nil || actorID == uuid.Nil {
		return false, fmt.Errorf("%s has invalid actor id %q", env.EventType, actor)
	}
	contentID, err := uuid.Parse(content)
	if err != nil || contentID == uuid.Nil {
		return false, fmt.Errorf("%s has invalid post id %q", env.EventType, content)
	}
	clientEventID, err := kafkaEventID(env.EventID)
	if err != nil {
		return false, err
	}

	ownership, err := c.store.GetContentOwnership(ctx, contentID)
	if errors.Is(err, pgstore.ErrContentNotProjected) {
		return false, retryableEngagementError{fmt.Errorf("content %s: %w", contentID, err)}
	}
	if err != nil {
		return false, err
	}

	now := c.now().UTC()
	occurredAt := env.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = now
	}
	payload, err := json.Marshal(map[string]any{
		"content_id":   ownership.ContentID.String(),
		"creator_id":   ownership.CreatorID.String(),
		"content_type": ownership.ContentType,
		"session_id":   uuid.Nil.String(),
		"surface":      "other",
		"event_name":   eventType,
		"is_self_view": actorID == ownership.CreatorID,
		"source":       "kafka",
	})
	if err != nil {
		return false, err
	}

	inserted, err := c.store.InsertAcceptedBatch(ctx, []pgstore.Event{{
		ID:            uuid.New(),
		ClientEventID: clientEventID,
		UserID:        actorID,
		SessionID:     uuid.Nil,
		ContentID:     ownership.ContentID,
		Type:          eventType,
		DedupeKey:     dedupeKey,
		Payload:       payload,
		Timestamp:     occurredAt,
		ReceivedAt:    now,
	}})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			// The ownership row vanished between the lookup and the
			// receipt, or was never there on this replica yet.
			return false, retryableEngagementError{err}
		}
		return false, err
	}
	return len(inserted) > 0, nil
}

// kafkaEventID derives the receipt id from the envelope's event id. The
// outbox id is stable across redelivery, which is the whole point; an
// envelope without one cannot be made idempotent and is refused rather
// than written under a fresh id every time it is redelivered.
func kafkaEventID(envelopeID string) (string, error) {
	const prefix = "kafka:"
	if envelopeID == "" {
		return "", errors.New("engagement envelope has no event_id; cannot be written idempotently")
	}
	id := prefix + envelopeID
	// ingest_receipts.event_id is CHECKed to 16..128 characters.
	if len(id) < 16 || len(id) > 128 {
		return "", fmt.Errorf("engagement envelope event_id %q is not a usable receipt id", envelopeID)
	}
	return id, nil
}

// bumpCQS incrementally updates the cached CQS for a post. Every 10
// engagement events it reads the latest aggregate from Postgres, recomputes
// CQS, and caches the result in Redis. Without Redis the durable write
// above has already happened; the estimate is simply not warmed.
func (c *EngagementConsumer) bumpCQS(ctx context.Context, postID, counterType string) error {
	if c.rdb == nil {
		return nil
	}
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
