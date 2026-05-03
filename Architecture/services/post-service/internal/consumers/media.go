// Package consumers contains Kafka consumers post-service runs to react to
// events from sibling services. Distinct from `internal/engagement/consumers`
// which handles the post-service's own engagement-event fan-out.
package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// MediaTranscodeConsumer listens for MediaTranscodeCompleted events on the
// media-service topic and updates the matching post's video_metadata row so
// clients receive an HLS master URL (preferring adaptive bitrate over the
// raw MP4 fallback). Idempotent: GetVideoMetadataByMediaAsset returns the
// same row each time, and UpdateVideoMetadata is a row UPDATE.
type MediaTranscodeConsumer struct {
	store    *postgres.Store
	consumer *sharedkafka.Consumer
}

func NewMediaTranscodeConsumer(
	store *postgres.Store,
	brokers []string,
	rdb *redis.Client,
	m *metrics.KafkaConsumerMetrics,
) *MediaTranscodeConsumer {
	c := &MediaTranscodeConsumer{store: store}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:  brokers,
			GroupID:  "post-service-media-transcode",
			Topic:    "media.events",
			DLQTopic: "media.events.dlq",
		},
		rdb, m, c.handle,
	)
	return c
}

func (c *MediaTranscodeConsumer) Start(ctx context.Context) {
	c.consumer.Start(ctx)
}

func (c *MediaTranscodeConsumer) Close() error {
	return c.consumer.Close()
}

func (c *MediaTranscodeConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.MediaTranscodeCompleted {
		return nil
	}
	var p events.MediaTranscodeCompletedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		// Bad payload — log + drop. Returning err would loop until DLQ.
		slog.Warn("media transcode consumer: bad payload", "error", err)
		return nil
	}
	// Only ready transcodes carry useful URLs. failed events are no-ops here
	// (post-service has no fallback to do — the upload UI reads media-service
	// status directly via my_uploads polling).
	if p.ProcessingStatus != "ready" {
		return nil
	}
	if p.HLSMasterURL == "" && p.MP4URL == "" {
		// Nothing to wire through; skip rather than overwriting any existing
		// playback_url with empty strings.
		return nil
	}

	mediaID, err := uuid.Parse(p.MediaAssetID)
	if err != nil {
		return nil
	}
	vm, err := c.store.GetVideoMetadataByMediaAsset(ctx, mediaID)
	if err != nil {
		// Common path on platforms without video_metadata rows yet (e.g. an
		// image-only post pulled this through). Not an error.
		slog.Debug("media transcode consumer: no video_metadata for media",
			"media_id", mediaID, "error", err)
		return nil
	}
	if vm == nil {
		return nil
	}

	// Prefer HLS for the playback URL so adaptive bitrate kicks in. Keep the
	// MP4 URL on storage_video_url as the fallback for clients that ignore
	// playback_url. Thumbnail likewise gets refreshed.
	chosen := p.HLSMasterURL
	if chosen == "" {
		chosen = p.MP4URL
	}
	vm.PlaybackURL = &chosen
	if p.MP4URL != "" {
		mp4 := p.MP4URL
		vm.StorageVideoURL = &mp4
	}
	if p.ThumbnailURL != "" {
		thumb := p.ThumbnailURL
		vm.ThumbnailURL = &thumb
	}
	if vm.UploadStatus != "ready" {
		vm.UploadStatus = "ready"
	}
	if err := c.store.UpdateVideoMetadata(ctx, vm); err != nil {
		return fmt.Errorf("update video_metadata for media %s: %w", mediaID, err)
	}

	// Reclassify the post's content_type now that we know duration +
	// dimensions. CreatePost falls back to "long_video" when the video
	// hasn't been transcoded yet, so a vertical reel ≤180s comes in as
	// long_video and stays there until this consumer flips it back to
	// "flick". Without this, the reel never appears in /v1/feed/reels.
	if duration, w, h, ok := lookupMediaDims(ctx, c.store, mediaID); ok {
		category := classifyForReclassification(duration, w, h)
		if err := c.store.UpdatePostContentType(ctx, vm.PostID, category); err != nil {
			slog.Warn("media transcode consumer: reclassify failed",
				"post_id", vm.PostID, "category", category, "error", err)
		} else {
			slog.Info("media transcode consumer: post reclassified",
				"post_id", vm.PostID, "category", category,
				"duration_s", duration, "w", w, "h", h)
		}
	}

	slog.Info("media transcode consumer: video_metadata updated",
		"media_id", mediaID, "post_id", vm.PostID,
		"hls", p.HLSMasterURL != "")
	return nil
}

// classifyForReclassification mirrors service.ClassifyVideo without
// the cyclic-import that would happen if we reached into internal/service
// from internal/consumers. The 180s + portrait/square rule is the spec —
// keep this in sync if it ever changes there.
const flickMaxDurationSeconds = 180

func classifyForReclassification(durationSeconds int, width, height int) string {
	orientation := "landscape"
	if width > 0 && height > 0 {
		if height > width {
			orientation = "portrait"
		} else if height == width {
			orientation = "square"
		}
	}
	if durationSeconds > 0 && durationSeconds <= flickMaxDurationSeconds &&
		(orientation == "portrait" || orientation == "square") {
		return "flick"
	}
	return "long_video"
}

// lookupMediaDims fetches the duration + dimensions written by the
// transcode pipeline. Returns ok=false when the media row is missing
// or the columns are still NULL.
func lookupMediaDims(ctx context.Context, store *postgres.Store, mediaID uuid.UUID) (duration, width, height int, ok bool) {
	d := store.ResolveMediaDuration(ctx, mediaID)
	w, h, err := store.ResolveMediaDimensions(ctx, mediaID)
	if err != nil || d <= 0 || w <= 0 || h <= 0 {
		return 0, 0, 0, false
	}
	return d, w, h, true
}
