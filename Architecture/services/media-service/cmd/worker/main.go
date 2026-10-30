package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/transport"
	"github.com/buckket/go-blurhash"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/segmentio/kafka-go"
)

// Video-domain metrics for the transcode worker. The worker is a separate
// process with no HTTP surface of its own, so it exposes its own /metrics
// endpoint (see startMetricsServer).
var (
	transcodeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_transcode_total",
		Help: "Video transcode jobs processed, by result.",
	}, []string{"result"})
	transcodeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "media_transcode_duration_seconds",
		Help:    "Wall-clock duration of a video transcode job.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600},
	})
)

// startMetricsServer serves Prometheus metrics for the worker process.
func startMetricsServer() {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9091"
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	log.Printf("Worker metrics listening on :%s/metrics", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Printf("Worker metrics server stopped: %v", err)
	}
}

func main() {
	// Config
	pgDSN := os.Getenv("POSTGRES_DSN")
	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	minioAccessKey := os.Getenv("MINIO_ACCESS_KEY")
	minioSecretKey := os.Getenv("MINIO_SECRET_KEY")
	minioBucket := os.Getenv("MINIO_BUCKET")
	minioUseSSL := os.Getenv("MINIO_USE_SSL") == "true"
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")

	if minioEndpoint == "" {
		minioEndpoint = "minio:9000"
		minioAccessKey = "minioadmin"
		minioSecretKey = "minioadmin"
		minioBucket = "media"
	}
	if kafkaBrokers == "" {
		kafkaBrokers = "kafka:9092"
	}

	brokers := strings.Split(kafkaBrokers, ",")

	// M4-P0-3 — the content-safety scanner, resolved BEFORE any external
	// dependency.
	//
	// This was unconditionally StubScanner, which returns "safe" for
	// everything. Combined with the moderation status defaulting to "passed",
	// the worker approved every video it ever saw while looking like it had a
	// safety gate. Production now REFUSES TO START on the stub: a media worker
	// that cannot scan is not a degraded worker, it is an approval machine.
	//
	// It is checked here, first, on purpose. A configuration refusal must not
	// depend on PostgreSQL or the blob store being reachable — otherwise the
	// operator sees "cannot connect to MinIO" and never learns that the safety
	// gate was misconfigured too.
	scanner, scannerName, err := selectScanner()
	if err != nil {
		log.Fatalf("Content scanner: %v", err)
	}
	log.Printf("Content scanner: %s", scannerName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	poolCfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		log.Fatalf("Unable to parse Postgres config: %v\n", err)
	}
	poolCfg.MaxConns = 25
	poolCfg.MinConns = 5
	poolCfg.MaxConnLifetime = 15 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	dbPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("Unable to connect to Postgres: %v\n", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("Postgres ping failed: %v\n", err)
	}
	log.Println("Connected to Postgres")

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		log.Fatalf("Failed to bootstrap media schema: %v\n", err)
	}
	log.Println("Media schema ready")

	// Blob store. Production is AWS-only and requires an EKS web-identity
	// token; MinIO remains available only for local integration tests.
	var blobStore *blob.Store
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MEDIA_STORAGE_BACKEND")), "s3") {
		blobStore, err = blob.NewS3IRSA(os.Getenv("AWS_REGION"), os.Getenv("S3_BUCKET"))
	} else if isWorkerProduction() {
		log.Fatal("Production requires MEDIA_STORAGE_BACKEND=s3 with IRSA; MinIO/static credentials are forbidden")
	} else {
		blobStore, err = blob.New(minioEndpoint, minioAccessKey, minioSecretKey, minioBucket, minioUseSSL)
	}
	if err != nil {
		log.Fatalf("Unable to configure blob store: %v\n", err)
	}
	log.Println("Connected to blob store")

	pgStore := postgres.New(dbPool)
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		log.Fatalf("Failed to configure Kafka dialer: %v\n", err)
	}
	// Kafka consumer
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  "media-transcode-worker",
		Topic:    "media.events",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   kafkaDialer,
	})

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("Shutting down worker...")
		cancel()
	}()

	go startMetricsServer()

	// Scratch left by a job that was OOM-killed or evicted (finding 14).
	// Nothing in this process uses it yet: the consumer starts below.
	if n := sweepStaleScratch(activeConfig.scratchDir); n > 0 {
		log.Printf("Removed %d stale transcode scratch director(ies) from %s", n, activeConfig.scratchDir)
	}
	log.Printf("Transcode policy: max %d attempts per message, max original %d bytes, limits %+v",
		activeConfig.maxAttempts, activeConfig.maxOriginalBytes, activeConfig.limits)

	// Transcode lease sweeper (migration 021): re-queues assets whose job
	// died mid-transcode, fails them after the attempts cap. Runs beside the
	// consumer loop; the tracker tells it whether this worker is busy.
	tracker := newBusyTracker(time.Now())
	if stallSweepEnabled() {
		go runStallSweeper(ctx, pgStore, tracker, postgres.DefaultStallPolicy(), stallSweepInterval)
	} else {
		log.Println("Transcode stall sweeper DISABLED (MEDIA_TRANSCODE_STALL_SWEEP=off)")
	}

	// Readiness gauges (P-16) and the Copyright Match fingerprint runner
	// (shadow mode; claims nothing unless COPYRIGHT_FINGERPRINT_ENABLED).
	go runReadinessGauges(ctx, pgStore, 30*time.Second)
	go runFingerprintLoop(ctx, &fingerprintRunner{
		store:      pgStore,
		blobs:      blobStore,
		tracker:    tracker,
		loadConfig: loadFingerprintConfig,
		scratchDir: os.TempDir(),
	})

	log.Println("Media transcode worker started, waiting for messages...")

	// M4-P0-3 — THE OFFSET IS COMMITTED BY THIS LOOP, AND ONLY AFTER THE
	// DURABLE EFFECT.
	//
	// This used to call ReadMessage, which for a group reader commits the
	// offset BEFORE returning the message. A transcode that then failed on a
	// transient fault — blob store blip, database restart — was acknowledged
	// and never retried, and the asset stayed stuck forever with no job.
	//
	// FetchMessage does not commit. CommitMessages below does, after
	// CompleteTranscode has durably recorded the outcome.
	for {
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("Fetch error: %v\n", err)
			continue
		}

		tracker.begin()
		durable := handleUntilDurable(ctx, m, pgStore, blobStore, scanner)
		tracker.end(time.Now())
		if !durable {
			// Shutting down mid-message. The offset is deliberately left
			// uncommitted so the next owner of this partition redelivers it.
			log.Println("Context cancelled before commit; stopping without committing")
			break
		}
		if err := reader.CommitMessages(ctx, m); err != nil {
			// The effect is durable and idempotent, so the redelivery this
			// causes is absorbed by the inbox.
			log.Printf("Commit error (message will be redelivered): %v", err)
		}
	}

	_ = reader.Close()
	log.Println("Worker stopped")
}

func processMessage(ctx context.Context, m kafka.Message, pgStore *postgres.MediaAssetStore, blobStore *blob.Store, scanner processing.Scanner) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		// No redelivery will make this parse. Marked permanent so the retry
		// loop commits past it instead of stalling the partition — every
		// transcode queued behind one corrupt record would otherwise never run.
		return permanentTranscode(fmt.Errorf("unmarshal envelope: %w", err))
	}

	if envelope.EventType != events.MediaTranscodeRequested {
		return nil // skip non-transcode events
	}
	if envelope.EventID == "" {
		return permanentTranscode(fmt.Errorf("transcode request has no event_id"))
	}

	// The store's payload type: the shared request plus the operator
	// rotation override a reprocess may carry (absent on a normal upload).
	var payload postgres.TranscodeRequestPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentTranscode(fmt.Errorf("unmarshal payload: %w", err))
	}

	mediaAssetID, err := uuid.Parse(payload.MediaAssetID)
	if err != nil {
		return permanentTranscode(fmt.Errorf("parse media_asset_id: %w", err))
	}

	env := transcodeEnv{store: pgStore, blobs: blobStore, scanner: scanner, cfg: activeConfig}
	// Cheap replay check before expensive work. The guarantee is the inbox
	// primary key inside CompleteTranscode; this only avoids re-running ffmpeg
	// over an asset another replica already finished.
	return pgStore.WithTranscodeEventLock(ctx, envelope.EventID, func() error {
		var blobs prefixCleaner
		if blobStore != nil {
			blobs = blobStore
		}
		return processTranscodeLocked(ctx, envelope, payload, mediaAssetID, pgStore, blobs,
			func(ctx context.Context, subject *transcodeSubject, budget *jobBudget, moderation *string) error {
				return transcodeVideo(ctx, env, subject, payload, budget, moderation)
			})
	})
}

// lockedStore is what processTranscodeLocked needs from the store.
type lockedStore interface {
	mediaReader
	requestLookup
	heartbeatStamper
	AlreadyApplied(ctx context.Context, eventID string) (bool, error)
	RecordTranscodeSkipped(ctx context.Context, eventID string, mediaAssetID uuid.UUID) error
	GetTranscodeTiming(ctx context.Context, id uuid.UUID) (postgres.TranscodeTiming, error)
	CompleteTranscode(ctx context.Context, eventID string, mediaAssetID uuid.UUID, outcome, hlsMasterKey,
		moderationStatus string, completion postgres.TranscodeCompletion) error
	ActivatePendingSlot(ctx context.Context, mediaAssetID uuid.UUID) error
}

// transcodeRun is one transcode of a resolved subject (transcodeVideo in
// production). It sets *moderation on success and extends budget once it
// knows the video's duration.
type transcodeRun func(ctx context.Context, subject *transcodeSubject, budget *jobBudget, moderation *string) error

func processTranscodeLocked(ctx context.Context, envelope events.EventEnvelope,
	payload postgres.TranscodeRequestPayload, mediaAssetID uuid.UUID,
	store lockedStore, blobs prefixCleaner, run transcodeRun) error {
	if done, err := store.AlreadyApplied(ctx, envelope.EventID); err != nil {
		return fmt.Errorf("inbox lookup: %w", err)
	} else if done {
		log.Printf("Transcode for media %s already applied; skipping", payload.MediaAssetID)
		return nil
	}
	if superseded, current := supersededDelivery(ctx, store, mediaAssetID, envelope.EventID); superseded {
		log.Printf("Transcode event %q for media %s superseded by re-queued event %q; skipping", //nolint:gosec // G706: the event ids are %q-quoted; the media id is a parsed uuid
			envelope.EventID, mediaAssetID, current)
		return nil
	}

	// The asset as its ROW describes it (findings 1 and 13). A request
	// for an asset that was deleted while it waited is recorded and
	// skipped: before, the download's NotFound became a terminal failure
	// that could not be written (no row), was retried forever, and stalled
	// the partition.
	subject, err := resolveTranscodeSubject(ctx, store, mediaAssetID, payload)
	if errors.Is(err, errAssetGone) {
		return skipGoneAsset(ctx, store, envelope.EventID, mediaAssetID)
	}
	if err != nil {
		return err
	}

	log.Printf("Processing video transcode for media %s (generation %d)", payload.MediaAssetID, payload.MediaGeneration)

	// Readiness metrics (P-16): queue wait now, confirm→ready at the end.
	// A failed read costs the observation, never the job.
	timing, timingErr := store.GetTranscodeTiming(ctx, mediaAssetID)
	if timingErr != nil {
		log.Printf("Warning: transcode timing for %s not read: %v", payload.MediaAssetID, timingErr)
	} else {
		observeQueueWait(timing.RequestedAt, time.Now())
	}

	// The lease: stamped now and every heartbeatInterval until this returns,
	// or until the job is past its budget (finding 3). A failed stamp is
	// logged inside and never fails the job.
	budget := &jobBudget{}
	stopHeartbeat := startBudgetedHeartbeat(ctx, store, mediaAssetID, heartbeatInterval, budget)
	defer stopHeartbeat()

	transcodeStart := time.Now()
	moderationResult := ""
	transcodeErr := run(ctx, subject, budget, &moderationResult)
	transcodeDuration.Observe(time.Since(transcodeStart).Seconds())

	if transcodeErr != nil {
		if ctx.Err() == nil && assetGone(ctx, store, mediaAssetID) {
			// Deleted while it ran (an FK failure on the variants, a
			// vanished original): not a failure of the upload.
			transcodeTotal.WithLabelValues("skipped").Inc()
			cleanupGoneAsset(ctx, blobs, subject)
			return skipGoneAsset(ctx, store, envelope.EventID, mediaAssetID)
		}
		transcodeTotal.WithLabelValues("failure").Inc()
		log.Printf("Transcode failed for %s: %v", payload.MediaAssetID, transcodeErr)

		// A transient failure must NOT be recorded terminal. The old code
		// marked every failure "failed" and returned nil — a blob-store blip
		// or a database restart permanently killed an upload the user could
		// not retry. Only a genuinely unprocessable input is terminal.
		if !isPermanentTranscodeFailure(transcodeErr) {
			return fmt.Errorf("transient transcode failure for %s: %w",
				payload.MediaAssetID, transcodeErr)
		}
		// Terminal: record it durably, and record it as NOT publishable.
		if err := store.CompleteTranscode(ctx, envelope.EventID, mediaAssetID,
			"failed", "", "manual_review", postgres.TranscodeCompletion{
				ProcessingStatus: "failed", ModerationStatus: "manual_review",
				MediaGeneration: payload.MediaGeneration,
			}); err != nil {
			if errors.Is(err, postgres.ErrTranscodeAlreadyApplied) {
				return nil
			}
			if errors.Is(err, postgres.ErrTranscodeAssetGone) { // deleted before its failure was recorded
				cleanupGoneAsset(ctx, blobs, subject)
				return nil
			}
			if errors.Is(err, postgres.ErrTranscodeGenerationStale) {
				log.Printf("Transcode failure for media %s generation %d not recorded: a newer generation owns the asset",
					payload.MediaAssetID, payload.MediaGeneration)
				return nil
			}
			return fmt.Errorf("record terminal failure for %s: %w", payload.MediaAssetID, err)
		}
		if timingErr == nil {
			observeReadyLatency(timing.UploadConfirmedAt, time.Now(), "failed")
		}
		return nil
	}

	// Read back what transcode wrote so the completion event can carry it.
	asset, fetchErr := store.GetMedia(ctx, mediaAssetID)
	if errors.Is(fetchErr, pgx.ErrNoRows) || (fetchErr == nil && asset == nil) {
		transcodeTotal.WithLabelValues("skipped").Inc()
		cleanupGoneAsset(ctx, blobs, subject)
		return skipGoneAsset(ctx, store, envelope.EventID, mediaAssetID)
	}
	if fetchErr != nil {
		// This used to be ignored, and the moderation status then defaulted to
		// "passed" — so a failed read-back PUBLISHED unreviewed media. The read
		// is now required.
		return fmt.Errorf("read back media %s after transcode: %w", payload.MediaAssetID, fetchErr)
	}
	transcodeTotal.WithLabelValues("success").Inc()

	// THE SAFETY DEFAULT. There is no "passed" fallback: if the scan did not
	// produce a verdict, the asset goes to manual review. Decision 001 A1.1 —
	// provider failure is not approval.
	modStatus := moderationResult
	if modStatus == "" {
		log.Printf("media %s finished transcode with no moderation verdict; holding for review",
			payload.MediaAssetID)
		modStatus = "manual_review"
	}

	hlsURL, mp4URL, thumbURL := "", "", ""
	if asset.HLSMasterKey != "" {
		hlsURL = "/" + strings.TrimPrefix(asset.HLSMasterKey, "/")
	}
	if asset.CdnURL != nil && *asset.CdnURL != "" {
		mp4URL = *asset.CdnURL
	}
	if asset.ThumbnailURL != nil && *asset.ThumbnailURL != "" {
		thumbURL = *asset.ThumbnailURL
	}

	if err := store.CompleteTranscode(ctx, envelope.EventID, mediaAssetID,
		"ready", asset.HLSMasterKey, modStatus, postgres.TranscodeCompletion{
			ProcessingStatus: "ready",
			HLSMasterURL:     hlsURL,
			MP4URL:           mp4URL,
			ThumbnailURL:     thumbURL,
			ModerationStatus: modStatus,
			MediaGeneration:  payload.MediaGeneration,
		}); err != nil {
		if errors.Is(err, postgres.ErrTranscodeAlreadyApplied) {
			log.Printf("Transcode for media %s completed by another replica", payload.MediaAssetID)
			return nil
		}
		if errors.Is(err, postgres.ErrTranscodeAssetGone) { // deleted before completion
			// Deleted between the read-back and the completion: the inbox row
			// is recorded as a skip; what this run uploaded is removed.
			cleanupGoneAsset(ctx, blobs, subject)
			return nil
		}
		if errors.Is(err, postgres.ErrTranscodeGenerationStale) {
			// A re-queue bumped the generation while this ran; the objects
			// this run wrote are being rewritten by the current generation's
			// own run, which will record the asset's state (P-14, rule 3).
			log.Printf("Transcode for media %s generation %d finished after a newer generation was queued; outcome not applied",
				payload.MediaAssetID, payload.MediaGeneration)
			return nil
		}
		return fmt.Errorf("record transcode completion for %s: %w", payload.MediaAssetID, err)
	}
	if timingErr == nil {
		observeReadyLatency(timing.UploadConfirmedAt, time.Now(), "ready")
	}
	if modStatus == "passed" {
		if err := store.ActivatePendingSlot(ctx, mediaAssetID); err != nil {
			log.Printf("Warning: failed to activate pending media slots for %s: %v", payload.MediaAssetID, err)
		}
	}
	log.Printf("Transcode completed for media %s (hls=%t, moderation=%s)",
		payload.MediaAssetID, hlsURL != "", modStatus)
	return nil
}

// assetGone reports whether the asset row is now absent. A failed read is
// "not known to be gone": the caller keeps its own classification.
func assetGone(ctx context.Context, store mediaReader, id uuid.UUID) bool {
	m, err := store.GetMedia(ctx, id)
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrMediaNotFound) || (err == nil && m == nil)
}

// skipGoneAsset records a request for a deleted asset as applied. The
// offset may then advance; a failure to record is transient.
func skipGoneAsset(ctx context.Context, store lockedStore, eventID string, mediaAssetID uuid.UUID) error {
	if err := store.RecordTranscodeSkipped(ctx, eventID, mediaAssetID); err != nil {
		return fmt.Errorf("record skipped transcode: %w", err)
	}
	log.Printf("Transcode for media %s skipped: the asset was deleted", mediaAssetID)
	return nil
}

// permanentTranscodeError marks input that redelivery cannot repair.
type permanentTranscodeError struct{ err error }

func (e *permanentTranscodeError) Error() string { return "permanent: " + e.err.Error() }
func (e *permanentTranscodeError) Unwrap() error { return e.err }

// permanentTranscode wraps an error as unprocessable input.
func permanentTranscode(err error) error { return &permanentTranscodeError{err: err} }

// isPermanentTranscodeFailure decides retry versus terminal.
//
// The distinction is the whole point of the change: a corrupt upload must not
// stall the partition forever, and a database outage must not permanently kill
// a perfectly good video. Anything not explicitly marked unprocessable is
// treated as transient, because the cost of an extra retry is far below the
// cost of destroying a user's upload. The retry loop's attempts cap is the
// backstop for a deterministic error misclassified as transient.
func isPermanentTranscodeFailure(err error) bool {
	var perm *permanentTranscodeError
	return errors.As(err, &perm)
}

// permanentUnlessCancelled classifies deterministic media/ffmpeg failures as
// terminal while preserving cancellation/timeouts as retryable. A corrupt
// upload must become a durable failed result; it must not stall every later
// upload on its Kafka partition forever.
func permanentUnlessCancelled(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return permanentTranscode(err)
}

// activeConfig is the worker's transcode policy, read from the environment
// once.
var activeConfig = loadWorkerConfig(os.Getenv)

// handleUntilDurable retries a message until its effect is durably recorded,
// at most activeConfig.maxAttempts times (retryUntilDurable). Reports
// whether the message may now be committed; false means the context was
// cancelled first and the offset must be left where it is.
func handleUntilDurable(
	ctx context.Context,
	m kafka.Message,
	pgStore *postgres.MediaAssetStore,
	blobStore *blob.Store,
	scanner processing.Scanner,
) bool {
	return retryUntilDurable(ctx, m,
		func() error { return processMessage(ctx, m, pgStore, blobStore, scanner) },
		pgStore.QuarantineTranscode,
		retryPolicy{maxAttempts: activeConfig.maxAttempts, initialBackoff: 2 * time.Second, maxBackoff: 2 * time.Minute})
}

// transcodeEnv is what one transcode works with.
type transcodeEnv struct {
	store   *postgres.MediaAssetStore
	blobs   *blob.Store
	scanner processing.Scanner
	cfg     workerConfig
}

// errJobBudgetExceeded: the transcode ran past processing.TranscodeJobBudget.
var errJobBudgetExceeded = errors.New("transcode exceeded its time budget")

// transcodeVideo runs one transcode and classifies its end: a job that ran
// out of its own budget while the worker is alive is terminal (the same
// input would run out again), not a retry.
func transcodeVideo(ctx context.Context, env transcodeEnv, subject *transcodeSubject,
	payload postgres.TranscodeRequestPayload, budget *jobBudget, moderationResult *string) error {
	var jobCtx context.Context = ctx
	cancelJob := func() {}
	defer func() { cancelJob() }()
	setDeadline := func(d time.Duration) context.Context {
		cancelJob()
		var c context.Context
		c, cancelJob = context.WithTimeout(ctx, d)
		budget.set(time.Now().Add(d))
		jobCtx = c
		return c
	}
	err := runTranscode(ctx, env, subject, payload, setDeadline, moderationResult)
	return classifyJobEnd(ctx, jobCtx, err)
}

// classifyJobEnd makes a failure terminal when the job's own deadline
// ended it while the worker (parent) is still running.
func classifyJobEnd(parent, job context.Context, err error) error {
	if err != nil && parent.Err() == nil && errors.Is(job.Err(), context.DeadlineExceeded) {
		return permanentTranscode(fmt.Errorf("%w: %v", errJobBudgetExceeded, err))
	}
	return err
}

// runTranscode is the pipeline. setDeadline replaces the job's deadline
// (measured from now) and returns the context bounded by it.
func runTranscode(parent context.Context, env transcodeEnv, subject *transcodeSubject,
	payload postgres.TranscodeRequestPayload, setDeadline func(time.Duration) context.Context,
	moderationResult *string) error {
	pgStore, blobStore, scanner := env.store, env.blobs, env.scanner
	mediaAssetID := subject.ID

	// 1+2. Stream the original from object storage straight into a temp
	// file. Long videos have no size limit of their own, so the original is
	// never read into memory: the worker container runs under a 2G limit and
	// a multi-GiB io.ReadAll would be OOM-killed. The temp volume needs room
	// for the original plus every rendition and the HLS ladder (roughly 2-3x
	// the original), which checkOriginalBeforeDownload verifies first.
	tmpDir, err := os.MkdirTemp(env.cfg.scratchDir, "transcode-"+mediaAssetID.String())
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	statCtx, cancel := context.WithTimeout(parent, s3MetadataCall)
	info, err := blobStore.StatObject(statCtx, subject.StorageKey)
	cancel()
	if err != nil {
		if errors.Is(err, blob.ErrObjectNotFound) {
			return permanentTranscode(fmt.Errorf("stat original: %w", err))
		}
		return fmt.Errorf("stat original: %w", err)
	}
	// The ETag pin (finding 6). An operator rotation re-run rewrites the
	// original itself, so it is not pinned to the upload's ETag.
	pinned := *subject
	if payload.RotateDegrees != 0 {
		pinned.OriginalETag = ""
	}
	space, spaceKnown := scratchSpaceOf(tmpDir)
	if err := checkOriginalBeforeDownload(info, &pinned, env.cfg.maxOriginalBytes, space, spaceKnown); err != nil {
		return err
	}

	// Download, bounded, and conditional on the ETag just checked: a
	// replace between the stat and the GET is refused, not transcoded.
	ctx := setDeadline(s3Timeout(info.Size) + 2*processing.ProbeTimeout + 10*time.Minute)
	inputPath := tmpDir + "/original"
	pin := pinned.OriginalETag
	if pin == "" {
		pin = info.ETag
	}
	dlCtx, cancel := context.WithTimeout(ctx, s3Timeout(info.Size))
	_, err = blobStore.DownloadObjectToFileIfMatch(dlCtx, subject.StorageKey, inputPath, pin)
	cancel()
	if err != nil {
		if errors.Is(err, blob.ErrObjectNotFound) {
			return permanentTranscode(fmt.Errorf("download original: %w", err))
		}
		return classifyDownloadError(parent, err)
	}

	// The bytes must be the container the upload declared (finding 7), and
	// one ffmpeg is allowed to open at all.
	sniffed, err := processing.SniffVideoFile(inputPath)
	if err != nil {
		return fmt.Errorf("read original header: %w", err)
	}
	if err := checkSniffedContainer(subject.MimeType, sniffed); err != nil {
		return err
	}

	// 2b. Operator rotation override (reprocess only). The pixels are
	// sideways and the container says nothing about it; stamp the display
	// rotation onto a stream-copied original and work from that. The tagged
	// copy REPLACES the original object under the same key, so the uploader
	// opening the original sees it upright too, and a later plain re-run
	// reads the rotation from the file itself.
	if payload.RotateDegrees != 0 {
		rotatedPath := tmpDir + "/original_rotated.mp4"
		if err := processing.ApplyDisplayRotation(ctx, inputPath, rotatedPath, payload.RotateDegrees); err != nil {
			return permanentUnlessCancelled(parent, fmt.Errorf("apply rotation override: %w", err))
		}
		upCtx, cancel := context.WithTimeout(ctx, s3Timeout(info.Size))
		rotated, err := blobStore.UploadFileInfo(upCtx, subject.StorageKey, rotatedPath, subject.MimeType)
		cancel()
		if err != nil {
			return fmt.Errorf("replace original with rotated copy: %w", err)
		}
		if err := pgStore.RecordOriginalRewrite(ctx, mediaAssetID, rotated.ETag, rotated.Size); err != nil {
			return fmt.Errorf("update original size: %w", err)
		}
		log.Printf("Rotation override %d° applied to original of media %s (%d bytes)", //nolint:gosec // G706: an int, a parsed uuid and an int64; nothing can forge a log line
			payload.RotateDegrees, mediaAssetID, rotated.Size)
		inputPath = rotatedPath
	}

	// 3. Probe, and refuse what is outside the limits before any encode
	// (finding 3). The refusal is terminal and says why: a failed
	// "limits" transcoding job carries the reason, and GET
	// /v1/media/:id/status lists it.
	meta, err := processing.ProbeVideo(ctx, inputPath)
	if err != nil {
		return permanentUnlessCancelled(parent, fmt.Errorf("probe video: %w", err))
	}
	if lerr := processing.CheckVideoLimits(meta, env.cfg.limits); lerr != nil {
		recordRefusal(ctx, pgStore, mediaAssetID, lerr)
		transcodeTotal.WithLabelValues("refused").Inc()
		return permanentTranscode(lerr)
	}

	// From here the job has its real budget: every ffmpeg call is bounded
	// by FFmpegCallTimeout of the duration, and the whole job by
	// TranscodeJobBudget, past which the heartbeat stops renewing the lease.
	ctx = processing.WithToolTimeout(
		setDeadline(processing.TranscodeJobBudget(meta.DurationFloat)),
		processing.FFmpegCallTimeout(meta.DurationFloat))

	// Determine if this is a reel (short-form video): ≤ 10 minutes and
	// portrait/square. Landscape is long-form at any length.
	isReel := processing.IsShortFormEncode(meta)

	type jobEntry struct {
		name  string
		jobID uuid.UUID
	}
	var jobEntries []jobEntry
	qualityHeights := []struct {
		name   string
		height int
	}{
		{"thumb_150", 0}, {"360p", 360}, {"480p", 480}, {"720p", 720}, {"1080p", 1080}, {"4k", 2160},
	}
	for _, q := range qualityHeights {
		if q.height > 0 && meta.Height < q.height {
			continue
		}
		// Skip 1080p and 4K for reels — cap at 720p
		if isReel && (q.name == "1080p" || q.name == "4k") {
			continue
		}
		jobID := uuid.New()
		job := &postgres.TranscodingJob{
			ID:            jobID,
			MediaAssetID:  mediaAssetID,
			TargetQuality: q.name,
			Status:        "queued",
		}
		if err := pgStore.CreateTranscodingJob(ctx, job); err != nil {
			return fmt.Errorf("create transcode job %s: %w", q.name, err)
		}
		if err := pgStore.UpdateTranscodingJob(ctx, jobID, "processing", nil, nil, nil); err != nil {
			return fmt.Errorf("mark transcode job %s processing: %w", q.name, err)
		}
		jobEntries = append(jobEntries, jobEntry{name: q.name, jobID: jobID})
	}

	// 4. Run FFmpeg transcode pipeline (reel-optimized or standard)
	var outputs []processing.TranscodeOutput
	if isReel {
		outputs, _, err = processing.TranscodeReel(ctx, inputPath, tmpDir)
	} else {
		outputs, _, err = processing.TranscodeVideo(ctx, inputPath, tmpDir)
	}
	if err != nil {
		// Mark all jobs as failed
		errMsg := err.Error()
		for _, je := range jobEntries {
			_ = pgStore.UpdateTranscodingJob(ctx, je.jobID, "failed", nil, nil, &errMsg)
		}
		return permanentUnlessCancelled(parent, fmt.Errorf("transcode: %w", err))
	}

	// 4b. Storyboard (MTube seek preview, 2026-09-27): a sprite sheet plus
	// a VTT cue file for videos longer than a minute, recorded as the
	// storyboard_jpg / storyboard_vtt variants. A failure here is logged
	// and skipped rather than failing the transcode: the video plays
	// without a hover preview, which beats never becoming ready.
	if plan, ok := processing.PlanStoryboard(meta.DurationFloat); ok {
		if sb, sbErr := processing.GenerateStoryboard(ctx, inputPath, tmpDir, plan); sbErr != nil {
			if ctx.Err() != nil {
				return sbErr
			}
			log.Printf("Warning: storyboard for media %s not generated: %v", mediaAssetID, sbErr)
		} else {
			outputs = append(outputs, sb...)
		}
	}

	// 5. Upload variants and update job records. Keys derive from the
	// ROW's storage key (finding 13).
	baseKey := strings.TrimSuffix(subject.StorageKey, "/original")
	var variants []postgres.MediaVariant

	for _, out := range outputs {
		objectName := out.ObjectName
		if objectName == "" {
			objectName = out.Name
		}
		objectKey := fmt.Sprintf("%s/%s", baseKey, objectName)
		// Streamed from disk: a 1080p/4K rendition of an hours-long video is
		// GiBs and must not be read into memory first.
		sz, err := uploadFileBounded(ctx, blobStore, objectKey, out.FilePath, out.Mime)
		if err != nil {
			return fmt.Errorf("upload transcode variant %s: %w", out.Name, err)
		}

		w := out.Width
		h := out.Height
		variants = append(variants, postgres.MediaVariant{
			MediaAssetID: mediaAssetID,
			Name:         out.Name,
			Width:        &w,
			Height:       &h,
			SizeBytes:    &sz,
			Mime:         out.Mime,
			ObjectKey:    objectKey,
		})

		// Update matching job record to completed
		for _, je := range jobEntries {
			if je.name == out.Name {
				if err := pgStore.UpdateTranscodingJob(ctx, je.jobID, "completed", &objectKey, &sz, nil); err != nil {
					return fmt.Errorf("mark transcode job %s complete: %w", out.Name, err)
				}
				break
			}
		}
	}

	// 6. Insert variants into DB
	if len(variants) == 0 {
		return permanentTranscode(fmt.Errorf("transcode produced no uploadable variants"))
	}
	if err := pgStore.InsertVariants(ctx, variants); err != nil {
		return fmt.Errorf("insert variants: %w", err)
	}
	// A re-run measured at the display size may produce fewer renditions
	// than the first run did; rows for the ones it no longer produces would
	// keep serving the old files. No-op on a first run.
	produced := make([]string, 0, len(variants))
	for _, v := range variants {
		produced = append(produced, v.Name)
	}
	if stale, err := pgStore.DeleteVariantsNotIn(ctx, mediaAssetID, produced); err != nil {
		return fmt.Errorf("prune stale variants: %w", err)
	} else {
		for _, key := range stale {
			if err := deleteObjectBounded(ctx, blobStore, key); err != nil {
				log.Printf("Warning: stale variant object %s for %s not deleted: %v", key, mediaAssetID, err) //nolint:gosec // G706: object key read back from our own variant rows; media id is a parsed uuid
			}
		}
		if len(stale) > 0 {
			log.Printf("Pruned %d stale variant(s) for media %s", len(stale), mediaAssetID) //nolint:gosec // G706: a count and a parsed uuid
		}
	}

	// 7. Generate blurhash from video thumbnail
	var videoBlurHash string
	for _, out := range outputs {
		if out.Name == "thumb_150" {
			thumbData, readErr := os.ReadFile(out.FilePath)
			if readErr == nil {
				img, _, decErr := image.Decode(bytes.NewReader(thumbData))
				if decErr == nil {
					hash, hashErr := blurhash.Encode(4, 3, img)
					if hashErr == nil {
						videoBlurHash = hash
					}
				}
			}
			break
		}
	}

	// 8. Update media metadata (dimensions, duration, blurhash)
	durationSeconds := meta.DurationSeconds
	durationMs := meta.DurationMs
	if err := pgStore.UpdateMediaMeta(ctx, mediaAssetID, meta.Width, meta.Height, videoBlurHash, &durationSeconds, &durationMs); err != nil {
		return fmt.Errorf("update meta: %w", err)
	}

	// 8b. Update orientation flag
	isVertical := meta.Height > meta.Width
	if err := pgStore.UpdateMediaOrientation(ctx, mediaAssetID, isVertical); err != nil {
		return fmt.Errorf("update media orientation for %s: %w", mediaAssetID, err)
	}

	// 9. Populate URL fields
	originalURL := subject.StorageKey
	cdnURL := fmt.Sprintf("/%s/%s", "media", subject.StorageKey)
	var thumbnailURL *string
	for _, v := range variants {
		if v.Name == "thumb_150" {
			key := v.ObjectKey
			thumbnailURL = &key
			break
		}
	}
	if err := pgStore.UpdateMediaURLs(ctx, mediaAssetID, &originalURL, &cdnURL, thumbnailURL); err != nil {
		return fmt.Errorf("update media URLs: %w", err)
	}

	// 10. Generate HLS adaptive bitrate variants
	hlsDir, err := os.MkdirTemp(env.cfg.scratchDir, "hls-"+mediaAssetID.String())
	if err != nil {
		return fmt.Errorf("create HLS temp dir: %w", err)
	}
	defer os.RemoveAll(hlsDir)
	// The same reel/source facts that sized the MP4 renditions size the HLS
	// ladder, so a phone reel is not re-encoded at 1080p after the MP4 pass
	// deliberately skipped it. Each rung is repackaged from the MP4
	// rendition of its height (still in tmpDir) instead of being encoded a
	// second time; only a rung without one is encoded from the original.
	masterPath, variantFiles, hlsErr := processing.GenerateHLSVariantsFor(
		ctx, inputPath, hlsDir, processing.HLSPlan{
			Reel:         isReel,
			SourceHeight: meta.Height,
			Renditions:   processing.MP4Renditions(outputs),
		},
	)
	if hlsErr != nil {
		return permanentUnlessCancelled(parent, fmt.Errorf("generate HLS variants: %w", hlsErr))
	}
	masterKey := baseKey + "/hls/master.m3u8"
	masterData, err := os.ReadFile(masterPath)
	if err != nil {
		return fmt.Errorf("read HLS master: %w", err)
	}
	if err := uploadBytesBounded(ctx, blobStore, masterKey, masterData, "application/x-mpegURL"); err != nil {
		return fmt.Errorf("upload HLS master: %w", err)
	}
	hlsPrefix := baseKey + "/hls/"
	uploadedHLS := map[string]bool{masterKey: true}
	for _, f := range variantFiles {
		rel := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(f, hlsDir), "/"), "\\")
		key := hlsPrefix + rel
		uploadedHLS[key] = true
		contentType := "video/MP2T"
		if strings.HasSuffix(f, ".m3u8") {
			contentType = "application/x-mpegURL"
		}
		fData, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read HLS file %s: %w", f, err)
		}
		if err := uploadBytesBounded(ctx, blobStore, key, fData, contentType); err != nil {
			return fmt.Errorf("upload HLS file %s: %w", key, err)
		}
	}
	// A re-run with a shorter ladder leaves the old rung's playlist and
	// segments behind; nothing references them once the new master is up,
	// so drop them. Listing an empty prefix on a first run costs one call.
	listCtx, cancel := context.WithTimeout(ctx, s3MetadataCall)
	existing, err := blobStore.ListObjectKeys(listCtx, hlsPrefix)
	cancel()
	if err != nil {
		log.Printf("Warning: could not list HLS prefix %s for cleanup: %v", hlsPrefix, err)
	} else {
		for _, key := range existing {
			if uploadedHLS[key] {
				continue
			}
			if err := deleteObjectBounded(ctx, blobStore, key); err != nil {
				log.Printf("Warning: stale HLS object %s not deleted: %v", key, err) //nolint:gosec // G706: object key listed from our own HLS prefix in the media bucket
			}
		}
	}
	if err := pgStore.UpdateHLSMasterKey(ctx, mediaAssetID, masterKey); err != nil {
		return fmt.Errorf("store HLS master key: %w", err)
	}
	log.Printf("HLS generation completed for media %s", mediaAssetID)

	// 10b. Content-safety scan — frame-sample the original and run each
	// frame through the scanner. A single unsafe frame rejects the video.
	// post-service gates the post's visibility on the verdict stored here.
	// Approval has no default. Only a completed provider verdict can set
	// "passed"; extraction/provider/store uncertainty remains retryable and
	// never produces ready-and-publishable media.
	//
	// The sample is proportional to the duration (finding 10): one frame
	// every few seconds, capped, each downscaled under the provider's
	// image limit (finding 2).
	moderationStatus := "manual_review"
	frameCount := processing.ScanFrameCount(meta.DurationFloat)
	if frames, frErr := processing.ExtractFramesAt(ctx, inputPath, tmpDir, meta.DurationFloat, frameCount); frErr != nil {
		return permanentUnlessCancelled(parent, fmt.Errorf("extract frames for safety scan: %w", frErr))
	} else if res, scanErr := processing.ScanVideoFrames(ctx, scanner, frames); scanErr != nil {
		return classifyScanError(scanErr)
	} else if !res.IsSafe {
		moderationStatus = "rejected"
		log.Printf("Content scan REJECTED media %s: %s", mediaAssetID, res.Reason)
	} else {
		if err := recordVideoScanLabels(ctx, pgStore, mediaAssetID, scanner, res); err != nil {
			return err
		}
		moderationStatus = "passed"
	}
	if moderationResult == nil {
		return fmt.Errorf("moderation result destination is nil")
	}
	*moderationResult = moderationStatus

	return nil
}

// checkSniffedContainer refuses (permanently) an original whose bytes are
// not the container its declared MIME names, or not one ffmpeg may open.
func checkSniffedContainer(declaredMIME, sniffed string) error {
	if want := processing.DeclaredVideoContainer(declaredMIME); want == "" || sniffed != want {
		return permanentTranscode(fmt.Errorf("%w: declared %q, bytes are %q",
			processing.ErrInputFormatRefused, declaredMIME, sniffed))
	}
	return nil
}

// classifyScanError: the provider refusing the frame itself (too large,
// unreadable, under the minimum size) is permanent, since the same frame is
// refused on every retry (finding 2); anything else (throttling, outage) is
// a transient "no verdict yet".
func classifyScanError(err error) error {
	if errors.Is(err, processing.ErrScanInputRejected) {
		return permanentTranscode(fmt.Errorf("scan video frames: %w", err))
	}
	return fmt.Errorf("scan video frames: %w", err)
}

// recordRefusal writes the limit a video broke as a failed "limits"
// transcoding job, so the status route can say why the upload failed.
// Best-effort: the terminal failure is recorded either way.
func recordRefusal(ctx context.Context, store *postgres.MediaAssetStore, mediaID uuid.UUID, refusal error) {
	jobID := uuid.New()
	if err := store.CreateTranscodingJob(ctx, &postgres.TranscodingJob{
		ID: jobID, MediaAssetID: mediaID, TargetQuality: "limits", Status: "queued",
	}); err != nil {
		log.Printf("Warning: refusal of media %s not recorded: %v", mediaID, err)
		return
	}
	msg := refusal.Error()
	if err := store.UpdateTranscodingJob(ctx, jobID, "failed", nil, nil, &msg); err != nil {
		log.Printf("Warning: refusal of media %s not recorded: %v", mediaID, err)
	}
}

// uploadFileBounded / uploadBytesBounded / deleteObjectBounded are the
// worker's blob writes with a per-call deadline (finding 14).
func uploadFileBounded(ctx context.Context, blobs *blob.Store, key, path, mime string) (int64, error) {
	var size int64
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	c, cancel := context.WithTimeout(ctx, s3Timeout(size))
	defer cancel()
	return blobs.UploadFile(c, key, path, mime)
}

func uploadBytesBounded(ctx context.Context, blobs *blob.Store, key string, data []byte, mime string) error {
	c, cancel := context.WithTimeout(ctx, s3Timeout(int64(len(data))))
	defer cancel()
	return blobs.UploadObject(c, key, data, mime)
}

func deleteObjectBounded(ctx context.Context, blobs *blob.Store, key string) error {
	c, cancel := context.WithTimeout(ctx, s3MetadataCall)
	defer cancel()
	return blobs.DeleteObject(c, key)
}

// selectScanner picks the content-safety scanner and refuses unsafe defaults
// in production.
//
// M4-P0-3 / Decision 001 A1.1 — provider failure is not approval, and neither
// is provider ABSENCE. Outside production the stub is permitted so the local
// dev loop works, and it says so loudly.
func selectScanner() (processing.Scanner, string, error) {
	production := isWorkerProduction()

	if backend := strings.ToLower(strings.TrimSpace(os.Getenv("MEDIA_SCANNER_BACKEND"))); backend == "rekognition" {
		region := os.Getenv("AWS_REGION")
		if region == "" {
			return nil, "", fmt.Errorf("MEDIA_SCANNER_BACKEND=rekognition requires AWS_REGION")
		}
		// Static credentials are refused outright: workload credentials come
		// from IRSA, and a manifest-supplied key is both a policy violation and
		// a credential that cannot be rotated.
		if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
			return nil, "", fmt.Errorf("static AWS credentials are present; the worker uses " +
				"IRSA only. Remove AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY")
		}
		if production && (os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") == "" || os.Getenv("AWS_ROLE_ARN") == "") {
			return nil, "", fmt.Errorf("production Rekognition requires AWS_WEB_IDENTITY_TOKEN_FILE and AWS_ROLE_ARN; node-role fallback is forbidden")
		}
		s, err := processing.NewRekognitionScanner(context.Background(), region,
			processing.RekognitionConfig{
				MinConfidence: rekognitionConfidence(),
			})
		if err != nil {
			return nil, "", fmt.Errorf("configure Rekognition scanner: %w", err)
		}
		return s, "rekognition (fail-closed to manual review)", nil
	}

	if production {
		return nil, "", fmt.Errorf("no real content scanner configured in production. " +
			"Set MEDIA_SCANNER_BACKEND=rekognition. Refusing to start: the stub scanner " +
			"approves every video, which is worse than not processing at all")
	}
	return &processing.StubScanner{}, "STUB — passes everything, non-production only", nil
}

func rekognitionConfidence() float32 {
	if v := os.Getenv("MEDIA_SCANNER_MIN_CONFIDENCE"); v != "" {
		var f float32
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil && f > 0 && f <= 100 {
			return f
		}
		log.Printf("MEDIA_SCANNER_MIN_CONFIDENCE=%q is not a percentage; using default", v)
	}
	return 80
}

func isWorkerProduction() bool {
	for _, key := range []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "production", "prod":
			return true
		}
	}
	return false
}
