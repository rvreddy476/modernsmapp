//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/database"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MediaSubtitlesChanged end to end over a real store (2026-09-27).
//
//	POSTGRES_DSN=postgres://…/media_it_test go test -tags integration ./internal/store/postgres -run SubtitleStateEvent -v
//
// The database name must end in _test: this test writes media rows.
func subtitleEventsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", cfg.Database)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(context.Background(), pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return pool
}

type subtitleOutboxRow struct {
	eventID   string
	payload   sharedevents.MediaSubtitlesChangedPayload
	published bool
	occurred  time.Time
}

func readSubtitleOutbox(t *testing.T, pool *pgxpool.Pool, mediaID uuid.UUID) (subtitleOutboxRow, bool) {
	t.Helper()
	var row subtitleOutboxRow
	var raw []byte
	err := pool.QueryRow(context.Background(), `
		SELECT event_id, payload, published_at IS NOT NULL, occurred_at
		  FROM media_event_outbox WHERE media_asset_id = $1 AND event_type = $2`,
		mediaID, sharedevents.MediaSubtitlesChanged).Scan(&row.eventID, &raw, &row.published, &row.occurred)
	if err == pgx.ErrNoRows {
		return row, false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &row.payload); err != nil {
		t.Fatal(err)
	}
	return row, true
}

func TestSubtitleStateEventFollowsEveryPublishedChange(t *testing.T) {
	pool := subtitleEventsPool(t)
	store := New(pool)
	ctx := context.Background()

	m := &MediaAsset{
		ID: uuid.New(), UploaderID: uuid.New(), FileType: "video", MediaSubtype: "general",
		MimeType: "video/mp4", FileSizeBytes: 1024, StorageBucket: "test",
		StorageKey: "user/test/subs/original", ProcessingStatus: "ready", CreatedAt: time.Now(),
	}
	if err := store.CreateMedia(ctx, m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM media_event_outbox WHERE media_asset_id = $1`, m.ID)
		_, _ = pool.Exec(bg, `DELETE FROM media_subtitles WHERE media_asset_id = $1`, m.ID)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, m.ID)
	})

	// 1. A manual track upload is published on write.
	if _, err := store.CreateSubtitle(ctx, &MediaSubtitle{
		MediaAssetID: m.ID, Language: "en", Source: "manual", Format: "vtt", Content: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	first, ok := readSubtitleOutbox(t, pool, m.ID)
	if !ok || !first.payload.HasPublishedSubtitles || strings.Join(first.payload.Languages, ",") != "en" || first.published {
		t.Fatalf("after a manual upload: %+v (present=%v)", first, ok)
	}

	// A relay publishes it.
	if err := store.MarkMediaEventPublished(ctx, first.eventID); err != nil {
		t.Fatal(err)
	}

	// 2. The job's generated track becomes a draft — never counted.
	if _, err := store.CreateSubtitle(ctx, &MediaSubtitle{
		MediaAssetID: m.ID, Language: "hi", Source: "auto_generated", Format: "vtt", Content: "draft",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkGeneratedSubtitleDraft(ctx, m.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	second, _ := readSubtitleOutbox(t, pool, m.ID)
	if strings.Join(second.payload.Languages, ",") != "en" || second.published || second.eventID == first.eventID {
		t.Fatalf("a draft must not be listed; the snapshot must be a NEW pending event: %+v", second)
	}
	if !second.occurred.After(first.occurred) {
		t.Fatalf("a later snapshot must carry a later occurred_at: %v !> %v", second.occurred, first.occurred)
	}

	// 3. The owner's toggle: publish the draft, then unpublish both.
	if _, err := store.SetSubtitlePublished(ctx, m.ID, "hi", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := readSubtitleOutbox(t, pool, m.ID); strings.Join(got.payload.Languages, ",") != "en,hi" {
		t.Fatalf("after publishing hi: %+v", got.payload)
	}
	stale, _ := readSubtitleOutbox(t, pool, m.ID)
	for _, lang := range []string{"en", "hi"} {
		if _, err := store.SetSubtitlePublished(ctx, m.ID, lang, false); err != nil {
			t.Fatal(err)
		}
	}
	last, _ := readSubtitleOutbox(t, pool, m.ID)
	if last.payload.HasPublishedSubtitles || len(last.payload.Languages) != 0 || last.published {
		t.Fatalf("with every track unpublished: %+v", last)
	}

	// A relay that read an older snapshot cannot mark the newer one sent.
	if err := store.MarkMediaEventPublished(ctx, stale.eventID); err == nil {
		t.Fatal("marking a superseded event id must not succeed")
	}
	if again, _ := readSubtitleOutbox(t, pool, m.ID); again.published {
		t.Fatal("the current snapshot was marked published by a stale relay")
	}

	// 4. An owner correction may create a published manual track.
	if err := store.UpdateSubtitleContent(ctx, m.ID, "ta", "corrected", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := readSubtitleOutbox(t, pool, m.ID); strings.Join(got.payload.Languages, ",") != "ta" {
		t.Fatalf("after a correction created ta: %+v", got.payload)
	}

	// A toggle on a track that does not exist changes nothing and records
	// nothing new.
	before, _ := readSubtitleOutbox(t, pool, m.ID)
	if _, err := store.SetSubtitlePublished(ctx, m.ID, "fr", true); err == nil {
		t.Fatal("toggling a missing track must fail")
	}
	if after, _ := readSubtitleOutbox(t, pool, m.ID); after.eventID != before.eventID {
		t.Fatal("a failed toggle replaced the snapshot")
	}
}
