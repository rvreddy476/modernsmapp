//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/media-service/database"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func durabilityPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("MEDIA_DURABILITY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MEDIA_DURABILITY_POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(context.Background(), pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

func seedDurabilityMedia(t *testing.T, store *MediaAssetStore, status string) *MediaAsset {
	t.Helper()
	m := &MediaAsset{
		ID: uuid.New(), UploaderID: uuid.New(), FileType: "video", MediaSubtype: "general",
		MimeType: "video/mp4", FileSizeBytes: 1024, StorageBucket: "test",
		StorageKey: "user/test/asset/original", ProcessingStatus: status, CreatedAt: time.Now(),
	}
	if err := store.CreateMedia(context.Background(), m); err != nil {
		t.Fatalf("seed media: %v", err)
	}
	return m
}

func TestLiveQueueTranscodeIsAtomicAndIdempotent(t *testing.T) {
	pool := durabilityPool(t)
	store := New(pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE media_event_outbox SET published_at=COALESCE(published_at,NOW())`); err != nil {
		t.Fatal(err)
	}
	m := seedDurabilityMedia(t, store, "uploaded")

	if err := store.QueueTranscode(ctx, m); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := store.QueueTranscode(ctx, m); err != nil {
		t.Fatalf("idempotent queue: %v", err)
	}
	var status string
	var requests int
	if err := pool.QueryRow(ctx, `SELECT processing_status FROM media_assets WHERE id=$1`, m.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_event_outbox WHERE media_asset_id=$1 AND event_type=$2`, m.ID, events.MediaTranscodeRequested).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if status != "processing" || requests != 1 {
		t.Fatalf("status=%s requests=%d, want processing/1", status, requests)
	}
	pending, err := store.PendingMediaEvents(ctx, events.MediaTranscodeRequested, 10)
	if err != nil || len(pending) != 1 || pending[0].ActorUserID == nil || *pending[0].ActorUserID != m.UploaderID.String() {
		t.Fatalf("pending outbox=%+v err=%v", pending, err)
	}

	missing := *m
	missing.ID = uuid.New()
	if err := store.QueueTranscode(ctx, &missing); err == nil {
		t.Fatal("missing media unexpectedly queued")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_event_outbox WHERE media_asset_id=$1`, missing.ID).Scan(&requests); err != nil || requests != 0 {
		t.Fatalf("failed queue left outbox row: count=%d err=%v", requests, err)
	}
}

func TestLiveCompletionStateInboxAndEventAreOneCommit(t *testing.T) {
	pool := durabilityPool(t)
	store := New(pool)
	ctx := context.Background()
	m := seedDurabilityMedia(t, store, "processing")
	eventID := uuid.NewString()
	completion := TranscodeCompletion{ProcessingStatus: "ready", ModerationStatus: "passed", HLSMasterURL: "/hls/master.m3u8"}
	if err := store.CompleteTranscode(ctx, eventID, m.ID, "ready", "hls/master.m3u8", "passed", completion); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := store.CompleteTranscode(ctx, eventID, m.ID, "ready", "hls/master.m3u8", "passed", completion); !errors.Is(err, ErrTranscodeAlreadyApplied) {
		t.Fatalf("replay=%v, want ErrTranscodeAlreadyApplied", err)
	}
	var status, moderation string
	var inbox, outbox int
	if err := pool.QueryRow(ctx, `SELECT processing_status, moderation_status FROM media_assets WHERE id=$1`, m.ID).Scan(&status, &moderation); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM media_transcode_inbox WHERE event_id=$1`, eventID).Scan(&inbox)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM media_event_outbox WHERE event_id=$1`, eventID+":completed").Scan(&outbox)
	if status != "ready" || moderation != "passed" || inbox != 1 || outbox != 1 {
		t.Fatalf("status=%s moderation=%s inbox=%d outbox=%d", status, moderation, inbox, outbox)
	}

	missingID, missingEvent := uuid.New(), uuid.NewString()
	if err := store.CompleteTranscode(ctx, missingEvent, missingID, "ready", "", "passed", completion); err == nil {
		t.Fatal("missing media completion unexpectedly committed")
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM media_transcode_inbox WHERE event_id=$1`, missingEvent).Scan(&inbox)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM media_event_outbox WHERE event_id=$1`, missingEvent+":completed").Scan(&outbox)
	if inbox != 0 || outbox != 0 {
		t.Fatalf("failed completion was partial: inbox=%d outbox=%d", inbox, outbox)
	}
}

func TestLiveDuplicateWorkersSerializeAndPoisonIsDurable(t *testing.T) {
	pool := durabilityPool(t)
	store := New(pool)
	ctx := context.Background()
	eventID := uuid.NewString()
	var active, maxActive atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.WithTranscodeEventLock(ctx, eventID, func() error {
				n := active.Add(1)
				for {
					old := maxActive.Load()
					if n <= old || maxActive.CompareAndSwap(old, n) {
						break
					}
				}
				time.Sleep(100 * time.Millisecond)
				active.Add(-1)
				return nil
			}); err != nil {
				t.Errorf("lock: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("duplicate workers overlapped: max active=%d", got)
	}

	m := kafka.Message{Topic: "media.events", Partition: 2, Offset: 91, Key: []byte("bad"), Value: []byte("{not-json")}
	if err := store.QuarantineTranscode(ctx, m, errors.New("decode failed")); err != nil {
		t.Fatal(err)
	}
	if err := store.QuarantineTranscode(ctx, m, errors.New("decode failed")); err != nil {
		t.Fatalf("idempotent quarantine: %v", err)
	}
	var count int
	var value []byte
	if err := pool.QueryRow(ctx, `SELECT message_value FROM media_transcode_quarantine WHERE topic=$1 AND partition_id=$2 AND offset_id=$3`, m.Topic, m.Partition, m.Offset).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_transcode_quarantine WHERE topic=$1 AND partition_id=$2 AND offset_id=$3`, m.Topic, m.Partition, m.Offset).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || string(value) != string(m.Value) {
		t.Fatalf("quarantine count=%d value=%q", count, value)
	}
}
