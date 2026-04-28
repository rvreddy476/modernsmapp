package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/media-service/database"
	mediaEvents "github.com/atpost/media-service/internal/events"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/transport"
	"github.com/buckket/go-blurhash"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

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

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		log.Fatalf("Failed to bootstrap media schema: %v\n", err)
	}
	log.Println("Media schema ready")

	// Blob Store
	blobStore, err := blob.New(minioEndpoint, minioAccessKey, minioSecretKey, minioBucket, minioUseSSL)
	if err != nil {
		log.Fatalf("Unable to connect to MinIO: %v\n", err)
	}
	log.Println("Connected to MinIO")

	pgStore := postgres.New(dbPool)
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		log.Fatalf("Failed to configure Kafka dialer: %v\n", err)
	}
	producer := mediaEvents.NewProducerWithDialer(brokers, "media.events", kafkaDialer)

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

	log.Println("Media transcode worker started, waiting for messages...")

	for {
		m, err := reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("Consumer error: %v\n", err)
			break
		}

		if err := processMessage(ctx, m, pgStore, blobStore, producer); err != nil {
			log.Printf("Failed to process message: %v\n", err)
		}
	}

	_ = reader.Close()
	_ = producer.Close()
	log.Println("Worker stopped")
}

func processMessage(ctx context.Context, m kafka.Message, pgStore *postgres.MediaAssetStore, blobStore *blob.Store, producer *mediaEvents.Producer) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}

	if envelope.EventType != events.MediaTranscodeRequested {
		return nil // skip non-transcode events
	}

	var payload events.MediaTranscodeRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	mediaAssetID, err := uuid.Parse(payload.MediaAssetID)
	if err != nil {
		return fmt.Errorf("parse media_asset_id: %w", err)
	}

	log.Printf("Processing video transcode for media %s", payload.MediaAssetID)

	if err := transcodeVideo(ctx, mediaAssetID, payload, pgStore, blobStore); err != nil {
		log.Printf("Transcode failed for %s: %v", payload.MediaAssetID, err)
		_ = pgStore.UpdateStatus(ctx, mediaAssetID, "failed")
		_ = producer.PublishTranscodeCompleted(ctx, mediaAssetID, "failed")
		return nil // Don't retry — mark as failed
	}

	// Read back the URLs we wrote during transcode so the success event can
	// carry them. Downstream consumers (post-service.video_metadata) need the
	// HLS master URL to point clients at adaptive bitrate playback rather
	// than the raw MP4 fallback.
	asset, fetchErr := pgStore.GetMedia(ctx, mediaAssetID)
	hlsURL, mp4URL, thumbURL := "", "", ""
	if fetchErr == nil && asset != nil {
		if asset.HLSMasterKey != "" {
			hlsURL = "/" + strings.TrimPrefix(asset.HLSMasterKey, "/")
		}
		if asset.CdnURL != nil && *asset.CdnURL != "" {
			mp4URL = *asset.CdnURL
		}
		if asset.ThumbnailURL != nil && *asset.ThumbnailURL != "" {
			thumbURL = *asset.ThumbnailURL
		}
	}
	_ = producer.PublishTranscodeCompletedWithURLs(ctx, mediaAssetID, "ready", hlsURL, mp4URL, thumbURL)
	log.Printf("Transcode completed for media %s (hls=%t)", payload.MediaAssetID, hlsURL != "")
	return nil
}

func transcodeVideo(ctx context.Context, mediaAssetID uuid.UUID, payload events.MediaTranscodeRequestedPayload, pgStore *postgres.MediaAssetStore, blobStore *blob.Store) error {
	// 1. Download original video from MinIO
	videoData, err := blobStore.DownloadObject(ctx, payload.StorageKey)
	if err != nil {
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
		return fmt.Errorf("probe video: %w", err)
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
			log.Printf("Warning: failed to create job record for %s: %v", q.name, err)
			continue
		}
		_ = pgStore.UpdateTranscodingJob(ctx, jobID, "processing", nil, nil, nil)
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
		return fmt.Errorf("transcode: %w", err)
	}

	// 5. Upload variants to MinIO and update job records
	baseKey := strings.TrimSuffix(payload.StorageKey, "/original")
	var variants []postgres.MediaVariant

	for _, out := range outputs {
		data, err := os.ReadFile(out.FilePath)
		if err != nil {
			log.Printf("Warning: failed to read output %s: %v", out.Name, err)
			continue
		}

		objectKey := fmt.Sprintf("%s/%s", baseKey, out.Name)
		if err := blobStore.UploadObject(ctx, objectKey, data, out.Mime); err != nil {
			log.Printf("Warning: failed to upload variant %s: %v", out.Name, err)
			continue
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
				_ = pgStore.UpdateTranscodingJob(ctx, je.jobID, "completed", &objectKey, &sz, nil)
				break
			}
		}
	}

	// 6. Insert variants into DB
	if len(variants) > 0 {
		if err := pgStore.InsertVariants(ctx, variants); err != nil {
			return fmt.Errorf("insert variants: %w", err)
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
	if err := pgStore.UpdateMediaMeta(ctx, mediaAssetID, meta.Width, meta.Height, videoBlurHash, &durationSeconds); err != nil {
		return fmt.Errorf("update meta: %w", err)
	}

	// 8b. Update orientation flag
	isVertical := meta.Height > meta.Width
	if err := pgStore.UpdateMediaOrientation(ctx, mediaAssetID, isVertical); err != nil {
		log.Printf("Warning: failed to update media orientation for %s: %v", mediaAssetID, err)
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
	_ = pgStore.UpdateMediaURLs(ctx, mediaAssetID, &originalURL, &cdnURL, thumbnailURL)

	// 10. Generate HLS adaptive bitrate variants
	hlsDir, err := os.MkdirTemp("", "hls-"+payload.MediaAssetID)
	if err != nil {
		log.Printf("Warning: failed to create HLS temp dir for %s: %v", payload.MediaAssetID, err)
	} else {
		defer os.RemoveAll(hlsDir)
		masterPath, variantFiles, hlsErr := processing.GenerateHLSVariants(ctx, inputPath, hlsDir)
		if hlsErr != nil {
			log.Printf("Warning: HLS generation failed for %s (non-fatal): %v", payload.MediaAssetID, hlsErr)
		} else {
			// Upload master playlist
			masterKey := fmt.Sprintf("%s/hls/master.m3u8", strings.TrimSuffix(payload.StorageKey, "/original"))
			masterData, readErr := os.ReadFile(masterPath)
			if readErr == nil {
				if uploadErr := blobStore.UploadObject(ctx, masterKey, masterData, "application/x-mpegURL"); uploadErr != nil {
					log.Printf("Warning: failed to upload HLS master playlist for %s: %v", payload.MediaAssetID, uploadErr)
				} else {
					// Upload variant playlists and segments
					for _, f := range variantFiles {
						rel := strings.TrimPrefix(f, hlsDir)
						rel = strings.TrimPrefix(rel, "/")
						rel = strings.TrimPrefix(rel, "\\")
						key := fmt.Sprintf("%s/hls/%s", strings.TrimSuffix(payload.StorageKey, "/original"), rel)
						contentType := "video/MP2T"
						if strings.HasSuffix(f, ".m3u8") {
							contentType = "application/x-mpegURL"
						}
						fData, fErr := os.ReadFile(f)
						if fErr != nil {
							log.Printf("Warning: failed to read HLS file %s: %v", f, fErr)
							continue
						}
						if uploadErr := blobStore.UploadObject(ctx, key, fData, contentType); uploadErr != nil {
							log.Printf("Warning: failed to upload HLS file %s: %v", key, uploadErr)
						}
					}
					// Store master key in DB
					if dbErr := pgStore.UpdateHLSMasterKey(ctx, mediaAssetID, masterKey); dbErr != nil {
						log.Printf("Warning: failed to store HLS master key for %s: %v", payload.MediaAssetID, dbErr)
					}
					log.Printf("HLS generation completed for media %s", payload.MediaAssetID)
				}
			}
		}
	}

	// 11. Set status to ready
	if err := pgStore.UpdateStatus(ctx, mediaAssetID, "ready"); err != nil {
		return err
	}

	// 12. Activate any pending media slots referencing this asset
	if err := pgStore.ActivatePendingSlot(ctx, mediaAssetID); err != nil {
		log.Printf("Warning: failed to activate pending slots for %s: %v", mediaAssetID, err)
	}

	return nil
}
