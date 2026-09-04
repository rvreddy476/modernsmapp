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

		if !handleUntilDurable(ctx, m, pgStore, blobStore, scanner) {
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

	var payload events.MediaTranscodeRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentTranscode(fmt.Errorf("unmarshal payload: %w", err))
	}

	mediaAssetID, err := uuid.Parse(payload.MediaAssetID)
	if err != nil {
		return permanentTranscode(fmt.Errorf("parse media_asset_id: %w", err))
	}

	// Cheap replay check before expensive work. The guarantee is the inbox
	// primary key inside CompleteTranscode; this only avoids re-running ffmpeg
	// over an asset another replica already finished.
	return pgStore.WithTranscodeEventLock(ctx, envelope.EventID, func() error {
		return processTranscodeLocked(ctx, envelope, payload, mediaAssetID, pgStore, blobStore, scanner)
	})
}

func processTranscodeLocked(ctx context.Context, envelope events.EventEnvelope,
	payload events.MediaTranscodeRequestedPayload, mediaAssetID uuid.UUID,
	pgStore *postgres.MediaAssetStore, blobStore *blob.Store, scanner processing.Scanner) error {
	if done, err := pgStore.AlreadyApplied(ctx, envelope.EventID); err != nil {
		return fmt.Errorf("inbox lookup: %w", err)
	} else if done {
		log.Printf("Transcode for media %s already applied; skipping", payload.MediaAssetID)
		return nil
	}

	log.Printf("Processing video transcode for media %s", payload.MediaAssetID)

	transcodeStart := time.Now()
	moderationResult := ""
	transcodeErr := transcodeVideo(ctx, mediaAssetID, payload, pgStore, blobStore, scanner, &moderationResult)
	transcodeDuration.Observe(time.Since(transcodeStart).Seconds())

	if transcodeErr != nil {
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
		if err := pgStore.CompleteTranscode(ctx, envelope.EventID, mediaAssetID,
			"failed", "", "manual_review", postgres.TranscodeCompletion{
				ProcessingStatus: "failed", ModerationStatus: "manual_review",
			}); err != nil {
			if errors.Is(err, postgres.ErrTranscodeAlreadyApplied) {
				return nil
			}
			return fmt.Errorf("record terminal failure for %s: %w", payload.MediaAssetID, err)
		}
		return nil
	}
	transcodeTotal.WithLabelValues("success").Inc()

	// Read back what transcode wrote so the completion event can carry it.
	asset, fetchErr := pgStore.GetMedia(ctx, mediaAssetID)
	if fetchErr != nil {
		// This used to be ignored, and the moderation status then defaulted to
		// "passed" — so a failed read-back PUBLISHED unreviewed media. The read
		// is now required.
		return fmt.Errorf("read back media %s after transcode: %w", payload.MediaAssetID, fetchErr)
	}
	if asset == nil {
		return fmt.Errorf("media %s vanished during transcode", payload.MediaAssetID)
	}

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

	if err := pgStore.CompleteTranscode(ctx, envelope.EventID, mediaAssetID,
		"ready", asset.HLSMasterKey, modStatus, postgres.TranscodeCompletion{
			ProcessingStatus: "ready",
			HLSMasterURL:     hlsURL,
			MP4URL:           mp4URL,
			ThumbnailURL:     thumbURL,
			ModerationStatus: modStatus,
		}); err != nil {
		if errors.Is(err, postgres.ErrTranscodeAlreadyApplied) {
			log.Printf("Transcode for media %s completed by another replica", payload.MediaAssetID)
			return nil
		}
		return fmt.Errorf("record transcode completion for %s: %w", payload.MediaAssetID, err)
	}
	if modStatus == "passed" {
		if err := pgStore.ActivatePendingSlot(ctx, mediaAssetID); err != nil {
			log.Printf("Warning: failed to activate pending media slots for %s: %v", payload.MediaAssetID, err)
		}
	}
	log.Printf("Transcode completed for media %s (hls=%t, moderation=%s)",
		payload.MediaAssetID, hlsURL != "", modStatus)
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
// cost of destroying a user's upload.
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

// handleUntilDurable retries a message until its effect is durably recorded.
// Reports whether the message may now be committed; false means the context was
// cancelled first and the offset must be left where it is.
func handleUntilDurable(
	ctx context.Context,
	m kafka.Message,
	pgStore *postgres.MediaAssetStore,
	blobStore *blob.Store,
	scanner processing.Scanner,
) bool {
	backoff := 2 * time.Second
	const maxBackoff = 2 * time.Minute
	for attempt := 1; ; attempt++ {
		err := processMessage(ctx, m, pgStore, blobStore, scanner)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}

		// Poison input: committing past a message that can never succeed is the
		// lesser harm. The alternative blocks every transcode queued behind it
		// on this partition forever. Logged at a level an operator can alert
		// on rather than dropped silently.
		if isPermanentTranscodeFailure(err) {
			if qerr := pgStore.QuarantineTranscode(ctx, m, err); qerr != nil {
				log.Printf("Failed to quarantine poison transcode; offset NOT committed: %v", qerr)
				err = fmt.Errorf("durable poison quarantine: %w", qerr)
			} else {
				log.Printf("PERMANENTLY UNDELIVERABLE transcode message partition=%d offset=%d: %v "+
					"(durably quarantined; committing so the partition is not stalled)", m.Partition, m.Offset, err)
				return true
			}
		}

		log.Printf("Transcode effect failed (attempt %d, partition=%d offset=%d), "+
			"offset NOT committed, retrying in %s: %v", attempt, m.Partition, m.Offset, backoff, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func transcodeVideo(ctx context.Context, mediaAssetID uuid.UUID, payload events.MediaTranscodeRequestedPayload, pgStore *postgres.MediaAssetStore, blobStore *blob.Store, scanner processing.Scanner, moderationResult *string) error {
	// 1. Download original video from MinIO
	videoData, err := blobStore.DownloadObject(ctx, payload.StorageKey)
	if err != nil {
		if errors.Is(err, blob.ErrObjectNotFound) {
			return permanentTranscode(fmt.Errorf("download original: %w", err))
		}
		return fmt.Errorf("download original: %w", err)
	}

	// 2. Write to temp file
	tmpDir, err := os.MkdirTemp("", "transcode-"+payload.MediaAssetID)
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := tmpDir + "/original"
	if err := os.WriteFile(inputPath, videoData, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	// 3. Create transcoding job records before running FFmpeg
	type jobEntry struct {
		name  string
		jobID uuid.UUID
	}
	var jobEntries []jobEntry

	// Probe video to determine source resolution for job creation
	meta, err := processing.ProbeVideo(ctx, inputPath)
	if err != nil {
		return permanentUnlessCancelled(ctx, fmt.Errorf("probe video: %w", err))
	}

	// Determine if this is a reel (short-form video)
	isReel := meta.DurationSeconds > 0 && meta.DurationSeconds <= processing.ReelMaxDurationSeconds

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
		return permanentUnlessCancelled(ctx, fmt.Errorf("transcode: %w", err))
	}

	// 5. Upload variants to MinIO and update job records
	baseKey := strings.TrimSuffix(payload.StorageKey, "/original")
	var variants []postgres.MediaVariant

	for _, out := range outputs {
		data, err := os.ReadFile(out.FilePath)
		if err != nil {
			return fmt.Errorf("read transcode output %s: %w", out.Name, err)
		}

		objectKey := fmt.Sprintf("%s/%s", baseKey, out.Name)
		if err := blobStore.UploadObject(ctx, objectKey, data, out.Mime); err != nil {
			return fmt.Errorf("upload transcode variant %s: %w", out.Name, err)
		}

		w := out.Width
		h := out.Height
		sz := int64(len(data))
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
	originalURL := payload.StorageKey
	cdnURL := fmt.Sprintf("/%s/%s", "media", payload.StorageKey)
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
	hlsDir, err := os.MkdirTemp("", "hls-"+payload.MediaAssetID)
	if err != nil {
		return fmt.Errorf("create HLS temp dir: %w", err)
	}
	defer os.RemoveAll(hlsDir)
	// The same reel/source facts that sized the MP4 renditions size the HLS
	// ladder, so a phone reel is not re-encoded at 1080p after the MP4 pass
	// deliberately skipped it.
	masterPath, variantFiles, hlsErr := processing.GenerateHLSVariantsFor(
		ctx, inputPath, hlsDir, processing.HLSPlan{Reel: isReel, SourceHeight: meta.Height},
	)
	if hlsErr != nil {
		return permanentUnlessCancelled(ctx, fmt.Errorf("generate HLS variants: %w", hlsErr))
	}
	masterKey := fmt.Sprintf("%s/hls/master.m3u8", strings.TrimSuffix(payload.StorageKey, "/original"))
	masterData, err := os.ReadFile(masterPath)
	if err != nil {
		return fmt.Errorf("read HLS master: %w", err)
	}
	if err := blobStore.UploadObject(ctx, masterKey, masterData, "application/x-mpegURL"); err != nil {
		return fmt.Errorf("upload HLS master: %w", err)
	}
	for _, f := range variantFiles {
		rel := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(f, hlsDir), "/"), "\\")
		key := fmt.Sprintf("%s/hls/%s", strings.TrimSuffix(payload.StorageKey, "/original"), rel)
		contentType := "video/MP2T"
		if strings.HasSuffix(f, ".m3u8") {
			contentType = "application/x-mpegURL"
		}
		fData, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read HLS file %s: %w", f, err)
		}
		if err := blobStore.UploadObject(ctx, key, fData, contentType); err != nil {
			return fmt.Errorf("upload HLS file %s: %w", key, err)
		}
	}
	if err := pgStore.UpdateHLSMasterKey(ctx, mediaAssetID, masterKey); err != nil {
		return fmt.Errorf("store HLS master key: %w", err)
	}
	log.Printf("HLS generation completed for media %s", payload.MediaAssetID)

	// 10b. Content-safety scan — frame-sample the original and run each
	// frame through the scanner. A single unsafe frame rejects the video.
	// post-service gates the post's visibility on the verdict stored here.
	// Approval has no default. Only a completed provider verdict can set
	// "passed"; extraction/provider/store uncertainty remains retryable and
	// never produces ready-and-publishable media.
	moderationStatus := "manual_review"
	if frames, frErr := processing.ExtractFrames(ctx, inputPath, tmpDir, 5); frErr != nil {
		return permanentUnlessCancelled(ctx, fmt.Errorf("extract frames for safety scan: %w", frErr))
	} else if res, scanErr := processing.ScanVideoFrames(ctx, scanner, frames); scanErr != nil {
		return fmt.Errorf("scan video frames: %w", scanErr)
	} else if !res.IsSafe {
		moderationStatus = "rejected"
		log.Printf("Content scan REJECTED media %s: %s", payload.MediaAssetID, res.Reason)
	} else {
		moderationStatus = "passed"
	}
	if moderationResult == nil {
		return fmt.Errorf("moderation result destination is nil")
	}
	*moderationResult = moderationStatus

	return nil
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
