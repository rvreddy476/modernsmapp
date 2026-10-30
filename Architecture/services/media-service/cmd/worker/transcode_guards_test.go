package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
)

// Reels audit 2026-10-30: the worker's guards.

var fastRetry = retryPolicy{maxAttempts: 4, initialBackoff: time.Millisecond, maxBackoff: 2 * time.Millisecond}

type quarantineSpy struct {
	calls int
	last  error
	fail  error
}

func (q *quarantineSpy) quarantine(_ context.Context, _ kafka.Message, err error) error {
	q.calls++
	q.last = err
	return q.fail
}

func TestTransientFailureIsQuarantinedAfterMaxAttempts(t *testing.T) {
	attempts := 0
	q := &quarantineSpy{}
	ok := retryUntilDurable(context.Background(), kafka.Message{}, func() error {
		attempts++
		return errors.New("insert variants: foreign key violation")
	}, q.quarantine, fastRetry)
	if !ok {
		t.Fatal("a message quarantined after its attempts was not committable")
	}
	if attempts != fastRetry.maxAttempts || q.calls != 1 {
		t.Fatalf("attempts=%d quarantines=%d, want %d and 1", attempts, q.calls, fastRetry.maxAttempts)
	}
	if !errors.Is(q.last, errAttemptsExhausted) {
		t.Fatalf("quarantine reason %v does not say the attempts ran out", q.last)
	}
}

func TestPermanentFailureIsQuarantinedAtOnce(t *testing.T) {
	attempts := 0
	q := &quarantineSpy{}
	ok := retryUntilDurable(context.Background(), kafka.Message{}, func() error {
		attempts++
		return permanentTranscode(errors.New("corrupt"))
	}, q.quarantine, fastRetry)
	if !ok || attempts != 1 || q.calls != 1 {
		t.Fatalf("ok=%v attempts=%d quarantines=%d", ok, attempts, q.calls)
	}
}

func TestTransientFailureThenSuccessIsNotQuarantined(t *testing.T) {
	attempts := 0
	q := &quarantineSpy{}
	ok := retryUntilDurable(context.Background(), kafka.Message{}, func() error {
		attempts++
		if attempts < fastRetry.maxAttempts {
			return errors.New("blob blip")
		}
		return nil
	}, q.quarantine, fastRetry)
	if !ok || q.calls != 0 || attempts != fastRetry.maxAttempts {
		t.Fatalf("ok=%v attempts=%d quarantines=%d", ok, attempts, q.calls)
	}
}

// A quarantine that cannot be written must never let the offset advance.
func TestUnwritableQuarantineDoesNotCommit(t *testing.T) {
	q := &quarantineSpy{fail: errors.New("db down")}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ok := retryUntilDurable(ctx, kafka.Message{}, func() error { return errors.New("transient") }, q.quarantine, fastRetry)
	if ok {
		t.Fatal("committable without a durable quarantine")
	}
	if q.calls < 2 {
		t.Fatalf("quarantine tried %d times; it must keep trying", q.calls)
	}
}

func TestWorkerConfigFromEnvironment(t *testing.T) {
	env := map[string]string{
		"MEDIA_TRANSCODE_MAX_ATTEMPTS":    "3",
		"MEDIA_MAX_VIDEO_DURATION_SEC":    "900",
		"MEDIA_WORKER_MAX_ORIGINAL_BYTES": "1024",
		"MEDIA_MAX_VIDEO_FPS":             "nonsense",
	}
	cfg := loadWorkerConfig(func(k string) string { return env[k] })
	if cfg.maxAttempts != 3 || cfg.limits.MaxDurationSeconds != 900 || cfg.maxOriginalBytes != 1024 {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.limits.MaxFrameRate != processing.DefaultMaxVideoFrameRate {
		t.Fatalf("a bad value did not fall back: %v", cfg.limits.MaxFrameRate)
	}
	def := loadWorkerConfig(func(string) string { return "" })
	if def.maxAttempts != defaultMaxAttempts || def.maxOriginalBytes != defaultMaxOriginalBytes ||
		def.limits.MinShortSide != processing.MinVideoResolution {
		t.Fatalf("defaults = %+v", def)
	}
	if zero := loadWorkerConfig(func(k string) string {
		if k == "MEDIA_TRANSCODE_MAX_ATTEMPTS" {
			return "0"
		}
		return ""
	}); zero.maxAttempts != 1 {
		t.Fatalf("zero attempts would never run: %d", zero.maxAttempts)
	}
}

// ── fakes ─────────────────────────────────────────────────────────────

type fakeLockedStore struct {
	mu        sync.Mutex
	media     *postgres.MediaAsset
	getErr    error
	goneAfter int // GetMedia calls that succeed before the row is "deleted"; -1 never
	gets      int
	skipped   []string
	completes []string
	complete  error
	applied   bool
}

func (f *fakeLockedStore) GetMedia(_ context.Context, _ uuid.UUID) (*postgres.MediaAsset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.media == nil || (f.goneAfter >= 0 && f.gets > f.goneAfter) {
		return nil, pgx.ErrNoRows
	}
	m := *f.media
	return &m, nil
}
func (f *fakeLockedStore) CurrentTranscodeRequest(context.Context, uuid.UUID) (string, bool, error) {
	return "", false, nil
}
func (f *fakeLockedStore) StampTranscodeHeartbeat(context.Context, uuid.UUID) error { return nil }
func (f *fakeLockedStore) AlreadyApplied(context.Context, string) (bool, error) {
	return f.applied, nil
}
func (f *fakeLockedStore) RecordTranscodeSkipped(_ context.Context, eventID string, _ uuid.UUID) error {
	f.skipped = append(f.skipped, eventID)
	return nil
}
func (f *fakeLockedStore) GetTranscodeTiming(context.Context, uuid.UUID) (postgres.TranscodeTiming, error) {
	return postgres.TranscodeTiming{}, nil
}
func (f *fakeLockedStore) CompleteTranscode(_ context.Context, _ string, _ uuid.UUID, outcome, _, _ string, _ postgres.TranscodeCompletion) error {
	f.completes = append(f.completes, outcome)
	return f.complete
}
func (f *fakeLockedStore) ActivatePendingSlot(context.Context, uuid.UUID) error { return nil }

type prefixBlobs struct {
	keys    []string
	deleted []string
}

func (b *prefixBlobs) ListObjectKeys(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for _, k := range b.keys {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k)
		}
	}
	return out, nil
}
func (b *prefixBlobs) DeleteObject(_ context.Context, key string) error {
	b.deleted = append(b.deleted, key)
	return nil
}

func videoAsset() *postgres.MediaAsset {
	uploader, id := uuid.New(), uuid.New()
	return &postgres.MediaAsset{
		ID: id, UploaderID: uploader, FileType: "video", MimeType: "video/mp4",
		StorageKey:   fmt.Sprintf("user/%s/%s/original", uploader, id),
		OriginalETag: "etag-confirmed",
	}
}

func lockedCall(store *fakeLockedStore, blobs prefixCleaner, run transcodeRun) error {
	id := uuid.Nil
	if store.media != nil {
		id = store.media.ID
	}
	return processTranscodeLocked(context.Background(),
		events.EventEnvelope{EventID: "ev-1"},
		postgres.TranscodeRequestPayload{}, id, store, blobs, run)
}

func neverRun(t *testing.T) transcodeRun {
	return func(context.Context, *transcodeSubject, *jobBudget, *string) error {
		t.Fatal("transcode ran for an asset that is gone")
		return nil
	}
}

// ── finding 1: a deleted asset is a skip ─────────────────────────────

func TestAssetDeletedBeforeTheWorkerIsSkippedNotRetried(t *testing.T) {
	store := &fakeLockedStore{goneAfter: -1}
	if err := lockedCall(store, &prefixBlobs{}, neverRun(t)); err != nil {
		t.Fatalf("err = %v, want a recorded skip", err)
	}
	if len(store.skipped) != 1 || store.skipped[0] != "ev-1" {
		t.Fatalf("skip not recorded: %v", store.skipped)
	}
	if len(store.completes) != 0 {
		t.Fatalf("a gone asset was completed: %v", store.completes)
	}
}

func TestAssetDeletedMidRunIsSkippedAndItsObjectsRemoved(t *testing.T) {
	m := videoAsset()
	store := &fakeLockedStore{media: m, goneAfter: 1}
	prefix := m.StorageKey[:len(m.StorageKey)-len("original")]
	blobs := &prefixBlobs{keys: []string{prefix + "360p", prefix + "hls/360p_000.ts", "user/other/x/360p"}}
	err := lockedCall(store, blobs, func(context.Context, *transcodeSubject, *jobBudget, *string) error {
		return errors.New("insert variants: violates foreign key constraint")
	})
	if err != nil {
		t.Fatalf("err = %v, want nil (skip)", err)
	}
	if len(store.skipped) != 1 || len(store.completes) != 0 {
		t.Fatalf("skipped=%v completes=%v", store.skipped, store.completes)
	}
	sort.Strings(blobs.deleted)
	if len(blobs.deleted) != 2 || blobs.deleted[0] != prefix+"360p" || blobs.deleted[1] != prefix+"hls/360p_000.ts" {
		t.Fatalf("deleted %v, want only the asset's own objects", blobs.deleted)
	}
}

func TestTransientFailureOfALiveAssetIsRetried(t *testing.T) {
	store := &fakeLockedStore{media: videoAsset(), goneAfter: -1}
	err := lockedCall(store, &prefixBlobs{}, func(context.Context, *transcodeSubject, *jobBudget, *string) error {
		return errors.New("blob blip")
	})
	if err == nil || isPermanentTranscodeFailure(err) || len(store.skipped) != 0 {
		t.Fatalf("err=%v skipped=%v: a live asset's transient failure must be retried", err, store.skipped)
	}
}

func TestCompletionForADeletedAssetIsAcceptedNotRetried(t *testing.T) {
	store := &fakeLockedStore{media: videoAsset(), goneAfter: -1,
		complete: fmt.Errorf("%w: x", postgres.ErrTranscodeAssetGone)}
	err := lockedCall(store, &prefixBlobs{}, func(_ context.Context, _ *transcodeSubject, _ *jobBudget, mod *string) error {
		*mod = "passed"
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	store2 := &fakeLockedStore{media: videoAsset(), goneAfter: -1,
		complete: fmt.Errorf("%w: x", postgres.ErrTranscodeAssetGone)}
	err = lockedCall(store2, &prefixBlobs{}, func(context.Context, *transcodeSubject, *jobBudget, *string) error {
		return permanentTranscode(errors.New("corrupt"))
	})
	if err != nil || len(store2.completes) != 1 || store2.completes[0] != "failed" {
		t.Fatalf("terminal failure for a deleted asset: err=%v completes=%v", err, store2.completes)
	}
}

// ── finding 13: the row, not the message ──────────────────────────────

func TestSubjectComesFromTheRow(t *testing.T) {
	m := videoAsset()
	store := &fakeLockedStore{media: m, goneAfter: -1}
	got, err := resolveTranscodeSubject(context.Background(), store, m.ID,
		postgres.TranscodeRequestPayload{MediaTranscodeRequestedPayload: events.MediaTranscodeRequestedPayload{
			StorageKey: "user/someone-else/x/original", MimeType: "text/html"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageKey != m.StorageKey || got.MimeType != "video/mp4" || got.OriginalETag != "etag-confirmed" {
		t.Fatalf("subject = %+v, want the row's key, MIME and ETag", got)
	}
}

func TestSubjectWithAForeignKeyShapeIsRefused(t *testing.T) {
	m := videoAsset()
	m.StorageKey = "user/" + uuid.NewString() + "/" + m.ID.String() + "/original"
	_, err := resolveTranscodeSubject(context.Background(), &fakeLockedStore{media: m, goneAfter: -1}, m.ID, postgres.TranscodeRequestPayload{})
	if !isPermanentTranscodeFailure(err) {
		t.Fatalf("key of another uploader: err = %v, want permanent", err)
	}
	img := videoAsset()
	img.FileType = "image"
	if _, err := resolveTranscodeSubject(context.Background(), &fakeLockedStore{media: img, goneAfter: -1}, img.ID, postgres.TranscodeRequestPayload{}); !isPermanentTranscodeFailure(err) {
		t.Fatalf("image: err = %v, want permanent", err)
	}
	if _, err := resolveTranscodeSubject(context.Background(), &fakeLockedStore{goneAfter: -1}, uuid.New(), postgres.TranscodeRequestPayload{}); !errors.Is(err, errAssetGone) {
		t.Fatalf("no row: err = %v, want errAssetGone", err)
	}
	if _, err := resolveTranscodeSubject(context.Background(), &fakeLockedStore{getErr: errors.New("db down")}, uuid.New(), postgres.TranscodeRequestPayload{}); err == nil || errors.Is(err, errAssetGone) || isPermanentTranscodeFailure(err) {
		t.Fatalf("db down: err = %v, want transient", err)
	}
}

// ── findings 2 and 6: before the download ─────────────────────────────

func TestOriginalCheckedBeforeDownload(t *testing.T) {
	subject := &transcodeSubject{ID: uuid.New(), OriginalETag: "e1"}
	roomy := scratchSpace{free: 100 << 30, total: 200 << 30}
	ok := blob.ObjectInfo{Size: 1 << 30, ETag: "e1"}
	if err := checkOriginalBeforeDownload(ok, subject, 16<<30, roomy, true); err != nil {
		t.Fatalf("ordinary original refused: %v", err)
	}
	changed := ok
	changed.ETag = "e2"
	if err := checkOriginalBeforeDownload(changed, subject, 16<<30, roomy, true); !isPermanentTranscodeFailure(err) || !errors.Is(err, blob.ErrObjectChanged) {
		t.Fatalf("replaced original: err = %v, want permanent ErrObjectChanged", err)
	}
	huge := blob.ObjectInfo{Size: 17 << 30, ETag: "e1"}
	if err := checkOriginalBeforeDownload(huge, subject, 16<<30, roomy, true); !isPermanentTranscodeFailure(err) {
		t.Fatalf("over the cap: err = %v, want permanent", err)
	}
	small := scratchSpace{free: 10 << 30, total: 10 << 30}
	if err := checkOriginalBeforeDownload(blob.ObjectInfo{Size: 4 << 30, ETag: "e1"}, subject, 0, small, true); !isPermanentTranscodeFailure(err) {
		t.Fatalf("cannot ever fit: err = %v, want permanent", err)
	}
	busy := scratchSpace{free: 1 << 30, total: 200 << 30}
	if err := checkOriginalBeforeDownload(ok, subject, 16<<30, busy, true); err == nil || isPermanentTranscodeFailure(err) {
		t.Fatalf("volume busy: err = %v, want transient", err)
	}
	if err := checkOriginalBeforeDownload(ok, subject, 16<<30, scratchSpace{}, false); err != nil {
		t.Fatalf("unknown space: %v", err)
	}
	legacy := &transcodeSubject{ID: subject.ID}
	if err := checkOriginalBeforeDownload(changed, legacy, 16<<30, roomy, true); err != nil {
		t.Fatalf("no recorded etag: %v", err)
	}
}

func TestDownloadErrorClasses(t *testing.T) {
	ctx := context.Background()
	if err := classifyDownloadError(ctx, fmt.Errorf("%w: k", blob.ErrObjectChanged)); !isPermanentTranscodeFailure(err) {
		t.Fatalf("changed between stat and GET: %v", err)
	}
	if err := classifyDownloadError(ctx, fmt.Errorf("write: %w", syscall.ENOSPC)); err == nil || isPermanentTranscodeFailure(err) {
		t.Fatalf("disk full: %v, want transient", err)
	}
	if err := classifyDownloadError(ctx, errors.New("reset")); err == nil || isPermanentTranscodeFailure(err) {
		t.Fatalf("connection reset: %v", err)
	}
}

func TestScanErrorClasses(t *testing.T) {
	rejected := fmt.Errorf("%w: %w: too big", processing.ErrScanUnavailable, processing.ErrScanInputRejected)
	if err := classifyScanError(rejected); !isPermanentTranscodeFailure(err) {
		t.Fatalf("provider refused the frame: %v, want permanent", err)
	}
	if err := classifyScanError(fmt.Errorf("%w: throttled", processing.ErrScanUnavailable)); isPermanentTranscodeFailure(err) {
		t.Fatalf("throttling: %v, want transient", err)
	}
}

// ── finding 3: budgets ────────────────────────────────────────────────

func TestJobPastItsBudgetIsTerminal(t *testing.T) {
	parent := context.Background()
	job, cancel := context.WithTimeout(parent, time.Nanosecond)
	defer cancel()
	<-job.Done()
	if err := classifyJobEnd(parent, job, errors.New("signal: killed")); !errors.Is(err, errJobBudgetExceeded) || !isPermanentTranscodeFailure(err) {
		t.Fatalf("over budget: %v", err)
	}
	stopped, stop := context.WithCancel(parent)
	stop()
	if err := classifyJobEnd(stopped, job, context.Canceled); isPermanentTranscodeFailure(err) {
		t.Fatalf("worker shutdown made terminal: %v", err)
	}
	live, cancel2 := context.WithTimeout(parent, time.Hour)
	defer cancel2()
	if err := classifyJobEnd(parent, live, errors.New("blip")); isPermanentTranscodeFailure(err) {
		t.Fatalf("within budget made terminal: %v", err)
	}
}

type countingStamper struct {
	mu sync.Mutex
	n  int
}

func (c *countingStamper) StampTranscodeHeartbeat(context.Context, uuid.UUID) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return nil
}

func (c *countingStamper) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func TestHeartbeatStopsPastTheBudget(t *testing.T) {
	s := &countingStamper{}
	b := &jobBudget{}
	b.set(time.Now().Add(-time.Second))
	stop := startBudgetedHeartbeat(context.Background(), s, uuid.New(), 2*time.Millisecond, b)
	time.Sleep(30 * time.Millisecond)
	stop()
	if got := s.count(); got != 1 {
		t.Fatalf("stamps = %d, want only the first one", got)
	}

	live := &countingStamper{}
	lb := &jobBudget{}
	lb.set(time.Now().Add(time.Hour))
	stop = startBudgetedHeartbeat(context.Background(), live, uuid.New(), 2*time.Millisecond, lb)
	time.Sleep(30 * time.Millisecond)
	stop()
	if live.count() < 3 {
		t.Fatalf("a job inside its budget stopped stamping: %d", live.count())
	}
}

func TestS3TimeoutScalesWithSize(t *testing.T) {
	if s3Timeout(0) != s3CallBase || s3Timeout(1<<30) <= s3CallBase {
		t.Fatal("timeout does not scale with size")
	}
	if s3Timeout(1<<62) != s3TimeoutCapped {
		t.Fatal("timeout not capped")
	}
}

// ── finding 14: scratch ───────────────────────────────────────────────

func TestStaleScratchIsSweptAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"transcode-abc", "hls-abc", "fingerprint-abc", "keep"} {
		if err := os.MkdirAll(filepath.Join(dir, d, "inner"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "transcode-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := sweepStaleScratch(dir); n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	for _, gone := range []string{"transcode-abc", "hls-abc"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s survived", gone)
		}
	}
	for _, kept := range []string{"fingerprint-abc", "keep", "transcode-file"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Fatalf("%s removed: %v", kept, err)
		}
	}
}

func TestDeclaredContainerMustMatchTheDownloadedBytes(t *testing.T) {
	if err := checkSniffedContainer("video/mp4", processing.ContainerMOV); err != nil {
		t.Fatalf("a real MP4 refused: %v", err)
	}
	for _, c := range []struct{ mime, sniffed string }{
		{"video/mp4", processing.ContainerAVI}, {"video/webm", processing.ContainerMOV},
		{"video/mp4", ""}, {"text/html", processing.ContainerMOV},
	} {
		if err := checkSniffedContainer(c.mime, c.sniffed); !isPermanentTranscodeFailure(err) {
			t.Fatalf("%s holding %q: err = %v, want permanent", c.mime, c.sniffed, err)
		}
	}
}

// A subject whose key is not one asset's exact layout never has anything
// deleted on its behalf.
func TestGoneAssetCleanupNeverLeavesTheAssetPrefix(t *testing.T) {
	blobs := &prefixBlobs{keys: []string{"user/a/original", "user/a/b/c", "user/x"}}
	cleanupGoneAsset(context.Background(), blobs, &transcodeSubject{StorageKey: "user/a/original"})
	cleanupGoneAsset(context.Background(), blobs, &transcodeSubject{StorageKey: "original"})
	if len(blobs.deleted) != 0 {
		t.Fatalf("deleted %v for a malformed key", blobs.deleted)
	}
}
