//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/fingerprint"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Copyright Match phase 1 against a real schema (migrations 022 and 023).
//
//	POSTGRES_DSN=postgres://…/media_copyright_it_test go test -tags integration ./internal/store/postgres -run Copyright -v
//
// The database name must end in _test. Everything this suite creates is
// prefixed copyright-it/ and removed afterwards.

const crKeyPrefix = "copyright-it/"

func copyrightPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := subtitleEventsPool(t)
	requireScratchDatabase(t, pool)
	t.Cleanup(func() {
		c := context.Background()
		const owned = `SELECT id FROM media_assets WHERE storage_key LIKE 'copyright-it/%'`
		for _, q := range []string{
			`DELETE FROM copyright_pair_outbox WHERE pair_id IN (SELECT pair_id FROM copyright_pairs WHERE media_lo IN (` + owned + `) OR media_hi IN (` + owned + `))`,
			`DELETE FROM copyright_pairs WHERE media_lo IN (` + owned + `) OR media_hi IN (` + owned + `)`,
			`DELETE FROM copyright_match_observations WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM copyright_anchor_postings WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM copyright_fingerprints WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM media_fingerprint_jobs WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM transcoding_jobs WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM media_event_outbox WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM media_transcode_inbox WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM media_variants WHERE media_asset_id IN (` + owned + `)`,
			`DELETE FROM media_assets WHERE storage_key LIKE 'copyright-it/%'`,
		} {
			_, _ = pool.Exec(c, q)
		}
	})
	return pool
}

// seedVideo inserts a video in the given status. ready videos get
// ready_generation = media_generation = 1 and a confirmed upload time.
func seedVideo(t *testing.T, pool *pgxpool.Pool, status string, confirmedAgo time.Duration) *MediaAsset {
	t.Helper()
	id, uploader := uuid.New(), uuid.New()
	key := crKeyPrefix + id.String() + "/original"
	var readyGen *int64
	if status == "ready" {
		g := int64(1)
		readyGen = &g
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes,
		    storage_bucket, storage_key, processing_status, moderation_status, duration_ms,
		    ready_generation, upload_confirmed_at, upload_time_source, hls_master_key, created_at, updated_at)
		VALUES ($1, $2, 'video', 'general', 'video/mp4', 100, 'media', $3, $4, 'passed', 60000,
		    $5, NOW() - $6::bigint * INTERVAL '1 millisecond', 'confirmed', $7, NOW() - INTERVAL '1 day', NOW())`,
		id, uploader, key, status, readyGen, confirmedAgo.Milliseconds(), crKeyPrefix+id.String()+"/hls/master.m3u8")
	if err != nil {
		t.Fatalf("seed video: %v", err)
	}
	return &MediaAsset{ID: id, UploaderID: uploader, FileType: "video", StorageKey: key, MimeType: "video/mp4", ProcessingStatus: status}
}

func requestPayload(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) TranscodeRequestPayload {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT payload FROM media_event_outbox WHERE media_asset_id = $1 AND event_type = $2`,
		id, sharedevents.MediaTranscodeRequested).Scan(&raw); err != nil {
		t.Fatalf("request row: %v", err)
	}
	var p TranscodeRequestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func genState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (gen int64, ready *int64, status string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `SELECT media_generation, ready_generation, processing_status FROM media_assets WHERE id = $1`, id).Scan(&gen, &ready, &status); err != nil {
		t.Fatal(err)
	}
	return
}

func countRows(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func publishRequest(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE media_event_outbox SET published_at = NOW() WHERE media_asset_id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

// P-14 end to end: the request carries the generation; the completion for
// the current generation sets ready_generation and enqueues one job
// (idempotently, TX-1); a re-queue bumps the generation and supersedes; a
// late completion for the old generation sets nothing and enqueues nothing
// (T11-8); confirm pins upload_confirmed_at once.
func TestCopyrightGenerationLifecycle(t *testing.T) {
	pool := copyrightPool(t)
	store := New(pool)
	ctx := context.Background()
	m := seedVideo(t, pool, "uploaded", 0)

	if err := store.MarkUploadConfirmed(ctx, m.ID, "etag-1"); err != nil {
		t.Fatal(err)
	}
	var confirmed1 time.Time
	var source, etag string
	if err := pool.QueryRow(ctx, `SELECT upload_confirmed_at, upload_time_source, original_etag FROM media_assets WHERE id = $1`, m.ID).Scan(&confirmed1, &source, &etag); err != nil {
		t.Fatal(err)
	}
	if source != "confirmed" || etag != "etag-1" {
		t.Fatalf("confirm wrote source=%s etag=%s", source, etag)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.MarkUploadConfirmed(ctx, m.ID, "etag-2"); err != nil {
		t.Fatal(err)
	}
	var confirmed2 time.Time
	if err := pool.QueryRow(ctx, `SELECT upload_confirmed_at, original_etag FROM media_assets WHERE id = $1`, m.ID).Scan(&confirmed2, &etag); err != nil {
		t.Fatal(err)
	}
	if !confirmed2.Equal(confirmed1) || etag != "etag-1" {
		t.Fatalf("re-confirm overwrote identity: %v→%v etag %s", confirmed1, confirmed2, etag)
	}

	if err := store.QueueTranscode(ctx, m); err != nil {
		t.Fatal(err)
	}
	if p := requestPayload(t, pool, m.ID); p.MediaGeneration != 1 {
		t.Fatalf("request generation %d, want 1", p.MediaGeneration)
	}
	publishRequest(t, pool, m.ID)

	ev1 := uuid.NewString()
	completion := TranscodeCompletion{ProcessingStatus: "ready", ModerationStatus: "passed", MediaGeneration: 1}
	if err := store.CompleteTranscode(ctx, ev1, m.ID, "ready", "hls/master.m3u8", "passed", completion); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTranscode(ctx, ev1, m.ID, "ready", "hls/master.m3u8", "passed", completion); !errors.Is(err, ErrTranscodeAlreadyApplied) {
		t.Fatalf("replay: %v", err)
	}
	gen, ready, status := genState(t, pool, m.ID)
	if gen != 1 || ready == nil || *ready != 1 || status != "ready" {
		t.Fatalf("after completion: gen %d ready %v status %s", gen, ready, status)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM media_fingerprint_jobs WHERE media_asset_id = $1`, m.ID); n != 1 {
		t.Fatalf("job rows after completion + replay: %d, want 1 (TX-1)", n)
	}
	var jobStatus string
	var priority int
	if err := pool.QueryRow(ctx, `SELECT status, priority FROM media_fingerprint_jobs WHERE media_asset_id = $1`, m.ID).Scan(&jobStatus, &priority); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "queued" || priority != FingerprintPriorityUpload {
		t.Fatalf("job %s priority %d", jobStatus, priority)
	}
	// The completion payload carries the generation.
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM media_event_outbox WHERE event_id = $1`, ev1+":completed").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cp TranscodeCompletedPayload
	_ = json.Unmarshal(raw, &cp)
	if cp.MediaGeneration != 1 || cp.ProcessingStatus != "ready" {
		t.Fatalf("completion payload %+v", cp)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_event_outbox SET published_at = NOW() WHERE media_asset_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}

	// Reprocess: generation 2, the gen-1 job superseded.
	m.ProcessingStatus = "ready"
	if _, err := store.RequeueTranscode(ctx, m, 0); err != nil {
		t.Fatal(err)
	}
	if p := requestPayload(t, pool, m.ID); p.MediaGeneration != 2 {
		t.Fatalf("re-request generation %d, want 2", p.MediaGeneration)
	}
	gen, ready, status = genState(t, pool, m.ID)
	if gen != 2 || *ready != 1 || status != "ready" {
		t.Fatalf("after requeue: gen %d ready %d status %s", gen, *ready, status)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM media_fingerprint_jobs WHERE media_asset_id = $1 AND media_generation = 1`, m.ID).Scan(&jobStatus); err != nil || jobStatus != "superseded" {
		t.Fatalf("gen-1 job after requeue: %s %v", jobStatus, err)
	}
	publishRequest(t, pool, m.ID)

	// A late completion from the gen-1 run: applied (inbox) but ignored.
	evLate := uuid.NewString()
	err := store.CompleteTranscode(ctx, evLate, m.ID, "ready", "", "passed", TranscodeCompletion{ProcessingStatus: "ready", ModerationStatus: "passed", MediaGeneration: 1})
	if !errors.Is(err, ErrTranscodeGenerationStale) {
		t.Fatalf("late gen-1 completion: %v, want stale", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM media_transcode_inbox WHERE event_id = $1`, evLate); n != 1 {
		t.Fatal("stale completion must still be recorded applied")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM media_event_outbox WHERE event_id = $1`, evLate+":completed"); n != 0 {
		t.Fatal("stale completion wrote a completion event")
	}
	gen, ready, _ = genState(t, pool, m.ID)
	if gen != 2 || *ready != 1 {
		t.Fatalf("stale completion moved the generation: gen %d ready %d", gen, *ready)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM media_fingerprint_jobs WHERE media_asset_id = $1 AND media_generation = 1 AND status = 'queued'`, m.ID); n != 0 {
		t.Fatal("stale completion re-queued a job (T11-8)")
	}

	// The gen-2 run completes: ready_generation 2, a gen-2 job.
	ev2 := uuid.NewString()
	if err := store.CompleteTranscode(ctx, ev2, m.ID, "ready", "", "passed", TranscodeCompletion{ProcessingStatus: "ready", ModerationStatus: "passed", MediaGeneration: 2}); err != nil {
		t.Fatal(err)
	}
	gen, ready, _ = genState(t, pool, m.ID)
	if gen != 2 || *ready != 2 {
		t.Fatalf("after gen-2 completion: gen %d ready %d", gen, *ready)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM media_fingerprint_jobs WHERE media_asset_id = $1 AND media_generation = 2`, m.ID).Scan(&jobStatus); err != nil || jobStatus != "queued" {
		t.Fatalf("gen-2 job: %s %v", jobStatus, err)
	}
	// A completion with no generation (a pre-022 request) is treated as
	// current and applied.
	ev3 := uuid.NewString()
	if err := store.CompleteTranscode(ctx, ev3, m.ID, "ready", "", "passed", TranscodeCompletion{ProcessingStatus: "ready", ModerationStatus: "passed"}); err != nil {
		t.Fatalf("legacy completion: %v", err)
	}
}

// Claim, lease, attempts and the fence (9.1, TX-5, T11-1).
func TestCopyrightFingerprintClaimLeaseAndFence(t *testing.T) {
	pool := copyrightPool(t)
	store := New(pool)
	ctx := context.Background()
	m := seedVideo(t, pool, "ready", time.Hour)

	if _, _, err := store.EnqueueFingerprintJob(ctx, seedVideo(t, pool, "processing", time.Hour).ID, 0, false); !errors.Is(err, ErrFingerprintNotReady) {
		t.Fatalf("processing asset enqueued: %v", err)
	}
	job, created, err := store.EnqueueFingerprintJob(ctx, m.ID, FingerprintPriorityBackfill, false)
	if err != nil || !created || job.Status != "queued" || job.Priority != FingerprintPriorityBackfill {
		t.Fatalf("enqueue: %+v created=%v err=%v", job, created, err)
	}
	if _, created, err := store.EnqueueFingerprintJob(ctx, m.ID, 0, false); err != nil || created {
		t.Fatalf("second enqueue created=%v err=%v", created, err)
	}

	// Backfill rows are not claimed at upload priority.
	if j, err := store.ClaimFingerprintJob(ctx, FingerprintPriorityUpload, FingerprintLeaseStale, FingerprintMaxAttempts); err != nil || j != nil {
		t.Fatalf("claimed a backfill row at upload priority: %+v %v", j, err)
	}
	first, err := store.ClaimFingerprintJob(ctx, FingerprintPriorityBackfill, FingerprintLeaseStale, FingerprintMaxAttempts)
	if err != nil || first == nil || first.Status != "claimed" || first.ClaimToken == nil || first.Attempts != 1 {
		t.Fatalf("claim: %+v %v", first, err)
	}
	if j, err := store.ClaimFingerprintJob(ctx, FingerprintPriorityBackfill, FingerprintLeaseStale, FingerprintMaxAttempts); err != nil || j != nil {
		t.Fatalf("a claimed job was claimed again: %+v", j)
	}
	if owned, err := store.HeartbeatFingerprintJob(ctx, first.FingerprintJobKey, *first.ClaimToken, 1234); err != nil || !owned {
		t.Fatalf("heartbeat owned=%v err=%v", owned, err)
	}
	// TX-5: the lease expires, another worker reclaims; the first token's
	// writes fail on claim_token.
	if _, err := pool.Exec(ctx, `UPDATE media_fingerprint_jobs SET heartbeat_at = NOW() - INTERVAL '11 minutes' WHERE media_asset_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimFingerprintJob(ctx, FingerprintPriorityBackfill, FingerprintLeaseStale, FingerprintMaxAttempts)
	if err != nil || second == nil || *second.ClaimToken == *first.ClaimToken || second.Attempts != 2 {
		t.Fatalf("reclaim: %+v %v", second, err)
	}
	if owned, _ := store.HeartbeatFingerprintJob(ctx, first.FingerprintJobKey, *first.ClaimToken, 0); owned {
		t.Fatal("the expired token still owns the job")
	}
	fp := StoredFingerprint{InputKind: "hls_rung", InputRef: "360p", DurationMs: 60000, InformativeMs: 60000, Frames: []byte{}, AnchorSpacingS: 5, DurBucket: 18}
	if err := store.StoreFingerprint(ctx, first.FingerprintJobKey, *first.ClaimToken, fp, nil); !errors.Is(err, ErrFingerprintClaimLost) {
		t.Fatalf("write with the expired token: %v", err)
	}
	if err := store.CompleteFingerprintJob(ctx, first.FingerprintJobKey, *first.ClaimToken, FingerprintCompletion{}); !errors.Is(err, ErrFingerprintClaimLost) {
		t.Fatalf("complete with the expired token: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM copyright_fingerprints WHERE media_asset_id = $1`, m.ID); n != 0 {
		t.Fatal("the lost claim wrote a fingerprint")
	}

	// T11-1: a reprocess during the job; the fence rejects the write.
	m.ProcessingStatus = "ready"
	if _, err := store.RequeueTranscode(ctx, m, 0); err != nil {
		t.Fatal(err)
	}
	err = store.StoreFingerprint(ctx, second.FingerprintJobKey, *second.ClaimToken, fp, []AnchorPosting{{BandNo: 0, BandValue: 1, AnchorMs: 0, Hash: 1}})
	if !errors.Is(err, ErrFingerprintSuperseded) {
		t.Fatalf("write after a requeue: %v, want superseded", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM copyright_anchor_postings WHERE media_asset_id = $1`, m.ID); n != 0 {
		t.Fatal("the fenced write left postings behind")
	}
	// The requeue already superseded the job row; the worker's mark is a
	// no-op that reports the claim gone.
	if err := store.MarkFingerprintJobSuperseded(ctx, second.FingerprintJobKey, *second.ClaimToken); !errors.Is(err, ErrFingerprintClaimLost) {
		t.Fatalf("mark superseded after requeue: %v", err)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM media_fingerprint_jobs WHERE media_asset_id = $1 AND media_generation = 1`, m.ID).Scan(&st); err != nil || st != "superseded" {
		t.Fatalf("job after requeue: %s %v", st, err)
	}

	// The media-row half of the fence, on its own: the job is still claimed
	// (nothing superseded it) but the asset moved on — its generation was
	// bumped, or it left 'ready'. Nothing may be written.
	for _, move := range []string{
		`UPDATE media_assets SET media_generation = media_generation + 1 WHERE id = $1`,
		`UPDATE media_assets SET processing_status = 'processing' WHERE id = $1`,
		`UPDATE media_assets SET ready_generation = NULL WHERE id = $1`,
	} {
		mf := seedVideo(t, pool, "ready", time.Hour)
		jf := claimFor(t, store, mf.ID)
		if _, err := pool.Exec(ctx, move, mf.ID); err != nil {
			t.Fatal(err)
		}
		err := store.StoreFingerprint(ctx, jf.FingerprintJobKey, *jf.ClaimToken, fp, []AnchorPosting{{BandNo: 0, BandValue: 1, AnchorMs: 0, Hash: 1}})
		if !errors.Is(err, ErrFingerprintSuperseded) {
			t.Fatalf("%s: write after the asset moved: %v, want superseded", move, err)
		}
		if n := countRows(t, pool, `SELECT count(*) FROM copyright_fingerprints WHERE media_asset_id = $1`, mf.ID); n != 0 {
			t.Fatalf("%s: the fenced write left a fingerprint", move)
		}
		if err := store.CompleteFingerprintJob(ctx, jf.FingerprintJobKey, *jf.ClaimToken, FingerprintCompletion{}); !errors.Is(err, ErrFingerprintSuperseded) {
			t.Fatalf("%s: complete after the asset moved: %v, want superseded", move, err)
		}
		if err := store.MarkFingerprintJobSuperseded(ctx, jf.FingerprintJobKey, *jf.ClaimToken); err != nil {
			t.Fatal(err)
		}
	}

	// Attempts exhausted: the next claim fails the row instead of running it.
	m2 := seedVideo(t, pool, "ready", time.Hour)
	if _, _, err := store.EnqueueFingerprintJob(ctx, m2.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_fingerprint_jobs SET attempts = $2 WHERE media_asset_id = $1`, m2.ID, FingerprintMaxAttempts); err != nil {
		t.Fatal(err)
	}
	if j, err := store.ClaimFingerprintJob(ctx, 0, FingerprintLeaseStale, FingerprintMaxAttempts); err != nil || j != nil {
		t.Fatalf("exhausted job claimed: %+v %v", j, err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM media_fingerprint_jobs WHERE media_asset_id = $1`, m2.ID).Scan(&st); err != nil || st != "failed" {
		t.Fatalf("exhausted job: %s %v", st, err)
	}
	// retry re-queues a failed job; without retry it is left alone.
	if _, created, err := store.EnqueueFingerprintJob(ctx, m2.ID, 0, false); err != nil || created {
		t.Fatalf("failed job re-queued without retry: created=%v err=%v", created, err)
	}
	j, created, err := store.EnqueueFingerprintJob(ctx, m2.ID, 0, true)
	if err != nil || !created || j.Status != "queued" || j.Attempts != 0 {
		t.Fatalf("retry: %+v created=%v err=%v", j, created, err)
	}
	// Skip requires a reason (CHECK constraint).
	j, _ = store.ClaimFingerprintJob(ctx, 0, FingerprintLeaseStale, FingerprintMaxAttempts)
	if err := store.SkipFingerprintJob(ctx, j.FingerprintJobKey, *j.ClaimToken, ""); err == nil {
		t.Fatal("skip without a reason accepted")
	}
	if err := store.SkipFingerprintJob(ctx, j.FingerprintJobKey, *j.ClaimToken, "original_too_large"); err != nil {
		t.Fatal(err)
	}
}

// synthFrames builds a changing 2 fps sequence.
func synthFrames(n int, seed uint64) []fingerprint.Frame {
	out := make([]fingerprint.Frame, n)
	for i := range out {
		x := uint64(i)*0x9E3779B97F4A7C15 + seed
		x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
		x = (x ^ (x >> 27)) * 0x94D049BB133111EB
		out[i] = fingerprint.Frame{TMs: int32(i * fingerprint.FrameMs), Hash: x ^ (x >> 31)}
	}
	return out
}

func postingsFor(frames []fingerprint.Frame) ([]AnchorPosting, int, int) {
	w := fingerprint.Weights(frames)
	inf := fingerprint.InformativeMs(w)
	spacing := fingerprint.AnchorSpacingS(inf)
	var out []AnchorPosting
	for _, a := range fingerprint.Anchors(frames, spacing) {
		for b := 0; b < fingerprint.Bands; b++ {
			out = append(out, AnchorPosting{BandNo: b, BandValue: fingerprint.Band(a.Hash, b), AnchorMs: a.TMs, Hash: a.Hash})
		}
	}
	return out, spacing, fingerprint.DurationBucket(inf)
}

// claimFor enqueues and claims the job of a ready asset.
func claimFor(t *testing.T, store *MediaAssetStore, id uuid.UUID) *FingerprintJob {
	t.Helper()
	ctx := context.Background()
	if _, _, err := store.EnqueueFingerprintJob(ctx, id, 0, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		j, err := store.ClaimFingerprintJob(ctx, 0, FingerprintLeaseStale, FingerprintMaxAttempts)
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			t.Fatal("nothing to claim")
		}
		if j.MediaID == id {
			return j
		}
		// Another suite's row; put it back.
		_ = store.ReleaseFingerprintJob(ctx, j.FingerprintJobKey, *j.ClaimToken, time.Hour, "not mine", true)
	}
	t.Fatal("could not claim the job")
	return nil
}

func storeFor(t *testing.T, store *MediaAssetStore, id uuid.UUID, frames []fingerprint.Frame) *FingerprintJob {
	t.Helper()
	job := claimFor(t, store, id)
	postings, spacing, bucket := postingsFor(frames)
	fp := StoredFingerprint{InputKind: "hls_rung", InputRef: "360p", InputETags: map[string]string{"k": "e"},
		DurationMs: len(frames) * fingerprint.FrameMs, InformativeMs: int(fingerprint.InformativeMs(fingerprint.Weights(frames))),
		Frames: fingerprint.Encode(frames), AnchorSpacingS: spacing, DurBucket: bucket}
	if err := store.StoreFingerprint(context.Background(), job.FingerprintJobKey, *job.ClaimToken, fp, postings); err != nil {
		t.Fatalf("store fingerprint: %v", err)
	}
	return job
}

// Index round trip: a 3/3/2/2 split is found through the database (T9-1),
// an asset never finds itself, stale-generation postings are ignored
// (T11-3), a hot key is dropped whole while others still return (T9-6).
func TestCopyrightIndexLookup(t *testing.T) {
	pool := copyrightPool(t)
	store := New(pool)
	ctx := context.Background()
	ref := seedVideo(t, pool, "ready", 2*time.Hour)
	refFrames := synthFrames(60*fingerprint.FPS, 1)
	storeFor(t, store, ref.ID, refFrames)

	// The copy: the same content with a 3/3/2/2 flip on every hash.
	flip := uint64(0x7)<<0 | uint64(0x7)<<16 | uint64(0x3)<<32 | uint64(0x3)<<48
	copyFrames := make([]fingerprint.Frame, len(refFrames))
	for i, f := range refFrames {
		copyFrames[i] = fingerprint.Frame{TMs: f.TMs, Hash: f.Hash ^ flip}
	}
	query := fingerprint.QueryFrames(copyFrames)
	hashes := make([]uint64, len(query))
	for i, f := range query {
		hashes[i] = f.Hash
	}
	params := DefaultLookupParams()
	params.Buckets = fingerprint.LookupBuckets(fingerprint.InformativeMs(fingerprint.Weights(copyFrames)))
	params.Probes = fingerprint.BuildProbeSet(hashes)
	params.ExcludeMedia = uuid.New()
	res, err := store.LookupAnchorCandidates(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Truncated {
		t.Fatalf("lookup found nothing: %+v", res)
	}
	var postings []fingerprint.Posting
	for _, h := range res.Hits {
		if h.MediaID != ref.ID {
			continue
		}
		postings = append(postings, fingerprint.Posting{AnchorMs: h.AnchorMs, Hash: h.Hash})
	}
	hits := fingerprint.Hits(query, postings)
	if len(hits) == 0 {
		t.Fatal("no hits at τ_hit")
	}
	for _, h := range hits {
		if h.Hamming != 10 || h.MinBandDistance != 2 {
			t.Fatalf("hit %+v: want Hamming 10, min band distance 2", h)
		}
	}
	offsets := fingerprint.Vote(hits)
	if len(offsets) == 0 || offsets[0].OffsetMs != 0 {
		t.Fatalf("vote %+v", offsets)
	}

	// Self-exclusion: the reference's own hashes never return itself.
	selfParams := params
	selfParams.ExcludeMedia = ref.ID
	res, err = store.LookupAnchorCandidates(ctx, selfParams)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.MediaID == ref.ID {
			t.Fatal("an asset matched its own postings")
		}
	}

	// T11-3: after a re-queue the reference's postings are stale and ignored.
	ref.ProcessingStatus = "ready"
	if _, err := store.RequeueTranscode(ctx, ref, 0); err != nil {
		t.Fatal(err)
	}
	res, err = store.LookupAnchorCandidates(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.MediaID == ref.ID {
			t.Fatal("a stale-generation posting was returned")
		}
	}
	if res.StaleDropped == 0 {
		t.Fatal("stale postings were not counted")
	}
	// The sweeper removes them.
	if n, err := store.SweepStalePostings(ctx, 5000, 0); err != nil || n == 0 {
		t.Fatalf("sweep removed %d: %v", n, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM copyright_anchor_postings WHERE media_asset_id = $1`, ref.ID); n != 0 {
		t.Fatalf("%d stale postings left", n)
	}

	// T9-6: a hot key. 300 assets post the same band value; that key is
	// dropped entirely, another key still returns.
	hot := uint64(0xABCD_1234_5678_9F0F)
	bucket := fingerprint.DurationBucket(60000)
	for i := 0; i < 300; i++ {
		a := seedVideo(t, pool, "ready", time.Hour)
		if _, err := pool.Exec(ctx, `
			INSERT INTO copyright_anchor_postings (algo_version, dur_bucket, band_no, band_value, media_asset_id, media_generation, anchor_ms, hash)
			VALUES ($1, $2, 0, $3, $4, 1, 0, $5)`, FingerprintAlgoVersion, bucket, int32(fingerprint.Band(hot, 0)), a.ID, int64(hot)); err != nil {
			t.Fatal(err)
		}
	}
	cold := seedVideo(t, pool, "ready", time.Hour)
	coldHash := uint64(0x1111_2222_3333_4444)
	if _, err := pool.Exec(ctx, `
		INSERT INTO copyright_anchor_postings (algo_version, dur_bucket, band_no, band_value, media_asset_id, media_generation, anchor_ms, hash)
		VALUES ($1, $2, 0, $3, $4, 1, 0, $5)`, FingerprintAlgoVersion, bucket, int32(fingerprint.Band(coldHash, 0)), cold.ID, int64(coldHash)); err != nil {
		t.Fatal(err)
	}
	hp := DefaultLookupParams()
	hp.Buckets = []int{bucket}
	hp.Probes = fingerprint.BuildProbeSet([]uint64{hot, coldHash})
	hp.ExcludeMedia = uuid.New()
	res, err = store.LookupAnchorCandidates(ctx, hp)
	if err != nil {
		t.Fatal(err)
	}
	if res.HotKeysDropped != 1 || res.TruncationKind != "hot_key" {
		t.Fatalf("hot key not dropped: %+v", res)
	}
	foundCold, foundHot := false, false
	for _, h := range res.Hits {
		if h.MediaID == cold.ID {
			foundCold = true
		}
		if h.Hash == hot {
			foundHot = true
		}
	}
	if !foundCold || foundHot {
		t.Fatalf("cold=%v hot=%v: the hot key must be dropped whole and the cold key kept", foundCold, foundHot)
	}
	// The stop-list drops a key before it is even read.
	if _, err := pool.Exec(ctx, `INSERT INTO copyright_band_hot (algo_version, dur_bucket, band_no, band_value, postings, refreshed_at)
		VALUES ($1, $2, 0, $3, 1, NOW()) ON CONFLICT DO NOTHING`, FingerprintAlgoVersion, bucket, int32(fingerprint.Band(coldHash, 0))); err != nil {
		t.Fatal(err)
	}
	res, err = store.LookupAnchorCandidates(ctx, hp)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.MediaID == cold.ID {
			t.Fatal("a stop-listed key returned rows")
		}
	}
	_, _ = pool.Exec(ctx, `DELETE FROM copyright_band_hot WHERE band_value = $1`, int32(fingerprint.Band(coldHash, 0)))
}

// Pairs: one revision per change, one outbox row per revision, in the
// completion transaction; invalidation on re-queue (T11-2) and purge
// (T12-6); the relay's claim lease and failure bookkeeping.
func TestCopyrightPairsRevisionsOutboxAndPurge(t *testing.T) {
	pool := copyrightPool(t)
	store := New(pool)
	ctx := context.Background()
	a := seedVideo(t, pool, "ready", 48*time.Hour)
	b := seedVideo(t, pool, "ready", time.Hour)
	aFrames := synthFrames(60*fingerprint.FPS, 7)
	bFrames := synthFrames(60*fingerprint.FPS, 7)
	jobA := storeFor(t, store, a.ID, aFrames)
	jobB := storeFor(t, store, b.ID, bFrames)

	side := func(m *MediaAsset) PairSide {
		st, err := store.GetMediaGenerationState(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return PairSide{MediaID: m.ID, Generation: st.MediaGeneration, UploadConfirmed: st.UploadConfirmed, UploadTimeSource: st.UploadTimeSource}
	}
	scores := PairScores{Class: "full_or_near_full", RefCoverage: 0.97, CopyCoverage: 0.96, MatchedS: 58, MatchedInformativeS: 58, MedianHamming: 2, P90Hamming: 4, Diversity: 10}
	// Job B (copy) found A (ref).
	if err := store.CompleteFingerprintJob(ctx, jobB.FingerprintJobKey, *jobB.ClaimToken, FingerprintCompletion{
		Pairs:        []PairWrite{{Copy: side(b), Ref: side(a), Algo: FingerprintAlgoVersion, PairScores: scores}},
		Observations: []Observation{{Other: side(a), MinBandDistance: 1, PairScores: scores}},
		Truncation:   json.RawMessage(`{"none":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	pairCount := func() int {
		return countRows(t, pool, `SELECT count(*) FROM copyright_pairs WHERE media_lo IN ($1,$2) AND media_hi IN ($1,$2)`, a.ID, b.ID)
	}
	outboxCount := func() int {
		return countRows(t, pool, `SELECT count(*) FROM copyright_pair_outbox o JOIN copyright_pairs p ON p.pair_id = o.pair_id WHERE p.media_lo IN ($1,$2) AND p.media_hi IN ($1,$2)`, a.ID, b.ID)
	}
	if pairCount() != 1 || outboxCount() != 1 {
		t.Fatalf("after first write: pairs %d outbox %d", pairCount(), outboxCount())
	}
	var revision int64
	var status, direction, class string
	var pairID uuid.UUID
	readPair := func() {
		if err := pool.QueryRow(ctx, `SELECT pair_id, pair_revision, status, direction, class FROM copyright_pairs WHERE media_lo IN ($1,$2) AND media_hi IN ($1,$2)`, a.ID, b.ID).Scan(&pairID, &revision, &status, &direction, &class); err != nil {
			t.Fatal(err)
		}
	}
	readPair()
	wantDir := DirectionLoEarlier
	if a.ID.String() > b.ID.String() {
		wantDir = DirectionHiEarlier
	}
	if revision != 1 || status != "active" || direction != wantDir || class != "full_or_near_full" {
		t.Fatalf("pair rev %d %s %s %s (want %s)", revision, status, direction, class, wantDir)
	}
	var jobStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM media_fingerprint_jobs WHERE media_asset_id = $1`, b.ID).Scan(&jobStatus); err != nil || jobStatus != "done" {
		t.Fatalf("job B %s", jobStatus)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM copyright_match_observations WHERE media_asset_id = $1 AND other_media_id = $2`, b.ID, a.ID); n != 1 {
		t.Fatal("observation not written")
	}

	// Job A (copy) finds B (ref) with the same numbers from its side: the
	// canonical row is identical, so no revision and no event.
	if err := store.CompleteFingerprintJob(ctx, jobA.FingerprintJobKey, *jobA.ClaimToken, FingerprintCompletion{
		Pairs: []PairWrite{{Copy: side(a), Ref: side(b), Algo: FingerprintAlgoVersion,
			PairScores: PairScores{Class: "full_or_near_full", RefCoverage: 0.96, CopyCoverage: 0.97, MatchedS: 58, MatchedInformativeS: 58, MedianHamming: 2, P90Hamming: 4, Diversity: 10}}},
	}); err != nil {
		t.Fatal(err)
	}
	readPair()
	if revision != 1 || outboxCount() != 1 {
		t.Fatalf("an unchanged pair bumped: rev %d outbox %d", revision, outboxCount())
	}

	// A changed score bumps the revision and writes exactly one more row.
	changed := scores
	changed.CopyCoverage = 0.90
	if _, _, err := upsertPairInTx(ctx, pool, PairWrite{Copy: side(b), Ref: side(a), Algo: FingerprintAlgoVersion, PairScores: changed}); err != nil {
		t.Fatal(err)
	}
	readPair()
	if revision != 2 || outboxCount() != 2 {
		t.Fatalf("changed score: rev %d outbox %d", revision, outboxCount())
	}

	// The relay side: claim leases the rows; a second claim gets nothing.
	rows, err := store.ClaimPairEvents(ctx, 50, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mine := 0
	for _, r := range rows {
		if r.PairID == pairID {
			mine++
			var p sharedevents.MediaFingerprintPairPayload
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.PairID != pairID.String() || p.EventID != r.EventID.String() || p.Status != "active" || p.Class != "full_or_near_full" {
				t.Fatalf("payload %+v", p)
			}
		}
	}
	if mine != 2 {
		t.Fatalf("claimed %d of this pair's rows, want 2", mine)
	}
	again, err := store.ClaimPairEvents(ctx, 50, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.PairID == pairID {
			t.Fatal("a leased row was claimed again inside the lease")
		}
	}
	for _, r := range rows {
		if r.PairID != pairID {
			continue
		}
		if r.PairRevision == 1 {
			if err := store.MarkPairEventPublished(ctx, r.EventID); err != nil {
				t.Fatal(err)
			}
		} else {
			if n, err := store.RecordPairEventFailure(ctx, r.EventID, errors.New("broker"), 4*time.Second); err != nil || n != 1 {
				t.Fatalf("failure attempts %d %v", n, err)
			}
		}
	}
	st, err := store.PairOutboxStats(ctx, 20)
	if err != nil || st.Pending < 1 {
		t.Fatalf("stats %+v %v", st, err)
	}

	// T11-2: a re-queue of A invalidates the pair with one event.
	a.ProcessingStatus = "ready"
	if _, err := store.RequeueTranscode(ctx, a, 0); err != nil {
		t.Fatal(err)
	}
	readPair()
	if revision != 3 || status != "invalidated" || outboxCount() != 3 {
		t.Fatalf("after requeue: rev %d %s outbox %d", revision, status, outboxCount())
	}
	var reason, evType string
	if err := pool.QueryRow(ctx, `SELECT p.invalidated_reason, o.event_type FROM copyright_pairs p JOIN copyright_pair_outbox o ON o.pair_id = p.pair_id AND o.pair_revision = 3 WHERE p.pair_id = $1`, pairID).Scan(&reason, &evType); err != nil {
		t.Fatal(err)
	}
	if reason != PairInvalidatedReprocessed || evType != sharedevents.MediaFingerprintPairInvalidated {
		t.Fatalf("invalidation %s %s", reason, evType)
	}
	// A second re-queue finds nothing active: no fourth row.
	publishRequest(t, pool, a.ID)
	if _, err := store.RequeueTranscode(ctx, a, 0); err != nil {
		t.Fatal(err)
	}
	if outboxCount() != 3 {
		t.Fatalf("an already-invalidated pair was invalidated again: %d rows", outboxCount())
	}

	// T12-6: purging B writes an invalidation for each active pair and
	// removes its derived rows, leaving the outbox intact.
	c := seedVideo(t, pool, "ready", time.Hour)
	if _, _, err := upsertPairInTx(ctx, pool, PairWrite{Copy: side(b), Ref: side(c), Algo: FingerprintAlgoVersion, PairScores: scores}); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, pool, `SELECT count(*) FROM copyright_pair_outbox`)
	if _, err := store.DeleteMedia(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM copyright_pair_outbox`); got != before+1 {
		t.Fatalf("purge wrote %d invalidation rows, want 1", got-before)
	}
	for _, q := range []string{
		`SELECT count(*) FROM copyright_pairs WHERE media_lo = $1 OR media_hi = $1`,
		`SELECT count(*) FROM copyright_fingerprints WHERE media_asset_id = $1`,
		`SELECT count(*) FROM copyright_anchor_postings WHERE media_asset_id = $1`,
		`SELECT count(*) FROM media_fingerprint_jobs WHERE media_asset_id = $1`,
		`SELECT count(*) FROM copyright_match_observations WHERE media_asset_id = $1 OR other_media_id = $1`,
	} {
		if n := countRows(t, pool, q, b.ID); n != 0 {
			t.Fatalf("%s left %d rows after purge", q, n)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM copyright_fingerprints WHERE media_asset_id = $1`, c.ID); n != 0 {
		// c never had one; a's remains.
		t.Fatal("unexpected fingerprint for c")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM copyright_fingerprints WHERE media_asset_id = $1`, a.ID); n != 1 {
		t.Fatalf("purging b removed a's fingerprint: %d", n)
	}
	_ = jobStatus
}

// upsertPairInTx runs upsertPairTx in its own transaction (a helper for
// the revision tests; the worker only ever goes through the fenced path).
func upsertPairInTx(ctx context.Context, pool *pgxpool.Pool, w PairWrite) (int64, bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rev, changed, err := upsertPairTx(ctx, tx, w)
	if err != nil {
		return 0, false, err
	}
	return rev, changed, tx.Commit(ctx)
}

// O-obs-1: a ready asset whose reprocess died is re-queued; at the attempt
// cap it is abandoned (never marked failed) and stops being a candidate.
func TestCopyrightStallSweeperReprocessOfReadyAsset(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	m := seedVideo(t, pool, "processing", time.Hour)
	// Make it a lease-suite asset so leasePool's cleanup removes it.
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET storage_key = $2 WHERE id = $1`, m.ID, leaseKeyPrefix+m.ID.String()+"/original"); err != nil {
		t.Fatal(err)
	}
	m.StorageKey = leaseKeyPrefix + m.ID.String() + "/original"
	if err := store.QueueTranscode(ctx, m); err != nil { // gen 1 request
		t.Fatal(err)
	}
	publishRequest(t, pool, m.ID)
	// The gen-1 run finished: ready at generation 1.
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET processing_status = 'ready', ready_generation = 1 WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	m.ProcessingStatus = "ready"
	// Operator reprocess → gen 2; the worker starts it, then dies.
	if _, err := store.RequeueTranscode(ctx, m, 0); err != nil {
		t.Fatal(err)
	}
	publishRequest(t, pool, m.ID)
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET transcode_heartbeat_at = NOW() - INTERVAL '20 minutes', updated_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	outs, err := store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	o := outcomeFor(outs, m.ID)
	if o == nil || o.Action != StallRequeue {
		t.Fatalf("dead reprocess of a ready asset: %+v, want requeue", o)
	}
	gen, ready, status := genState(t, pool, m.ID)
	if gen != 3 || *ready != 1 || status != "ready" {
		t.Fatalf("after the sweep's requeue: gen %d ready %d status %s", gen, *ready, status)
	}
	// At the cap: abandoned, still ready, request recorded, not a candidate.
	publishRequest(t, pool, m.ID)
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET transcode_heartbeat_at = NOW() - INTERVAL '20 minutes', updated_at = NOW() - INTERVAL '1 hour', transcode_attempts = $2 WHERE id = $1`, m.ID, DefaultStallPolicy().MaxAttempts); err != nil {
		t.Fatal(err)
	}
	outs, err = store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	o = outcomeFor(outs, m.ID)
	if o == nil || o.Action != StallAbandonReprocess {
		t.Fatalf("at the cap: %+v, want abandon", o)
	}
	gen, ready, status = genState(t, pool, m.ID)
	if status != "ready" || gen != 3 || *ready != 1 {
		t.Fatalf("abandon changed the asset: gen %d ready %d status %s", gen, *ready, status)
	}
	ev, _, _ := requestRow(t, pool, m.ID)
	if n := countRows(t, pool, `SELECT count(*) FROM media_transcode_inbox WHERE event_id = $1 AND outcome = 'failed'`, ev); n != 1 {
		t.Fatal("abandon did not record the request as failed")
	}
	outs, err = store.ReclaimStalledTranscodes(ctx, DefaultStallPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if o := outcomeFor(outs, m.ID); o != nil {
		t.Fatalf("abandoned asset is still a candidate: %+v", o)
	}
}
