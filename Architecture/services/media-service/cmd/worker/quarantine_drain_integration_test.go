//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

// The worker's quarantine and drain paths against the REAL store and schema
// (2026-11-18). On 17 Nov a fake store hid a missing column in a write; these
// run the exact store calls the worker makes. CI only, POSTGRES_DSN naming a
// database that ends in _test.

const workerITPrefix = "worker-it/"

func workerITStore(t *testing.T) (*postgres.MediaAssetStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() {
		c := context.Background()
		sub := `(SELECT id FROM media_assets WHERE storage_key LIKE '` + workerITPrefix + `%')`
		for _, q := range []string{
			`DELETE FROM transcoding_jobs WHERE media_asset_id IN ` + sub,
			`DELETE FROM media_event_outbox WHERE media_asset_id IN ` + sub,
			`DELETE FROM media_transcode_inbox WHERE media_asset_id IN ` + sub,
			`DELETE FROM media_transcode_quarantine WHERE topic = 'worker-it'`,
			`DELETE FROM media_assets WHERE storage_key LIKE '` + workerITPrefix + `%'`,
		} {
			_, _ = pool.Exec(c, q)
		}
	})
	return postgres.New(pool), pool
}

// seedQueuedVideo: a processing video with its request published. The
// columns are postgres.seedLeaseAsset's (unexported, so not callable from
// here): setup.sql makes created_at and updated_at NOT NULL with no default.
func seedQueuedVideo(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, kafka.Message) {
	t.Helper()
	ctx := context.Background()
	id, uploader := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type,
		    file_size_bytes, storage_bucket, storage_key, processing_status,
		    created_at, updated_at)
		VALUES ($1, $2, 'video', 'general', 'video/mp4', 100, 'media', $3, 'processing',
		    NOW() - INTERVAL '1 hour', NOW() - INTERVAL '1 hour')`,
		id, uploader, workerITPrefix+id.String()+"/original"); err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	payload, _ := json.Marshal(postgres.TranscodeRequestPayload{
		MediaTranscodeRequestedPayload: events.MediaTranscodeRequestedPayload{MediaAssetID: id.String(), UploaderID: uploader.String()},
	})
	eventID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_event_outbox (event_id, media_asset_id, event_type, actor_user_id, payload, published_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, NOW())`,
		eventID, id, events.MediaTranscodeRequested, uploader, payload); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	value, _ := json.Marshal(events.EventEnvelope{EventID: eventID, EventType: events.MediaTranscodeRequested, Payload: payload})
	return id, kafka.Message{Topic: "worker-it", Partition: 0, Offset: int64(uuid.New().ID()), Value: value}
}

// reels-5: the quarantine leaves the asset failed + manual_review with the
// creator-visible reason, and the reprocess route's requeue then accepts it.
func TestQuarantineFailsTheAssetInTheRealSchema(t *testing.T) {
	store, pool := workerITStore(t)
	ctx := context.Background()
	id, m := seedQueuedVideo(t, pool)

	if err := quarantineAndFail(store)(ctx, m, errors.New("transcode attempts exhausted after 8 attempts: scanner down")); err != nil {
		t.Fatal(err)
	}
	var status, moderation string
	if err := pool.QueryRow(ctx, `SELECT processing_status, moderation_status FROM media_assets WHERE id = $1`, id).
		Scan(&status, &moderation); err != nil || status != "failed" || moderation != "manual_review" {
		t.Fatalf("asset = %s/%s %v, want failed/manual_review", status, moderation, err)
	}
	var reason string
	if err := pool.QueryRow(ctx, `SELECT error_message FROM transcoding_jobs
		 WHERE media_asset_id = $1 AND target_quality = 'pipeline' AND status = 'failed'`, id).Scan(&reason); err != nil ||
		!strings.Contains(reason, "scanner down") {
		t.Fatalf("creator reason = %q %v", reason, err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_transcode_quarantine WHERE topic = 'worker-it' AND offset_id = $1`, m.Offset).
		Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("quarantine rows = %d %v", rows, err)
	}

	// Replay: once the failure's completion event is published (the relay),
	// the reprocess route's requeue takes the failed asset back.
	if _, err := pool.Exec(ctx, `UPDATE media_event_outbox SET published_at = NOW() WHERE media_asset_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	asset, err := store.GetMedia(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueTranscode(ctx, asset, 0); err != nil {
		t.Fatalf("replay refused: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT processing_status FROM media_assets WHERE id = $1`, id).Scan(&status); err != nil || status != "processing" {
		t.Fatalf("after replay = %s %v, want processing", status, err)
	}
}

// REG-2: a drained run that is then killed is not a lost worker, through
// the worker's own accounting and the real columns.
func TestDrainThenKillIsNotLostInTheRealSchema(t *testing.T) {
	store, pool := workerITStore(t)
	ctx := context.Background()
	id, _ := seedQueuedVideo(t, pool)

	if _, err := beginDelivery(ctx, store, id, "ev-it"); err != nil {
		t.Fatal(err)
	}
	draining := make(chan struct{})
	run := newDeliveryRun(store, id, "ev-it")
	run.watchDrain(ctx, draining)
	close(draining)
	waitFor(t, func() bool {
		var d int
		_ = pool.QueryRow(ctx, `SELECT transcode_delivery_drains FROM media_assets WHERE id = $1`, id).Scan(&d)
		return d == 1
	})
	// Killed: run.end never runs.
	lost, err := beginDelivery(ctx, store, id, "ev-it")
	if err != nil || lost != 0 {
		t.Fatalf("lost = %d %v, want 0", lost, err)
	}
	// Control: a run killed WITHOUT a drain is lost.
	if lost, err := beginDelivery(ctx, store, id, "ev-it"); err != nil || lost != 1 {
		t.Fatalf("control lost = %d %v, want 1", lost, err)
	}
}
