//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChatAttachmentRequiresOwnerReadyAndPassed(t *testing.T) {
	dsn := os.Getenv("MEDIA_CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("MEDIA_CHAT_POSTGRES_DSN is required")
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
	uploaderID, mediaID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets (
			id, uploader_id, file_type, media_subtype, mime_type,
			file_size_bytes, storage_bucket, storage_key,
			processing_status, moderation_status, created_at, updated_at
		) VALUES ($1,$2,'image','chat','image/jpeg',100,'media','protected/chat/item.jpg','ready','passed',NOW(),NOW())
	`, mediaID, uploaderID); err != nil {
		t.Fatal(err)
	}
	service := &Service{pgStore: postgres.New(pool)}
	if err := service.ReserveChatAttachment(ctx, uuid.New(), uploaderID, mediaID); err != nil {
		t.Fatalf("valid canonical attachment denied: %v", err)
	}
	if err := service.ReserveChatAttachment(ctx, uuid.New(), uuid.New(), mediaID); !errors.Is(err, ErrChatAttachmentDenied) {
		t.Fatalf("non-owner accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET moderation_status='rejected' WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	if err := service.ReserveChatAttachment(ctx, uuid.New(), uploaderID, mediaID); !errors.Is(err, ErrChatAttachmentDenied) {
		t.Fatalf("rejected media accepted: %v", err)
	}
}

func TestChatAttachmentReservationSerializesAgainstPhysicalDelete(t *testing.T) {
	dsn := os.Getenv("MEDIA_CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("MEDIA_CHAT_POSTGRES_DSN is required")
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
	store := postgres.New(pool)
	var deleteAction string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE conrelid='media_chat_attachment_reservations'::regclass
		  AND confrelid='media_assets'::regclass
		  AND contype='f'
	`).Scan(&deleteAction); err != nil {
		t.Fatalf("load chat reservation FK: %v", err)
	}
	if deleteAction != "a" && deleteAction != "r" {
		t.Fatalf("chat reservation FK is not restrictive: confdeltype=%q", deleteAction)
	}

	for iteration := 0; iteration < 40; iteration++ {
		uploaderID, mediaID, referenceID := uuid.New(), uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_assets (
				id, uploader_id, file_type, media_subtype, mime_type,
				file_size_bytes, storage_bucket, storage_key,
				processing_status, moderation_status, created_at, updated_at
			) VALUES ($1,$2,'image','chat','image/jpeg',100,'media',$3,'ready','passed',NOW(),NOW())
		`, mediaID, uploaderID, "protected/chat/race-"+mediaID.String()+".jpg"); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var reserveErr, deleteErr error
		go func() {
			defer wait.Done()
			<-start
			reserveErr = store.ReserveChatAttachment(ctx, referenceID, uploaderID, mediaID)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, deleteErr = store.DeleteMedia(ctx, mediaID)
		}()
		close(start)
		wait.Wait()

		if reserveErr == nil && deleteErr == nil {
			t.Fatalf("iteration %d: reservation and physical delete both succeeded", iteration)
		}
		var assetCount, referenceCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_assets WHERE id=$1`, mediaID).Scan(&assetCount); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_chat_attachment_reservations WHERE reference_id=$1`, referenceID).Scan(&referenceCount); err != nil {
			t.Fatal(err)
		}
		if referenceCount > 0 && assetCount != 1 {
			t.Fatalf("iteration %d: dangling chat reference survived physical delete", iteration)
		}
		if reserveErr == nil && (referenceCount != 1 || assetCount != 1) {
			t.Fatalf("iteration %d: successful reservation not durable (asset=%d reference=%d)", iteration, assetCount, referenceCount)
		}
		if deleteErr == nil && (assetCount != 0 || referenceCount != 0) {
			t.Fatalf("iteration %d: successful delete left rows (asset=%d reference=%d)", iteration, assetCount, referenceCount)
		}
	}
}
