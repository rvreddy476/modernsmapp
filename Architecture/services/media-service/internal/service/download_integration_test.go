//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The file-download route over a real store (owner only since 2026-10-02).
//
//	POSTGRES_DSN=postgres://…/media_rec_it_test go test -tags integration ./internal/service -run DownloadIT -v
//
// The database name must end in _test.
//
// The gate carries a download authority that answers YES to everybody —
// post-service saying "this post allows download" — wired exactly as
// cmd/server wires it. A stranger is refused all the same, and the
// authority is never asked: allow_download opens this route to nobody.
func TestDownloadITIsTheUploadersAlone(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test (e.g. media_rec_it_test)", cfg.Database)
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

	owner, stranger := uuid.New(), uuid.New()
	seed := func(fileType, scope string, variants ...string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_assets (
				id, uploader_id, file_type, media_subtype, mime_type,
				file_size_bytes, storage_bucket, storage_key,
				processing_status, moderation_status, created_at, updated_at
			) VALUES ($1,$2,$3,'general','video/mp4',100,'media',$4,'ready','passed',NOW(),NOW())`,
			id, owner, fileType, "uploads/"+owner.String()+"/"+id.String()+"/original"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM media_variants WHERE media_asset_id = $1`, id)
			_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, id)
		})
		if scope != "" {
			if _, err := pool.Exec(ctx, `UPDATE media_assets SET access_scope = $2 WHERE id = $1`, id, scope); err != nil {
				t.Fatal(err)
			}
		}
		for _, v := range variants {
			if _, err := pool.Exec(ctx, `
				INSERT INTO media_variants (media_asset_id, variant, size_bytes, mime, object_key)
				VALUES ($1, $2, 10, 'video/mp4', $3)`, id, v, "uploads/"+owner.String()+"/"+id.String()+"/"+v); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	video := seed("video", "", "360p", "480p", "720p")
	originalOnly := seed("video", "")
	image := seed("image", "")
	anonymous := seed("video", postgres.AccessScopeAnonymous, "720p")

	authority := &fakeDownloadAuthority{} // err nil: "the post allows download"
	gate := delivery.NewGate(attachmentSigner{}, nil).WithDownloadAuthorizer(authority)
	svc := &Service{pgStore: postgres.New(pool), gate: gate}

	// The uploader gets an attachment URL of the best rendition.
	url, err := svc.DownloadURL(ctx, owner, video)
	if err != nil || !strings.Contains(url, "/"+video.String()+"/720p?") || !strings.Contains(url, "response-content-disposition=attachment") ||
		!strings.Contains(url, video.String()+".mp4") {
		t.Fatalf("owner: url=%q err=%v", url, err)
	}
	if url, err := svc.DownloadURL(ctx, owner, originalOnly); err != nil || !strings.Contains(url, "/original?") {
		t.Fatalf("owner, no rendition: url=%q err=%v", url, err)
	}

	// Nobody else does, although post-service would say yes — and it is not asked.
	for name, viewer := range map[string]uuid.UUID{"a stranger": stranger, "anonymous": uuid.Nil} {
		url, err := svc.DownloadURL(ctx, viewer, video)
		if !errors.Is(err, ErrDownloadNotAllowed) || url != "" {
			t.Fatalf("%s: url=%q err=%v, want ErrDownloadNotAllowed", name, url, err)
		}
	}
	if len(authority.asked) != 0 {
		t.Fatalf("post-service's allow_download was consulted: %v", authority.asked)
	}

	// Not a video, no such asset, and the assets that are never a download.
	if _, err := svc.DownloadURL(ctx, owner, image); !errors.Is(err, ErrDownloadNotVideo) {
		t.Fatalf("image: %v", err)
	}
	if _, err := svc.DownloadURL(ctx, owner, uuid.New()); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := svc.DownloadURL(ctx, owner, anonymous); !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("anonymous-scope asset, its own uploader: %v", err)
	}

	// A trusted service acting for an administrator: any ordinary video,
	// never a scoped one, and the same not-found answers.
	if url, err := svc.DownloadURLForService(ctx, video); err != nil || !strings.Contains(url, "response-content-disposition=attachment") {
		t.Fatalf("service: url=%q err=%v", url, err)
	}
	if _, err := svc.DownloadURLForService(ctx, anonymous); !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("service, anonymous-scope asset: %v", err)
	}
	if _, err := svc.DownloadURLForService(ctx, image); !errors.Is(err, ErrDownloadNotVideo) {
		t.Fatalf("service, image: %v", err)
	}
	if _, err := svc.DownloadURLForService(ctx, uuid.New()); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("service, missing: %v", err)
	}

	// No gate: an outage, never a URL.
	if _, err := (&Service{pgStore: postgres.New(pool)}).DownloadURL(ctx, owner, video); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("no gate: %v", err)
	}
}
