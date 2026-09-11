//go:build integration

package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/service"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func engagementPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testsupport.RequireTestDSN(t)
	ctx := context.Background()
	// Own database, own truncates: see internal/testsupport.
	pool := testsupport.Pool(t, "consumers")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.ingest_receipts, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func projectContent(t *testing.T, store *pgstore.Store, content, creator uuid.UUID) {
	t.Helper()
	if err := store.UpsertContentOwnership(context.Background(), pgstore.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "flick", CreatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func postReacted(reactor, post, author uuid.UUID) events.EventEnvelope {
	payload, _ := json.Marshal(events.PostReactedPayload{
		PostID: post.String(), PostAuthorID: author.String(), ReactorID: reactor.String(),
		ReactType: "like", CreatedAt: time.Now().UTC(),
	})
	return events.EventEnvelope{
		EventID: uuid.New().String(), EventType: events.PostReacted,
		OccurredAt: time.Now().UTC(), Payload: payload,
	}
}

func likeRows(t *testing.T, pool *pgxpool.Pool, actor, content uuid.UUID) (raw, receipts int) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw
		WHERE type = 'like' AND user_id = $1 AND payload->>'content_id' = $2`,
		actor, content.String()).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.ingest_receipts
		WHERE event_type = 'like' AND actor_id = $1 AND content_id = $2`,
		actor, content).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	return raw, receipts
}

// The bug this pins down (audit M-05): a web client's like reached
// events_raw twice — once over HTTP with its own receipt, once from
// post-service's PostReacted through this consumer with a bare INSERT
// and no receipt — and both fed the quality score. One like, one row,
// whichever door it comes through first.
func TestKafkaAndHTTPLikeCollapseToOneRow(t *testing.T) {
	ctx := context.Background()
	pool := engagementPool(t)
	store := pgstore.New(pool)
	creator, actor := uuid.New(), uuid.New()
	svc := service.New(ctx, store, nil)
	consumer := NewEngagementConsumer(pool, nil)

	httpLike := func(content uuid.UUID, eventID string) {
		t.Helper()
		// An old client: the like still arrives inside a playback session.
		payload, _ := json.Marshal(map[string]any{
			"content_id": content.String(), "session_id": uuid.New().String(), "surface": "feed",
		})
		result, err := svc.IngestEvents(ctx, actor.String(), []service.EventDTO{{
			EventID: eventID, Type: "like", Payload: payload, Timestamp: time.Now().UTC(),
		}})
		if err != nil {
			t.Fatalf("http like: %v", err)
		}
		if result.Accepted+result.Duplicate != 1 {
			t.Fatalf("http like result=%+v", result)
		}
	}

	// HTTP first, then Kafka.
	first := uuid.New()
	projectContent(t, store, first, creator)
	httpLike(first, "evt-http-like-first-0000001")
	env := postReacted(actor, first, creator)
	if err := consumer.handlePostReacted(ctx, &env); err != nil {
		t.Fatalf("kafka like after http: %v", err)
	}
	if raw, receipts := likeRows(t, pool, actor, first); raw != 1 || receipts != 1 {
		t.Fatalf("http then kafka: like rows=%d receipts=%d, want 1 and 1", raw, receipts)
	}

	// Kafka first, then HTTP — and the HTTP copy from a second session.
	second := uuid.New()
	projectContent(t, store, second, creator)
	env = postReacted(actor, second, creator)
	if err := consumer.handlePostReacted(ctx, &env); err != nil {
		t.Fatalf("kafka like: %v", err)
	}
	httpLike(second, "evt-http-like-second-000001")
	httpLike(second, "evt-http-like-second-000002")
	if raw, receipts := likeRows(t, pool, actor, second); raw != 1 || receipts != 1 {
		t.Fatalf("kafka then http x2: like rows=%d receipts=%d, want 1 and 1", raw, receipts)
	}

	// A different viewer's like on the same content is its own row.
	other := postReacted(uuid.New(), second, creator)
	if err := consumer.handlePostReacted(ctx, &other); err != nil {
		t.Fatal(err)
	}
	var all int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw
		WHERE type = 'like' AND payload->>'content_id' = $1`, second.String()).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if all != 2 {
		t.Fatalf("two viewers' likes=%d want 2", all)
	}
}

// Kafka is at-least-once and the consumer used to lean on a Redis SETNX
// to notice a redelivery. The receipt keyed on the outbox event id is
// the dedupe now, so the same PostReacted twice is one row with no
// Redis at all — and a reaction that beat PostCreated is retried, not
// committed and lost.
func TestPostReactedRedeliveryIsIdempotentWithoutRedis(t *testing.T) {
	ctx := context.Background()
	pool := engagementPool(t)
	store := pgstore.New(pool)
	creator, actor, content := uuid.New(), uuid.New(), uuid.New()
	consumer := NewEngagementConsumer(pool, nil) // no Redis

	// Before the ownership projection exists: retryable, nothing written.
	env := postReacted(actor, content, creator)
	err := consumer.handlePostReacted(ctx, &env)
	var retryable retryableEngagementError
	if !errors.As(err, &retryable) {
		t.Fatalf("reaction before PostCreated: err=%v, want a retryable error", err)
	}
	if raw, receipts := likeRows(t, pool, actor, content); raw != 0 || receipts != 0 {
		t.Fatalf("unprojected content wrote rows=%d receipts=%d", raw, receipts)
	}

	// PostCreated lands; the same record is delivered three times.
	projectContent(t, store, content, creator)
	for i := range 3 {
		if err := consumer.handlePostReacted(ctx, &env); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	raw, receipts := likeRows(t, pool, actor, content)
	if raw != 1 || receipts != 1 {
		t.Fatalf("three deliveries: like rows=%d receipts=%d, want 1 and 1", raw, receipts)
	}

	// The receipt is the outbox id, so the row is traceable to the event.
	var eventID string
	if err := pool.QueryRow(ctx, `SELECT event_id FROM analytics.ingest_receipts
		WHERE event_type = 'like' AND actor_id = $1 AND content_id = $2`, actor, content).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if eventID != "kafka:"+env.EventID {
		t.Fatalf("receipt event_id=%q want kafka:%s", eventID, env.EventID)
	}

	// Attribution comes from the projection, and the nil session from
	// the normaliser, exactly as for an HTTP like.
	var persisted map[string]any
	if err := pool.QueryRow(ctx, `SELECT payload FROM analytics.events_raw
		WHERE type = 'like' AND user_id = $1`, actor).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["creator_id"] != creator.String() || persisted["content_type"] != "flick" || persisted["session_id"] != uuid.Nil.String() {
		t.Fatalf("persisted like payload=%v", persisted)
	}
}
