// Package consumers contains Kafka consumers post-service runs to react to
// events from sibling services. Distinct from `internal/engagement/consumers`
// which handles the post-service's own engagement-event fan-out.
package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/postclassify"
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
	producer *postEvents.Producer // optional; nil means no fan-out
	consumer *sharedkafka.Consumer
	rdb      *redis.Client // for busting the post-body cache on a gate flip
}

func NewMediaTranscodeConsumer(
	store *postgres.Store,
	brokers []string,
	rdb *redis.Client,
	m *metrics.KafkaConsumerMetrics,
) *MediaTranscodeConsumer {
	c := &MediaTranscodeConsumer{store: store, rdb: rdb}
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

// WithProducer wires the post-service event producer so a successful
// reclassification fans a PostContentTypeChanged event out to
// feed-service. nil-safe: the consumer still reclassifies the
// posts.content_type column locally; the timeline rows just stay
// stale until manually fixed or until the next event flushes them.
func (c *MediaTranscodeConsumer) WithProducer(p *postEvents.Producer) *MediaTranscodeConsumer {
	c.producer = p
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
	mediaID, err := uuid.Parse(p.MediaAssetID)
	if err != nil {
		return nil
	}

	// reels/posttube items 2+3 — finalize the video publish gate: flip a
	// still-'pending' post to approved/rejected now that transcode and the
	// content scan have produced a verdict.
	c.finalizeReviewGate(ctx, mediaID, &p)

	// Only ready transcodes carry useful URLs to wire through; a failed
	// transcode is otherwise a no-op here (the gate above handled it) and
	// the upload UI reads media-service status directly.
	if p.ProcessingStatus != "ready" {
		return nil
	}
	if p.HLSMasterURL == "" && p.MP4URL == "" {
		// Nothing to wire through; skip rather than overwriting any existing
		// playback_url with empty strings.
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
	// dimensions. CreatePost falls back to "long_video" when the
	// video hasn't been transcoded yet, so a vertical reel ≤180s
	// comes in as long_video and stays there until this consumer
	// flips it back to "flick". Without this, the reel never
	// appears in /v1/feed/reels.
	//
	// On a successful flip, fan a PostContentTypeChanged event out
	// so feed-service can rewrite the matching content_type column
	// on its Scylla timeline rows — those carry their own copy and
	// would otherwise stay stale.
	if duration, w, h, ok := lookupMediaDims(ctx, c.store, mediaID); ok {
		newType := postclassify.Classify(duration, w, h)
		authorID, oldType, err := c.store.GetPostAuthorAndContentType(ctx, vm.PostID)
		if err != nil {
			slog.Warn("media transcode consumer: read author+content_type failed",
				"post_id", vm.PostID, "error", err)
		} else if oldType != newType {
			if err := c.store.UpdatePostContentType(ctx, vm.PostID, newType); err != nil {
				slog.Warn("media transcode consumer: reclassify failed",
					"post_id", vm.PostID, "new_type", newType, "error", err)
			} else {
				slog.Info("media transcode consumer: post reclassified",
					"post_id", vm.PostID, "old_type", oldType, "new_type", newType,
					"duration_s", duration, "w", w, "h", h)
				if c.producer != nil {
					if err := c.producer.PublishPostContentTypeChanged(
						ctx, vm.PostID, authorID, oldType, newType,
					); err != nil {
						slog.Warn("media transcode consumer: publish content_type_changed failed",
							"post_id", vm.PostID, "error", err)
					}
				}
			}
		}
	}

	slog.Info("media transcode consumer: video_metadata updated",
		"media_id", mediaID, "post_id", vm.PostID,
		"hls", p.HLSMasterURL != "")
	return nil
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

// finalizeReviewGate flips a still-'pending' video post to its terminal
// review_status once transcode finishes — 'rejected' on a failed transcode
// or a rejected content scan, 'approved' on a clean ready transcode. A post
// that already has a terminal status (finalized at create time, or a manual
// moderator decision) is left untouched.
func (c *MediaTranscodeConsumer) finalizeReviewGate(ctx context.Context, mediaID uuid.UUID, p *events.MediaTranscodeCompletedPayload) {
	var decision string
	switch {
	case p.ProcessingStatus == "failed":
		decision = "rejected"
	case p.ProcessingStatus == "ready" && p.ModerationStatus == "rejected":
		decision = "rejected"
	case p.ProcessingStatus == "ready":
		decision = "approved"
	default:
		return // intermediate status — wait for a terminal event
	}

	vm, err := c.store.GetVideoMetadataByMediaAsset(ctx, mediaID)
	if err != nil || vm == nil {
		return // no post mapped to this media yet — nothing to gate
	}

	flipped, err := c.store.FlipReviewStatusFromPending(ctx, vm.PostID, decision)
	if err != nil {
		slog.Warn("media transcode consumer: review-gate flip failed",
			"post_id", vm.PostID, "error", err)
		return
	}
	if !flipped {
		return // post wasn't pending — leave its existing status
	}

	// Audit row for the verdict + bust the cached post body so the new
	// review_status is visible immediately.
	confidence := 1.0
	if err := c.store.InsertModerationReview(ctx, &postgres.ModerationReview{
		ReelID:       vm.PostID,
		ReviewerType: "auto",
		Decision:     decision,
		Confidence:   &confidence,
	}); err != nil {
		slog.Warn("media transcode consumer: moderation review insert failed",
			"post_id", vm.PostID, "error", err)
	}
	if c.rdb != nil {
		_ = c.rdb.Del(ctx, "post:body:"+vm.PostID.String()).Err()
	}
	slog.Info("media transcode consumer: publish gate finalized",
		"post_id", vm.PostID, "decision", decision)
}
