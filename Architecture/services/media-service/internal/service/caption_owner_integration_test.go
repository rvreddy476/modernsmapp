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

// Gate line 3 (long video), the caption owner-refusal stranger test: a
// non-owner cannot create, replace, unpublish (the caption "delete": there
// is no DELETE route) or correct captions on someone else's video, and the
// owner's track is untouched by the attempts; the owner succeeds.
//
//	POSTGRES_DSN=postgres://…/media_it_test go test -tags integration ./internal/service -run CaptionWritesStranger -v
func TestCaptionWritesStrangerIsRefusedOwnerSucceeds(t *testing.T) {
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
		) VALUES ($1,$2,'video','post','video/mp4',100,'media','protected/post/long.mp4','ready','passed',NOW(),NOW())`,
		mediaID, owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, mediaID) })
	svc := &Service{pgStore: postgres.New(pool)}

	track := func(content string) *postgres.MediaSubtitle {
		return &postgres.MediaSubtitle{MediaAssetID: mediaID, Language: "en", Source: "manual", Format: "vtt", Content: content}
	}
	content := func() string {
		var c string
		if err := pool.QueryRow(ctx, `SELECT content FROM media_subtitles WHERE media_asset_id = $1 AND language = 'en'`, mediaID).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// Create: stranger refused, nothing written; owner succeeds.
	if _, err := svc.CreateSubtitle(ctx, stranger, track("stranger words")); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("stranger create: %v, want ErrNotMediaOwner", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_subtitles WHERE media_asset_id = $1`, mediaID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("stranger create wrote %d rows (%v)", n, err)
	}
	if _, err := svc.CreateSubtitle(ctx, owner, track("owner words")); err != nil {
		t.Fatalf("owner create: %v", err)
	}

	// Replace (the same upsert on (media, language)): stranger refused.
	if _, err := svc.CreateSubtitle(ctx, stranger, track("replaced by stranger")); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("stranger replace: %v", err)
	}
	if got := content(); got != "owner words" {
		t.Fatalf("after the stranger's replace the track is %q", got)
	}

	// Unpublish / publish: stranger refused; owner succeeds.
	if _, err := svc.SetCaptionPublished(ctx, stranger, mediaID, "en", false); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("stranger unpublish: %v", err)
	}
	if _, err := svc.SetCaptionPublished(ctx, owner, mediaID, "en", false); err != nil {
		t.Fatalf("owner unpublish: %v", err)
	}

	// Correct and request: stranger refused.
	if _, err := svc.CorrectCaption(ctx, mediaID, stranger, "en", "corrected by stranger"); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("stranger correct: %v", err)
	}
	if _, err := svc.RequestCaptions(ctx, mediaID, stranger, "en"); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("stranger request: %v", err)
	}
	if got := content(); got != "owner words" {
		t.Fatalf("after the stranger's attempts the track is %q", got)
	}

	// Owner replaces their own track.
	if _, err := svc.CreateSubtitle(ctx, owner, track("owner words, v2")); err != nil {
		t.Fatalf("owner replace: %v", err)
	}
}
