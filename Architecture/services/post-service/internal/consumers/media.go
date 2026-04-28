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
	slog.Info("media transcode consumer: video_metadata updated",
		"media_id", mediaID, "post_id", vm.PostID,
		"hls", p.HLSMasterURL != "")
	return nil
}
