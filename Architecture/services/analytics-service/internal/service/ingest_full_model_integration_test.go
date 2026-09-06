//go:build integration

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	"github.com/atpost/analytics-service/internal/model"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A realistic watch session posted through the ingest service: every one
// of the thirteen event types, persisted and readable back, then the
// same batch replayed to prove the dedupe is durable rather than
// in-memory.
func TestLiveIngestPersistsEveryEventTypeAndDedupesOnReplay(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ANALYTICS_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.ingest_receipts, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}

	store := pgstore.New(pool)
	creator, actor := uuid.New(), uuid.New()
	content, session := uuid.New(), uuid.New()
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "long_video",
		CreatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	svc := New(ctx, store, nil)
	now := time.Now().UTC()

	seq := 0
	eventID := func() string {
		seq++
		return fmt.Sprintf("evt-full-model-%016d", seq)
	}
	build := func(eventType string, extra map[string]any) EventDTO {
		payload := map[string]any{
			"content_id": content.String(),
			"session_id": session.String(),
			"surface":    "posttube",
			// Forged attribution, present on every event, must never survive.
			"creator_id": uuid.New().String(),
			"viewer_id":  uuid.New().String(),
		}
		for k, v := range extra {
			payload[k] = v
		}
		raw, _ := json.Marshal(payload)
		return EventDTO{EventID: eventID(), Type: eventType, Payload: raw, Timestamp: now}
	}

	batch := []EventDTO{
		build(model.EventImpression, map[string]any{"visible_ms": 800}),
		build(model.EventPlayStart, map[string]any{"content_duration_ms": 200_000, "start_method": "tap"}),
		build(model.EventWatchHeartbeat, map[string]any{"watched_ms_increment": 5000, "watched_ms_total": 5000, "playhead_position_ms": 5000}),
		build(model.EventWatchHeartbeat, map[string]any{"watched_ms_increment": 5000, "watched_ms_total": 10_000, "playhead_position_ms": 10_000}),
		build(model.EventMilestone, map[string]any{"milestone_type": "VIEW_10S", "watched_ms": 10_000}),
		build(model.EventMilestone, map[string]any{"milestone_type": "PCT_50", "watched_ms": 100_000}),
		build(model.EventPlayEnd, map[string]any{
			"content_duration_ms": 200_000, "watched_ms_total": 180_000,
			"max_continuous_watch_ms": 150_000, "percent_viewed": 90.0,
			"loop_count": 0, "end_reason": "ended",
		}),
		build(model.EventLike, nil),
		build(model.EventCommentCreate, nil),
		build(model.EventShare, nil),
		build(model.EventSave, nil),
		build(model.EventFollowFromContent, nil),
		build(model.EventNotInterested, map[string]any{"reason": "repetitive"}),
		build(model.EventReport, map[string]any{"reason": "spam"}),
		build(model.EventBlockCreator, map[string]any{"reason": "dislike_creator"}),
	}

	result, err := svc.IngestEvents(ctx, actor.String(), batch)
	if err != nil {
		t.Fatalf("ingest rejected the batch: %v", err)
	}
	if result.Accepted != len(batch) || result.Duplicate != 0 {
		t.Fatalf("first ingest = %+v, want accepted=%d duplicate=0", result, len(batch))
	}

	// Every declared type must be readable back out of events_raw.
	rows, err := pool.Query(ctx, `SELECT type, count(*) FROM analytics.events_raw GROUP BY type`)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]int{}
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			t.Fatal(err)
		}
		stored[typ] = n
	}
	rows.Close()
	for eventType := range model.VideoEventNames {
		if stored[eventType] == 0 {
			t.Errorf("event type %q was accepted but not persisted", eventType)
		}
	}
	if stored[model.EventWatchHeartbeat] != 2 {
		t.Errorf("heartbeats stored=%d, want 2 — repeated heartbeats must not collapse",
			stored[model.EventWatchHeartbeat])
	}
	if stored[model.EventMilestone] != 2 {
		t.Errorf("milestones stored=%d, want 2 — different thresholds are different signals",
			stored[model.EventMilestone])
	}

	// Attribution came from the projection, never from the client.
	var badAttribution int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM analytics.events_raw
		WHERE payload->>'creator_id' <> $1 OR payload ? 'viewer_id'`, creator.String()).Scan(&badAttribution); err != nil {
		t.Fatal(err)
	}
	if badAttribution != 0 {
		t.Fatalf("%d rows carry client-supplied attribution", badAttribution)
	}

	// Replay the identical batch: same event_ids, so every one is a
	// duplicate and nothing new is written.
	replay, err := svc.IngestEvents(ctx, actor.String(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Accepted != 0 || replay.Duplicate != len(batch) {
		t.Fatalf("replay = %+v, want accepted=0 duplicate=%d", replay, len(batch))
	}

	var totalEvents, totalReceipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw`).Scan(&totalEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.ingest_receipts`).Scan(&totalReceipts); err != nil {
		t.Fatal(err)
	}
	if totalEvents != len(batch) || totalReceipts != len(batch) {
		t.Fatalf("after replay events=%d receipts=%d, want %d/%d",
			totalEvents, totalReceipts, len(batch), len(batch))
	}

	// A fresh event_id for a signal that can only happen once per
	// session — a double-tapped like — is still a duplicate, because
	// counting it twice would inflate the quality score.
	doubleTap := build(model.EventLike, nil)
	again, err := svc.IngestEvents(ctx, actor.String(), []EventDTO{doubleTap})
	if err != nil {
		t.Fatal(err)
	}
	if again.Accepted != 0 || again.Duplicate != 1 {
		t.Fatalf("double-tapped like = %+v, want accepted=0 duplicate=1", again)
	}

	// Whereas a second comment in the same session is a real second
	// comment and must be accepted.
	secondComment := build(model.EventCommentCreate, nil)
	more, err := svc.IngestEvents(ctx, actor.String(), []EventDTO{secondComment})
	if err != nil {
		t.Fatal(err)
	}
	if more.Accepted != 1 || more.Duplicate != 0 {
		t.Fatalf("second comment = %+v, want accepted=1 duplicate=0", more)
	}

	// A mixed batch reports both halves of the count.
	mixed := []EventDTO{
		build(model.EventImpression, map[string]any{"visible_ms": 500}),
		batch[0], // already ingested
	}
	mixedResult, err := svc.IngestEvents(ctx, actor.String(), mixed)
	if err != nil {
		t.Fatal(err)
	}
	if mixedResult.Accepted != 1 || mixedResult.Duplicate != 1 {
		t.Fatalf("mixed batch = %+v, want accepted=1 duplicate=1", mixedResult)
	}
}

// An event about content the ownership projection has never seen is
// refused: analytics must never invent an attribution.
func TestLiveIngestRefusesUnprojectedContentForEveryType(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ANALYTICS_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}

	svc := New(ctx, pgstore.New(pool), nil)
	now := time.Now().UTC()
	seq := 0
	for eventType := range model.VideoEventNames {
		seq++
		payload, _ := json.Marshal(map[string]any{
			"content_id": uuid.New().String(), "session_id": uuid.New().String(),
			"content_duration_ms": 10_000, "watched_ms_total": 5_000,
			"milestone_type": "PCT_50", "visible_ms": 500,
		})
		_, err := svc.IngestEvents(ctx, uuid.New().String(), []EventDTO{{
			EventID: fmt.Sprintf("evt-unprojected-%016d", seq), Type: eventType,
			Payload: payload, Timestamp: now,
		}})
		if err == nil {
			t.Errorf("%s about unprojected content was accepted", eventType)
		}
	}
}
