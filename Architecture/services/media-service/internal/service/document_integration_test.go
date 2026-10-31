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

// Private documents on real PostgreSQL (CI):
//
//	MEDIA_CHAT_POSTGRES_DSN=postgres://…/media_it_test go test -tags integration ./internal/service -run DocumentIT -v
//
// What only the database proves: the confirm step's single UPDATE moves a
// document (and nothing else) to ready, once; and every viewer read path,
// over the real store and a gate whose authority admits everyone, still
// refuses a document — while the keyed record read commerce verifies with
// returns it.
func documentITPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("MEDIA_CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("MEDIA_CHAT_POSTGRES_DSN is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("MEDIA_CHAT_POSTGRES_DSN does not parse: %v", err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", cfg.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	return pool
}

func seedDocumentIT(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID, fileType, mime, status, moderation string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	key := "user/" + owner.String() + "/" + id.String() + "/original"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO media_assets (
			id, uploader_id, file_type, media_subtype, mime_type,
			file_size_bytes, storage_bucket, storage_key,
			processing_status, moderation_status, hls_master_key, created_at, updated_at
		) VALUES ($1,$2,$3,'general',$4,100,'media',$5,$6,$7,$8,NOW(),NOW())`,
		id, owner, fileType, mime, key, status, moderation, "user/"+owner.String()+"/"+id.String()+"/hls/master.m3u8"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_variants WHERE media_asset_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, id)
	})
	return id
}

func statusesOf(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (string, string) {
	t.Helper()
	var p, m string
	if err := pool.QueryRow(context.Background(),
		`SELECT processing_status, moderation_status FROM media_assets WHERE id = $1`, id).Scan(&p, &m); err != nil {
		t.Fatal(err)
	}
	return p, m
}

func TestDocumentITMarkReadyMovesOnlyAConfirmedDocument(t *testing.T) {
	pool := documentITPool(t)
	store := postgres.New(pool)
	ctx := context.Background()
	owner := uuid.New()

	doc := seedDocumentIT(t, pool, owner, FileTypeDocument, "application/pdf", "uploaded", "pending")
	if err := store.MarkDocumentReady(ctx, doc); err != nil {
		t.Fatalf("a confirmed document was not moved to ready: %v", err)
	}
	if p, m := statusesOf(t, pool, doc); p != "ready" || m != "passed" {
		t.Fatalf("document is %s/%s, want ready/passed", p, m)
	}
	if err := store.MarkDocumentReady(ctx, doc); !errors.Is(err, postgres.ErrDocumentNotConfirmable) {
		t.Fatalf("a second MarkDocumentReady: %v, want ErrDocumentNotConfirmable", err)
	}

	for name, row := range map[string]uuid.UUID{
		"an image":              seedDocumentIT(t, pool, owner, "image", "image/png", "uploaded", "pending"),
		"a video":               seedDocumentIT(t, pool, owner, "video", "video/mp4", "uploaded", "pending"),
		"a rejected document":   seedDocumentIT(t, pool, owner, FileTypeDocument, "application/pdf", "rejected", "pending"),
		"an unconfirmed upload": seedDocumentIT(t, pool, owner, FileTypeDocument, "application/pdf", "pending_upload", "pending"),
	} {
		before, beforeMod := statusesOf(t, pool, row)
		if err := store.MarkDocumentReady(ctx, row); !errors.Is(err, postgres.ErrDocumentNotConfirmable) {
			t.Errorf("%s: MarkDocumentReady = %v, want ErrDocumentNotConfirmable", name, err)
		}
		if p, m := statusesOf(t, pool, row); p != before || m != beforeMod {
			t.Errorf("%s: moved from %s/%s to %s/%s", name, before, beforeMod, p, m)
		}
	}
}

func TestDocumentITNoViewerReadServesADocument(t *testing.T) {
	pool := documentITPool(t)
	ctx := context.Background()
	owner := uuid.New()
	svc := New(postgres.New(pool), nil).WithDeliveryGate(delivery.NewGate(noopSigner{}, allowAll{}))

	doc := seedDocumentIT(t, pool, owner, FileTypeDocument, "application/pdf", "ready", "passed")
	// Positive control: the same gate and store serve an image to the same
	// viewers, so only the document rule can be what refuses below.
	img := seedDocumentIT(t, pool, owner, "image", "image/png", "ready", "passed")
	if _, err := svc.GetMediaVariantURL(ctx, owner, img, "original"); err != nil {
		t.Fatalf("control: the image's original was refused: %v", err)
	}

	for _, viewer := range []uuid.UUID{owner, uuid.New(), uuid.Nil} {
		if _, err := svc.GetMediaURL(ctx, viewer, doc); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("GetMediaURL(viewer %s) = %v, want denied", viewer, err)
		}
		if _, err := svc.GetMediaVariantURL(ctx, viewer, doc, "original"); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("GetMediaVariantURL(viewer %s, original) = %v, want denied", viewer, err)
		}
		if _, err := svc.GetMediaVariantURL(ctx, viewer, doc, AvatarVariant); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("GetMediaVariantURL(viewer %s, avatar) = %v, want denied", viewer, err)
		}
		if _, err := svc.GetHLSPlaylist(ctx, viewer, doc, "master.m3u8"); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("GetHLSPlaylist(viewer %s) = %v, want denied", viewer, err)
		}
		got, err := svc.BatchMediaURLs(ctx, viewer, []uuid.UUID{doc, img})
		if err != nil {
			t.Fatalf("BatchMediaURLs(viewer %s): %v", viewer, err)
		}
		if _, ok := got[doc]; ok {
			t.Errorf("BatchMediaURLs(viewer %s) returned the document", viewer)
		}
		if _, ok := got[img]; !ok {
			t.Errorf("control: BatchMediaURLs(viewer %s) omitted the image", viewer)
		}
		if _, err := svc.ServeHeads().HeadForViewer(ctx, viewer, doc, "original"); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("HEAD serve (viewer %s) = %v, want denied", viewer, err)
		}
		if err := svc.AuthorizeMediaRead(ctx, viewer, doc); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Errorf("AuthorizeMediaRead(viewer %s) = %v, want denied", viewer, err)
		}
	}

	// The keyed record read is how commerce verifies a KYC document: it
	// answers, with no URL in it.
	rec, err := svc.RecordReads().MediaForService(ctx, doc)
	if err != nil {
		t.Fatalf("the keyed record read refused a document: %v", err)
	}
	if rec.FileType != FileTypeDocument || rec.UploaderID != owner || rec.ProcessingStatus != "ready" || rec.ModerationStatus != "passed" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.CdnURL != nil || rec.OriginalURL != nil || rec.ThumbnailURL != nil {
		t.Fatal("a document record carries a URL")
	}
}

// A private document is never a chat attachment: the reservation SQL refuses
// it as it refuses an asset that does not exist. Positive control: the same
// uploader's ready, passed image reserves.
func TestDocumentITIsNeverAChatAttachment(t *testing.T) {
	pool := documentITPool(t)
	ctx := context.Background()
	owner := uuid.New()
	svc := &Service{pgStore: postgres.New(pool)}
	doc := seedDocumentIT(t, pool, owner, FileTypeDocument, "application/pdf", "ready", "passed")
	img := seedDocumentIT(t, pool, owner, "image", "image/png", "ready", "passed")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_chat_attachment_reservations WHERE media_id = ANY($1)`, []uuid.UUID{doc, img})
	})

	if err := svc.ReserveChatAttachment(ctx, uuid.New(), owner, img); err != nil {
		t.Fatalf("control: the uploader's ready image was not reserved: %v", err)
	}
	if err := svc.ReserveChatAttachment(ctx, uuid.New(), owner, doc); !errors.Is(err, ErrChatAttachmentDenied) {
		t.Fatalf("a KYC document was reserved as a chat attachment: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_chat_attachment_reservations WHERE media_id = $1`, doc).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d reservation row(s) pin a document", n)
	}
}
