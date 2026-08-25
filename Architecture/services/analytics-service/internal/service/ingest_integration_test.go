//go:build integration

package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveIngestUsesCanonicalAttributionAndDurableDedupe(t *testing.T) {
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
	creator := uuid.New()
	forgedCreator := uuid.New()
	actor := uuid.New()
	forgedViewer := uuid.New()
	content := uuid.New()
	session := uuid.New()
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "long_video", CreatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	svc := New(ctx, store, nil)
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]any{
		"content_id": content.String(), "creator_id": forgedCreator.String(),
		"viewer_id": forgedViewer.String(), "session_id": session.String(),
		"content_type": "reel", "content_duration_ms": 60_000,
		"watched_ms_total": 45_000, "percent_viewed": 1,
		"loop_count": 0, "end_reason": "ended", "surface": "posttube",
	})
	dto := EventDTO{EventID: "event-00000000000000000001", Type: "play_end", Payload: payload, Timestamp: now}
	result, err := svc.IngestEvents(ctx, actor.String(), []EventDTO{dto})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Duplicate != 0 {
		t.Fatalf("first result=%+v", result)
	}

	var persistedActor uuid.UUID
	var persisted map[string]any
	if err := pool.QueryRow(ctx, `SELECT user_id, payload FROM analytics.events_raw WHERE type='play_end'`).Scan(&persistedActor, &persisted); err != nil {
		t.Fatal(err)
	}
	if persistedActor != actor {
		t.Fatalf("actor=%s want=%s", persistedActor, actor)
	}
	if persisted["creator_id"] != creator.String() {
		t.Fatalf("creator attribution=%v want=%s", persisted["creator_id"], creator)
	}
	if _, exists := persisted["viewer_id"]; exists {
		t.Fatalf("client viewer_id survived sanitization: %v", persisted)
	}
	if persisted["content_type"] != "long_video" {
		t.Fatalf("content type=%v", persisted["content_type"])
	}

	result, err = svc.IngestEvents(ctx, actor.String(), []EventDTO{dto})
	if err != nil || result.Accepted != 0 || result.Duplicate != 1 {
		t.Fatalf("same-event retry result=%+v err=%v", result, err)
	}
	dto.EventID = "event-00000000000000000002"
	result, err = svc.IngestEvents(ctx, actor.String(), []EventDTO{dto})
	if err != nil || result.Accepted != 0 || result.Duplicate != 1 {
		t.Fatalf("same session/content retry result=%+v err=%v", result, err)
	}
	var eventCount, receiptCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.events_raw`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.ingest_receipts`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || receiptCount != 1 {
		t.Fatalf("events=%d receipts=%d", eventCount, receiptCount)
	}

	unknownPayload, _ := json.Marshal(map[string]any{
		"content_id": uuid.New().String(), "session_id": uuid.New().String(),
		"content_duration_ms": 10_000, "watched_ms_total": 5_000,
	})
	_, err = svc.IngestEvents(ctx, actor.String(), []EventDTO{{
		EventID: "event-00000000000000000003", Type: "play_end",
		Payload: unknownPayload, Timestamp: now,
	}})
	if err == nil {
		t.Fatal("unknown content was accepted")
	}
}
