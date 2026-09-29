//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The transcode lease and stall sweeper against a real schema (migration 021).
//
//	POSTGRES_DSN=postgres://…/media_lease_it_test go test -tags integration ./internal/store/postgres -run TranscodeLease -v
//
// The database name must end in _test: this suite writes media rows and
// neutralises heartbeats on processing videos it did not create.

const leaseKeyPrefix = "lease-it/"

func leasePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := subtitleEventsPool(t) // DSN, _test name guard, real bootstrap
	requireScratchDatabase(t, pool)
	ctx := context.Background()
	// Other suites leave processing videos behind; take them out of this
	// suite's way (a fresh heartbeat keeps them from being candidates, and
	// clearing ours keeps the pipeline-idle probe honest).
	if _, err := pool.Exec(ctx, `
		UPDATE media_assets SET processing_status = 'failed'
		 WHERE processing_status = 'processing' AND storage_key NOT LIKE $1`, leaseKeyPrefix+"%"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET transcode_heartbeat_at = NULL WHERE transcode_heartbeat_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM transcoding_jobs WHERE media_asset_id IN (SELECT id FROM media_assets WHERE storage_key LIKE $1)`, leaseKeyPrefix+"%")
		_, _ = pool.Exec(c, `DELETE FROM media_event_outbox WHERE media_asset_id IN (SELECT id FROM media_assets WHERE storage_key LIKE $1)`, leaseKeyPrefix+"%")
		_, _ = pool.Exec(c, `DELETE FROM media_transcode_inbox WHERE media_asset_id IN (SELECT id FROM media_assets WHERE storage_key LIKE $1)`, leaseKeyPrefix+"%")
		_, _ = pool.Exec(c, `DELETE FROM media_assets WHERE storage_key LIKE $1`, leaseKeyPrefix+"%")
	})
	return pool
}

// seedLeaseAsset: a processing video whose request was published (the state
// a worker picks up), with the heartbeat and updated_at ages given. A nil
// heartbeat age leaves it NULL.
func seedLeaseAsset(t *testing.T, pool *pgxpool.Pool, heartbeatAge *time.Duration, updatedAge time.Duration, attempts, rotate int) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id, uploader := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type,
		    file_size_bytes, storage_bucket, storage_key, processing_status,
		    transcode_attempts, created_at, updated_at)
		VALUES ($1, $2, 'video', 'general', 'video/mp4', 100, 'media', $3, 'processing',
		    $4, NOW() - INTERVAL '1 day', NOW() - $5::bigint * INTERVAL '1 millisecond')`,
		id, uploader, leaseKeyPrefix+id.String()+"/original", attempts, updatedAge.Milliseconds()); err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	if heartbeatAge != nil {
		if _, err := pool.Exec(ctx, `UPDATE media_assets SET transcode_heartbeat_at = NOW() - $2::bigint * INTERVAL '1 millisecond' WHERE id = $1`,
			id, heartbeatAge.Milliseconds()); err != nil {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(TranscodeRequestPayload{
		MediaTranscodeRequestedPayload: sharedevents.MediaTranscodeRequestedPayload{
			MediaAssetID: id.String(), UploaderID: uploader.String(),
			StorageKey: leaseKeyPrefix + id.String() + "/original", MimeType: "video/mp4",
		},
		RotateDegrees: rotate,
	})
	eventID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_event_outbox (event_id, media_asset_id, event_type, actor_user_id, payload, published_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, NOW() - INTERVAL '1 hour')`,
		eventID, id, sharedevents.MediaTranscodeRequested, uploader, payload); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	return id, eventID
}

func dur(d time.Duration) *time.Duration { return &d }

type leaseRow struct {
	status, moderation string
	heartbeat          *time.Time
	attempts           int
	updated            time.Time
}

func readLease(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) leaseRow {
	t.Helper()
	var r leaseRow
	if err := pool.QueryRow(context.Background(), `
		SELECT processing_status, moderation_status, transcode_heartbeat_at, transcode_attempts, updated_at
		  FROM media_assets WHERE id = $1`, id).Scan(&r.status, &r.moderation, &r.heartbeat, &r.attempts, &r.updated); err != nil {
		t.Fatal(err)
	}
	return r
}

func requestRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (eventID string, published bool, rotate int) {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT event_id, published_at IS NOT NULL, payload FROM media_event_outbox
		 WHERE media_asset_id = $1 AND event_type = $2`, id, sharedevents.MediaTranscodeRequested).Scan(&eventID, &published, &raw); err != nil {
		t.Fatal(err)
	}
	var p TranscodeRequestPayload
	_ = json.Unmarshal(raw, &p)
	return eventID, published, p.RotateDegrees
}

func outcomeFor(outs []StallOutcome, id uuid.UUID) *StallOutcome {
	for i := range outs {
		if outs[i].MediaID == id {
			return &outs[i]
		}
	}
	return nil
}

func TestTranscodeLeaseColumnsNeedNoReclaimClassification(t *testing.T) {
	pool := leasePool(t)
	ctx := context.Background()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'media_assets'
		   AND column_name IN ('transcode_heartbeat_at', 'transcode_attempts')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("migration 021 columns present: %d, want 2", n)
	}
	if _, err := ResolveLiveReferences(ctx, pool); err != nil {
		t.Fatalf("reclaim policy refuses after migration 021: %v", err)
	}
}

func TestTranscodeLeaseHeartbeatLeavesUpdatedAtAlone(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	id, eventID := seedLeaseAsset(t, pool, nil, 3*time.Hour, 0, 0)
	before := readLease(t, pool, id)
	if err := store.StampTranscodeHeartbeat(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	after := readLease(t, pool, id)
	if after.heartbeat == nil || !after.updated.Equal(before.updated) {
		t.Fatalf("heartbeat=%v updated %v -> %v; want a stamp and updated_at unchanged", after.heartbeat, before.updated, after.updated)
	}
	cur, found, err := store.CurrentTranscodeRequest(context.Background(), id)
	if err != nil || !found || cur != eventID {
		t.Fatalf("current request = %q %v %v, want %q", cur, found, err, eventID)
	}
	if _, found, err := store.CurrentTranscodeRequest(context.Background(), uuid.New()); err != nil || found {
		t.Fatalf("unknown asset: found=%v err=%v", found, err)
	}
}

func TestTranscodeLeaseSweepSelection(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()

	fresh, _ := seedLeaseAsset(t, pool, dur(time.Minute), 5*time.Hour, 0, 0)
	stale, staleEvent := seedLeaseAsset(t, pool, dur(11*time.Minute), 5*time.Hour, 0, 90)
	orphan, _ := seedLeaseAsset(t, pool, nil, 2*time.Hour, 0, 0)
	young, _ := seedLeaseAsset(t, pool, nil, 10*time.Minute, 0, 0)
	inflight, _ := seedLeaseAsset(t, pool, dur(time.Hour), 5*time.Hour, 0, 0)
	if _, err := pool.Exec(ctx, `UPDATE media_event_outbox SET published_at = NULL WHERE media_asset_id = $1`, inflight); err != nil {
		t.Fatal(err)
	}
	// "fresh" is itself a live job, so the pipeline is BUSY: the orphan must
	// wait. Only the dead job is re-queued.
	outs, err := store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if o := outcomeFor(outs, fresh); o != nil {
		t.Fatalf("fresh heartbeat was a candidate: %+v", o)
	}
	if o := outcomeFor(outs, young); o != nil {
		t.Fatalf("recently queued asset was a candidate: %+v", o)
	}
	if o := outcomeFor(outs, orphan); o == nil || o.Action != StallSkip {
		t.Fatalf("orphan with a busy pipeline: %+v, want skip", o)
	}
	if o := outcomeFor(outs, inflight); o == nil || o.Action != StallSkip {
		t.Fatalf("in-flight asset: %+v, want skip", o)
	}
	if ev, pub, _ := requestRow(t, pool, inflight); pub || ev == "" {
		t.Fatalf("in-flight request was touched: %s published=%v", ev, pub)
	}
	if r := readLease(t, pool, inflight); r.attempts != 0 {
		t.Fatalf("in-flight attempts=%d, want 0", r.attempts)
	}

	o := outcomeFor(outs, stale)
	if o == nil || o.Action != StallRequeue || o.Attempts != 1 {
		t.Fatalf("stale heartbeat: %+v, want requeue attempt 1", o)
	}
	ev, published, rotate := requestRow(t, pool, stale)
	if ev == staleEvent || ev != o.EventID || published {
		t.Fatalf("re-queue event %s published=%v; want a fresh unpublished %s (old %s)", ev, published, o.EventID, staleEvent)
	}
	if rotate != 90 {
		t.Fatalf("re-queue dropped the rotation override: %d", rotate)
	}
	if r := readLease(t, pool, stale); r.status != "processing" || r.heartbeat != nil || r.attempts != 1 {
		t.Fatalf("after re-queue: %+v, want processing / heartbeat cleared / attempts 1", r)
	}

	// A second sweep straight away does nothing to it: the new request is
	// unpublished (in flight), and even once published it is NULL + young.
	outs, err = store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if o := outcomeFor(outs, stale); o != nil && o.Action != StallSkip {
		t.Fatalf("second sweep acted on the re-queued asset: %+v", o)
	}

	// Pipeline goes idle (the live job finishes): now the orphan is lost work.
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET processing_status = 'ready' WHERE id = $1`, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET transcode_heartbeat_at = NOW() - INTERVAL '20 minutes' WHERE id = $1`, fresh); err != nil {
		t.Fatal(err)
	}
	// ...but not while THIS worker says it is busy.
	outs, err = store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), true)
	if err != nil {
		t.Fatal(err)
	}
	if o := outcomeFor(outs, orphan); o == nil || o.Action != StallSkip {
		t.Fatalf("orphan while the local worker is busy: %+v, want skip", o)
	}
	outs, err = store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if o := outcomeFor(outs, orphan); o == nil || o.Action != StallRequeue || o.Attempts != 1 {
		t.Fatalf("orphan with an idle pipeline: %+v, want requeue", o)
	}
	// The finished job, stale heartbeat and all, is not even a candidate.
	if o := outcomeFor(outs, fresh); o != nil {
		t.Fatalf("a ready asset was a stall candidate: %+v", o)
	}
}

func TestTranscodeLeaseGiveUpMarksFailed(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()

	started, startedEvent := seedLeaseAsset(t, pool, dur(time.Hour), 5*time.Hour, 3, 0)
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcoding_jobs (id, media_asset_id, target_quality, status, started_at)
		VALUES ($1, $2, '720p', 'processing', NOW() - INTERVAL '2 hours'),
		       ($3, $2, '360p', 'completed', NOW() - INTERVAL '2 hours')`, uuid.New(), started, uuid.New()); err != nil {
		t.Fatal(err)
	}
	// A published completion from an earlier run must not swallow the
	// failure notification (outbox UNIQUE (media_asset_id, event_type)).
	oldCompletion, _ := json.Marshal(sharedevents.MediaTranscodeCompletedPayload{MediaAssetID: started.String(), ProcessingStatus: "ready"})
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_event_outbox (event_id, media_asset_id, event_type, payload, published_at)
		VALUES ($1, $2, $3, $4::jsonb, NOW() - INTERVAL '1 day')`,
		"old:completed:"+started.String(), started, sharedevents.MediaTranscodeCompleted, oldCompletion); err != nil {
		t.Fatal(err)
	}
	never, _ := seedLeaseAsset(t, pool, nil, 5*time.Hour, 3, 0)

	outs, err := store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	o := outcomeFor(outs, started)
	if o == nil || o.Action != StallGiveUp || o.EventID != startedEvent {
		t.Fatalf("attempts at cap: %+v, want give_up recorded against %s", o, startedEvent)
	}
	r := readLease(t, pool, started)
	if r.status != "failed" || r.moderation != "manual_review" {
		t.Fatalf("after give-up: %+v, want failed / manual_review", r)
	}
	// The redelivery of the dead request is now an applied event.
	if done, err := store.AlreadyApplied(ctx, startedEvent); err != nil || !done {
		t.Fatalf("give-up not recorded in the inbox: %v %v", done, err)
	}
	// post-service hears "failed".
	var raw []byte
	var published bool
	if err := pool.QueryRow(ctx, `
		SELECT payload, published_at IS NOT NULL FROM media_event_outbox
		 WHERE media_asset_id = $1 AND event_type = $2`, started, sharedevents.MediaTranscodeCompleted).Scan(&raw, &published); err != nil {
		t.Fatalf("no completion event: %v", err)
	}
	var p sharedevents.MediaTranscodeCompletedPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.ProcessingStatus != "failed" || published {
		t.Fatalf("completion %+v published=%v, want unpublished failed", p, published)
	}
	// The reason is on the lingering 720p job itself (no extra pipeline row
	// when a real job exists); the finished 360p job is untouched.
	var jobs, completedJobs int
	var status720, reason string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'completed'),
		       max(status) FILTER (WHERE target_quality = '720p'),
		       COALESCE(max(error_message) FILTER (WHERE target_quality = '720p'), '')
		  FROM transcoding_jobs WHERE media_asset_id = $1`, started).Scan(&jobs, &completedJobs, &status720, &reason); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 || completedJobs != 1 || status720 != "failed" || reason != o.Reason {
		t.Fatalf("jobs=%d completed=%d 720p=%s reason=%q, want 2/1/failed/%q", jobs, completedJobs, status720, reason, o.Reason)
	}

	o = outcomeFor(outs, never)
	if o == nil || o.Action != StallGiveUp {
		t.Fatalf("never-started at cap: %+v, want give_up", o)
	}
	var target, status string
	if err := pool.QueryRow(ctx, `SELECT target_quality, status FROM transcoding_jobs WHERE media_asset_id = $1`, never).Scan(&target, &status); err != nil {
		t.Fatalf("never-started give-up left no reason row: %v", err)
	}
	if target != "pipeline" || status != "failed" {
		t.Fatalf("reason row %s/%s", target, status)
	}

	// An operator reprocess of a given-up asset starts the count again (once
	// the relay has published the failure, as it does within seconds).
	if _, err := pool.Exec(ctx, `UPDATE media_event_outbox SET published_at = NOW() WHERE media_asset_id = $1`, started); err != nil {
		t.Fatal(err)
	}
	media, err := store.GetMedia(ctx, started)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueTranscode(ctx, media, 0); err != nil {
		t.Fatalf("operator reprocess after give-up: %v", err)
	}
	if r := readLease(t, pool, started); r.status != "processing" || r.attempts != 0 || r.heartbeat != nil {
		t.Fatalf("after operator reprocess: %+v, want processing / attempts 0 / heartbeat cleared", r)
	}
}

func TestTranscodeLeaseSkipLockedAndReplicasNeverDoubleQueue(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	id, _ := seedLeaseAsset(t, pool, dur(time.Hour), 5*time.Hour, 0, 0)

	// A row another replica holds is skipped, not waited on.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Runs before the pool's row cleanup (LIFO), so a failure here cannot
	// leave the cleanup waiting on this lock.
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, `SELECT 1 FROM media_assets WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	sweepCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	outs, err := store.ReclaimStalledTranscodes(sweepCtx, DefaultStallPolicy(), false)
	cancel()
	if err != nil {
		t.Fatalf("sweep blocked or failed on a locked row: %v", err)
	}
	if o := outcomeFor(outs, id); o != nil {
		t.Fatalf("locked row was acted on: %+v", o)
	}
	_ = tx.Rollback(ctx)

	// Two replicas sweeping at once: exactly one re-queue.
	var wg sync.WaitGroup
	results := make([][]StallOutcome, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
		}(i)
	}
	wg.Wait()
	requeues := 0
	for _, outs := range results {
		if o := outcomeFor(outs, id); o != nil && o.Action == StallRequeue {
			requeues++
		}
	}
	var requests int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_event_outbox WHERE media_asset_id = $1 AND event_type = $2`,
		id, sharedevents.MediaTranscodeRequested).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if r := readLease(t, pool, id); requeues != 1 || requests != 1 || r.attempts != 1 {
		t.Fatalf("concurrent sweeps: requeues=%d requests=%d attempts=%d, want 1/1/1", requeues, requests, r.attempts)
	}
}

func TestTranscodeLeaseRequeueRefusesInFlightWork(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	id, eventID := seedLeaseAsset(t, pool, dur(time.Hour), 5*time.Hour, 0, 0)
	if _, err := pool.Exec(ctx, `UPDATE media_event_outbox SET published_at = NULL WHERE media_asset_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	media, err := store.GetMedia(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// The shared store path refuses, whoever calls it.
	if _, err := store.RequeueTranscode(ctx, media, 0); err != ErrTranscodeInFlight {
		t.Fatalf("requeue over an unpublished request: %v, want ErrTranscodeInFlight", err)
	}
	if ev, pub, _ := requestRow(t, pool, id); ev != eventID || pub {
		t.Fatalf("in-flight request replaced: %s published=%v", ev, pub)
	}
}
