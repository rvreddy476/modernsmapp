//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Draft captions against a real store (MTube, 2026-09-27): the service's
// viewer reads, end to end over media_subtitles.published.
//
//	POSTGRES_DSN=postgres://…/media_it_test go test -tags integration ./internal/service -run DraftCaptions -v
//
// The database name must end in _test.
func TestDraftCaptionsViewerReads(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test (e.g. media_it_test)", cfg.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}

	owner, stranger, mediaID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets (
			id, uploader_id, file_type, media_subtype, mime_type,
			file_size_bytes, storage_bucket, storage_key,
			processing_status, moderation_status, created_at, updated_at
		) VALUES ($1,$2,'video','post','video/mp4',100,'media','protected/post/clip.mp4','ready','passed',NOW(),NOW())`,
		mediaID, owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, mediaID) })
	for _, row := range []struct {
		lang, source, content string
		published             bool
	}{
		{"en", "manual", "published words", true},
		{"hi", "auto_generated", "draft words", false},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_subtitles (media_asset_id, language, source, content_url, content, published)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			mediaID, row.lang, row.source, SubtitleTrackPath(mediaID, row.lang), row.content, row.published); err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{pgStore: postgres.New(pool)}

	langs := func(viewer uuid.UUID) string {
		subs, err := svc.ViewerSubtitles(ctx, viewer, mediaID)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(subs))
		for i, s := range subs {
			out[i] = s.Language
		}
		return strings.Join(out, ",")
	}
	if got := langs(owner); got != "en,hi" {
		t.Fatalf("owner list %q", got)
	}
	if got := langs(stranger); got != "en" {
		t.Fatalf("stranger list %q", got)
	}
	if got := langs(uuid.Nil); got != "en" {
		t.Fatalf("anonymous list %q", got)
	}

	if _, err := svc.ViewerCaptionTrackVTT(ctx, stranger, mediaID, "hi"); !errors.Is(err, ErrCaptionTrackNotFound) {
		t.Fatalf("stranger draft track: %v, want ErrCaptionTrackNotFound", err)
	}
	if body, err := svc.ViewerCaptionTrackVTT(ctx, owner, mediaID, "hi"); err != nil || !strings.Contains(body, "draft words") {
		t.Fatalf("owner draft track: %q %v", body, err)
	}
	if body, err := svc.ViewerCaptionTrackVTT(ctx, stranger, mediaID, "en"); err != nil || !strings.Contains(body, "published words") {
		t.Fatalf("stranger published track: %q %v", body, err)
	}
}
