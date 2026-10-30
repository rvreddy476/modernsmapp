package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
)

// Reels audit 2026-10-30 — the guards around one transcode.
//
//   - a message is retried at most maxAttempts times, then quarantined
//     through QuarantineTranscode, so no message can stall a partition
//     forever (finding 1);
//   - an asset deleted while its request was queued or running is a SKIP,
//     recorded in the inbox, never a retry and never poison (finding 1);
//   - the storage key and MIME come from the asset row, not the Kafka
//     message (finding 13);
//   - the original is checked against the size cap and the scratch disk
//     BEFORE it is downloaded, and downloaded pinned to the ETag recorded at
//     confirm (findings 2 and 6);
//   - every S3 call has a deadline (finding 14);
//   - stale scratch directories are removed at start (finding 14).

// ── configuration ─────────────────────────────────────────────────────

// workerConfig is the transcode policy, from the environment.
type workerConfig struct {
	// maxAttempts bounds the processing attempts of ONE message.
	maxAttempts int
	// limits are the probed-video bounds (processing.CheckVideoLimits).
	limits processing.VideoLimits
	// maxOriginalBytes refuses a larger original before downloading it.
	maxOriginalBytes int64
	// scratchDir holds every job's temp files.
	scratchDir string
}

const (
	// defaultMaxAttempts: with the 2 s doubling backoff capped at 2 min,
	// eight attempts span about a quarter of an hour of retries, enough to
	// ride out a database restart or a blob-store blip. A message still
	// failing after that is quarantined; its asset stays 'processing' and
	// the stall sweeper re-queues it with a fresh request (and fails it
	// after its own attempts cap), so nothing is lost silently.
	defaultMaxAttempts = 8
	// defaultMaxOriginalBytes: the worker scratch volume is 50 Gi in prod
	// and needs the original plus every rendition and the HLS ladder
	// (~2-3x the original).
	defaultMaxOriginalBytes int64 = 16 << 30
	// scratchHeadroom is how many times the original's size must be free
	// on the scratch volume before the download starts.
	scratchHeadroom = 3
)

func loadWorkerConfig(getenv func(string) string) workerConfig {
	lim := processing.DefaultVideoLimits()
	lim.MaxDurationSeconds = envFloat(getenv, "MEDIA_MAX_VIDEO_DURATION_SEC", lim.MaxDurationSeconds)
	lim.MinShortSide = envIntFrom(getenv, "MEDIA_MIN_VIDEO_SHORT_SIDE", lim.MinShortSide)
	lim.MaxLongSide = envIntFrom(getenv, "MEDIA_MAX_VIDEO_LONG_SIDE", lim.MaxLongSide)
	lim.MaxPixels = envIntFrom(getenv, "MEDIA_MAX_VIDEO_PIXELS", lim.MaxPixels)
	lim.MaxFrameRate = envFloat(getenv, "MEDIA_MAX_VIDEO_FPS", lim.MaxFrameRate)
	cfg := workerConfig{
		maxAttempts:      envIntFrom(getenv, "MEDIA_TRANSCODE_MAX_ATTEMPTS", defaultMaxAttempts),
		limits:           lim,
		maxOriginalBytes: int64(envFloat(getenv, "MEDIA_WORKER_MAX_ORIGINAL_BYTES", float64(defaultMaxOriginalBytes))),
		scratchDir:       os.TempDir(),
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	return cfg
}

func envIntFrom(getenv func(string) string, key string, def int) int {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
		log.Printf("%s=%q is not a non-negative integer; using %d", key, v, def)
	}
	return def
}

func envFloat(getenv func(string) string, key string, def float64) float64 {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			return f
		}
		log.Printf("%s=%q is not a non-negative number; using %v", key, v, def)
	}
	return def
}

// ── the retry loop ────────────────────────────────────────────────────

type retryPolicy struct {
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// errAttemptsExhausted wraps the last error of a message that failed
// maxAttempts times; it is what the quarantine row records.
var errAttemptsExhausted = errors.New("transcode attempts exhausted")

// retryUntilDurable runs attempt until its effect is durable, and reports
// whether the message may now be committed; false means ctx ended first
// and the offset must stay where it is.
//
// A permanent failure is quarantined at once. A transient one is retried
// with backoff, at most p.maxAttempts times in all; the last failure is then
// quarantined too. Before the cap a message that could never succeed (an
// error misclassified as transient) retried forever and stalled every
// transcode behind it on its partition. A quarantine that cannot be
// written leaves the offset uncommitted and keeps trying: committing past
// a message with no durable record of it would lose it silently.
func retryUntilDurable(ctx context.Context, m kafka.Message, attempt func() error,
	quarantine func(context.Context, kafka.Message, error) error, p retryPolicy) bool {
	backoff := p.initialBackoff
	for n := 1; ; n++ {
		err := attempt()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		exhausted := !isPermanentTranscodeFailure(err) && n >= p.maxAttempts
		if exhausted {
			err = fmt.Errorf("%w after %d attempts: %w", errAttemptsExhausted, n, err)
		}
		if isPermanentTranscodeFailure(err) || exhausted {
			if qerr := quarantine(ctx, m, err); qerr != nil {
				log.Printf("Failed to quarantine transcode; offset NOT committed: %v", qerr)
			} else {
				log.Printf("PERMANENTLY UNDELIVERABLE transcode message partition=%d offset=%d: %v "+
					"(durably quarantined; committing so the partition is not stalled)", m.Partition, m.Offset, err)
				return true
			}
		}
		log.Printf("Transcode effect failed (attempt %d of %d, partition=%d offset=%d), "+
			"offset NOT committed, retrying in %s: %v", n, p.maxAttempts, m.Partition, m.Offset, backoff, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		if backoff < p.maxBackoff {
			if backoff *= 2; backoff > p.maxBackoff {
				backoff = p.maxBackoff
			}
		}
	}
}

// ── the subject: what to transcode, from the asset row ───────────────

// errAssetGone: the asset row does not exist (deleted mid-queue).
var errAssetGone = errors.New("transcode target asset no longer exists")

// transcodeSubject is the asset as its row describes it.
type transcodeSubject struct {
	ID           uuid.UUID
	UploaderID   uuid.UUID
	StorageKey   string
	MimeType     string
	OriginalETag string
}

// assetPrefix is the asset's storage prefix "user/<uploader>/<id>/".
func (s *transcodeSubject) assetPrefix() string {
	return strings.TrimSuffix(s.StorageKey, "original")
}

type mediaReader interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
}

// resolveTranscodeSubject reads the asset the request names. The storage
// key and MIME come from the ROW (finding 13): the worker used to read and
// overwrite whatever key the Kafka message named, and media.events is a
// shared topic. The key must be exactly the asset's own
// user/<uploader>/<id>/original; anything else is permanent. A missing row
// is errAssetGone.
func resolveTranscodeSubject(ctx context.Context, store mediaReader, mediaID uuid.UUID,
	payload postgres.TranscodeRequestPayload) (*transcodeSubject, error) {
	m, err := store.GetMedia(ctx, mediaID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrMediaNotFound) || (err == nil && m == nil) {
		return nil, errAssetGone
	}
	if err != nil {
		return nil, fmt.Errorf("read asset %s: %w", mediaID, err)
	}
	if m.FileType != "video" {
		return nil, permanentTranscode(fmt.Errorf("asset %s is %q, not a video", mediaID, m.FileType))
	}
	want := fmt.Sprintf("user/%s/%s/original", m.UploaderID, m.ID)
	if m.StorageKey != want {
		return nil, permanentTranscode(fmt.Errorf("asset %s has an unexpected storage key", mediaID))
	}
	if payload.StorageKey != "" && payload.StorageKey != m.StorageKey {
		log.Printf("Warning: transcode request for %s names a storage key that is not the asset's; using the row's", mediaID)
	}
	return &transcodeSubject{
		ID: m.ID, UploaderID: m.UploaderID, StorageKey: m.StorageKey,
		MimeType: m.MimeType, OriginalETag: m.OriginalETag,
	}, nil
}

// ── before the download ───────────────────────────────────────────────

// scratchSpace is the scratch filesystem's free and total bytes.
type scratchSpace struct{ free, total int64 }

// checkOriginalBeforeDownload decides, from the object's stat and the
// scratch volume, whether the download may start:
//
//   - an ETag other than the one recorded at confirm: permanent (the
//     original was replaced after confirm; finding 6);
//   - larger than the cap, or larger than the scratch volume could ever
//     hold with headroom: permanent (finding 2: a 25-60 GiB upload filled
//     the 50 Gi emptyDir and was retried forever);
//   - not enough FREE space right now: transient (other work on the volume
//     may finish), bounded by the attempts cap.
func checkOriginalBeforeDownload(info blob.ObjectInfo, subject *transcodeSubject, maxBytes int64, space scratchSpace, spaceKnown bool) error {
	if subject.OriginalETag != "" && info.ETag != subject.OriginalETag {
		return permanentTranscode(fmt.Errorf("%w: original of %s changed after it was confirmed", blob.ErrObjectChanged, subject.ID))
	}
	if maxBytes > 0 && info.Size > maxBytes {
		return permanentTranscode(fmt.Errorf("original of %s is %d bytes, over the %d-byte cap", subject.ID, info.Size, maxBytes))
	}
	if !spaceKnown {
		return nil
	}
	need := info.Size * scratchHeadroom
	if space.total > 0 && need > space.total {
		return permanentTranscode(fmt.Errorf("original of %s (%d bytes) cannot fit the %d-byte scratch volume with headroom",
			subject.ID, info.Size, space.total))
	}
	if need > space.free {
		return fmt.Errorf("scratch volume has %d bytes free, %d needed for %s", space.free, need, subject.ID)
	}
	return nil
}

// classifyDownloadError: a changed object (If-Match failed) is permanent;
// a full disk mid-download is transient (bounded by the attempts cap; the
// pre-check refuses what can never fit); anything else keeps its class.
func classifyDownloadError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, blob.ErrObjectChanged):
		return permanentTranscode(fmt.Errorf("download original: %w", err))
	case errors.Is(err, syscall.ENOSPC):
		return fmt.Errorf("download original: scratch volume full: %w", err)
	case ctx.Err() != nil:
		return err
	}
	return fmt.Errorf("download original: %w", err)
}

// ── deadlines ─────────────────────────────────────────────────────────

const (
	s3CallBase      = 2 * time.Minute
	s3BytesPerSec   = 8 << 20 // a pessimistic 8 MiB/s
	s3MetadataCall  = time.Minute
	s3TimeoutCapped = 6 * time.Hour
)

// s3Timeout bounds one blob call moving size bytes (finding 14: the
// worker's S3 calls ran under the process context, so a hung connection
// stalled the consumer with its heartbeat still fresh).
func s3Timeout(size int64) time.Duration {
	if size <= 0 {
		return s3CallBase
	}
	secs := float64(size) / s3BytesPerSec
	if secs >= s3TimeoutCapped.Seconds() {
		return s3TimeoutCapped
	}
	return s3CallBase + time.Duration(secs*float64(time.Second))
}

// jobBudget is the wall-clock deadline of one transcode, shared with its
// heartbeat: past it the heartbeat stops extending the lease (finding 3),
// so a job that runs over is reclaimed by the stall sweeper instead of
// looking alive forever.
type jobBudget struct{ deadline atomic.Int64 }

func (b *jobBudget) set(t time.Time) {
	if b != nil {
		b.deadline.Store(t.UnixNano())
	}
}

// exceeded reports whether now is past a set deadline.
func (b *jobBudget) exceeded(now time.Time) bool {
	if b == nil {
		return false
	}
	d := b.deadline.Load()
	return d != 0 && now.UnixNano() > d
}

// ── scratch ───────────────────────────────────────────────────────────

// scratchPrefixes are the temp directories transcode jobs create.
var scratchPrefixes = []string{"transcode-", "hls-"}

// sweepStaleScratch removes every transcode/HLS temp directory in dir.
// It runs once at start, before the consumer takes a message, so nothing
// in this process is using them: they are what an OOM kill or eviction
// left behind on the scratch volume, which survives container restarts
// (finding 14). Only directories with the job prefixes, directly in dir,
// are touched.
func sweepStaleScratch(dir string) (removed int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("Warning: scratch sweep of %s skipped: %v", dir, err)
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() || !hasScratchPrefix(e.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			log.Printf("Warning: stale scratch %s not removed: %v", e.Name(), err)
			continue
		}
		removed++
	}
	return removed
}

func hasScratchPrefix(name string) bool {
	for _, p := range scratchPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ── a deleted asset ───────────────────────────────────────────────────

type prefixCleaner interface {
	ListObjectKeys(ctx context.Context, prefix string) ([]string, error)
	DeleteObject(ctx context.Context, objectKey string) error
}

// cleanupGoneAsset removes what this run uploaded under the asset's prefix
// after the asset was deleted: renditions and HLS objects written after
// the owner's delete have no row and no reclaim record. Best-effort and
// bounded; only ever the asset's own exact prefix.
func cleanupGoneAsset(ctx context.Context, blobs prefixCleaner, subject *transcodeSubject) {
	if subject == nil || blobs == nil {
		return
	}
	prefix := subject.assetPrefix()
	if !postgres.IsAssetPrefix(prefix) {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, s3MetadataCall)
	keys, err := blobs.ListObjectKeys(lctx, prefix)
	cancel()
	if err != nil {
		log.Printf("Warning: objects of deleted media %s not listed: %v", subject.ID, err)
		return
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, s3MetadataCall)
		if err := blobs.DeleteObject(dctx, k); err != nil {
			log.Printf("Warning: object of deleted media %s not removed: %v", subject.ID, err)
		}
		cancel()
	}
}
