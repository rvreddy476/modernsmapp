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
	"github.com/atpost/post-service/internal/service"
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
	// Module 1 fixes-v2 / Codex P0-2: media-service publishes a voice
	// safety verdict, but nothing consumed it — so a voice post held at
	// review_status='pending' had no path to ever become visible.
	if env.EventType == events.MediaVoiceSafetyResolved {
		return c.handleVoiceSafetyResolved(ctx, env)
	}
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

	// The measurement — duration, dimensions, orientation and the category
	// they imply — is recorded on video_metadata for analytics whatever the
	// post's content_type ends up being. Whether that measurement also
	// rewrites the post is decided below.
	duration, w, h, measured := lookupMediaDims(ctx, c.store, mediaID)
	measuredType := ""
	if measured {
		measuredType = postclassify.Classify(duration, w, h)
		_, orientation := service.ClassifyVideo(float64(duration), w, h)
		vm.DurationSeconds = float64(duration)
		vm.Width = &w
		vm.Height = &h
		vm.Orientation = orientation
		vm.ComputedCategory = measuredType
	}
	if err := c.store.UpdateVideoMetadata(ctx, vm); err != nil {
		return fmt.Errorf("update video_metadata for media %s: %w", mediaID, err)
	}

	// Reclassify the post's content_type now that duration + dimensions are
	// known — but only when nobody chose the kind. A reel is what the
	// author posted as a reel; a video is what the author posted as a video
	// (founder, 2026-09-04/05): an explicit flick or long_video is never
	// rewritten from the measurement, a landscape reel stays a reel and a
	// short vertical clip posted from Tube stays a long video.
	//
	// The row that does get rewritten is a plain "post" that carried a
	// video and defaulted to long_video while transcode was pending
	// (content_type_explicit = FALSE): a vertical ≤300s clip flips to
	// "flick" here, otherwise it would never appear in /v1/feed/reels.
	//
	// On a successful flip, fan a PostContentTypeChanged event out so
	// feed-service can rewrite the matching content_type column on its
	// Scylla timeline rows — those carry their own copy and would
	// otherwise stay stale.
	if measured {
		st, err := c.store.GetPostClassificationState(ctx, vm.PostID)
		if err != nil {
			slog.Warn("media transcode consumer: read post classification state failed",
				"post_id", vm.PostID, "error", err)
		} else if newType, keep := reclassifyDecision(st.ContentType, st.ContentTypeExplicit, measuredType); keep {
			if st.ContentType != measuredType {
				slog.Info("media transcode consumer: keeping author's kind",
					"post_id", vm.PostID, "content_type", st.ContentType, "measured_type", measuredType,
					"duration_s", duration, "w", w, "h", h)
			}
		} else if newType != st.ContentType {
			if err := c.store.UpdatePostContentType(ctx, vm.PostID, newType); err != nil {
				slog.Warn("media transcode consumer: reclassify failed",
					"post_id", vm.PostID, "new_type", newType, "error", err)
			} else {
				slog.Info("media transcode consumer: post reclassified",
					"post_id", vm.PostID, "old_type", st.ContentType, "new_type", newType,
					"duration_s", duration, "w", w, "h", h)
				if c.producer != nil {
					if err := c.producer.PublishPostContentTypeChanged(
						ctx, vm.PostID, st.AuthorID, st.ContentType, newType,
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

// reclassifyDecision applies the rule "a reel is what the author posted as
// a reel; a video is what the author posted as a video" to a post whose
// video has just been measured. It returns the content_type the post should
// have, and keep=true when the current kind must stand regardless of the
// measurement:
//
//   - an explicit kind (content_type_explicit) is the author's choice —
//     flick stays flick, long_video stays long_video;
//   - a flick is never downgraded even when the row predates the explicit
//     flag: every flick was posted from the Reel composer;
//   - anything else — a plain post that defaulted to long_video while
//     transcode was pending — takes the measured type.
func reclassifyDecision(current string, explicit bool, measured string) (newType string, keep bool) {
	if explicit || current == postclassify.Flick {
		return current, true
	}
	return measured, false
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
// handleVoiceSafetyResolved releases (or rejects) voice posts held at
// review_status='pending' once media-service produces a terminal safety
// verdict. Module 1 fixes-v2 / Codex P0-2 — previously the event was
// published but nothing consumed it, so held voice posts never surfaced.
//
// Idempotent by construction: FlipReviewStatusFromPending only applies
// from 'pending', so a duplicate delivery is a no-op.
func (c *MediaTranscodeConsumer) handleVoiceSafetyResolved(ctx context.Context, env *events.EventEnvelope) error {
	var p events.MediaVoiceSafetyResolvedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		slog.Warn("voice safety consumer: bad payload", "error", err)
		return nil // don't loop to DLQ on a malformed message
	}
	mediaID, err := uuid.Parse(p.MediaID)
	if err != nil {
		return nil
	}

	var decision string
	switch p.ModerationStatus {
	case "approved":
		decision = "approved"
	case "rejected":
		decision = "rejected"
	case "failed":
		// No verdict was produced. Route to human review — never approve.
		decision = "flagged"
	default:
		return nil // non-terminal status; wait for the real verdict
	}

	postIDs, err := c.store.PostIDsByMediaID(ctx, mediaID)
	if err != nil {
		// Retryable: returning the error redelivers the event.
		return fmt.Errorf("voice safety: resolve posts for media %s: %w", mediaID, err)
	}
	for _, postID := range postIDs {
		flipped, err := c.store.FlipReviewStatusFromPending(ctx, postID, decision)
		if err != nil {
			return fmt.Errorf("voice safety: flip review status for %s: %w", postID, err)
		}
		if !flipped {
			continue // not pending — already resolved
		}
		confidence := 1.0
		if err := c.store.InsertModerationReview(ctx, &postgres.ModerationReview{
			ReelID:       postID,
			ReviewerType: "auto",
			Decision:     decision,
			Confidence:   &confidence,
		}); err != nil {
			slog.Warn("voice safety: moderation review insert failed",
				"post_id", postID, "error", err)
		}
		if c.rdb != nil {
			c.rdb.Del(ctx, "post:"+postID.String())
		}
		slog.Info("voice safety: post gate resolved",
			"post_id", postID, "media_id", mediaID, "decision", decision)
	}
	return nil
}

func (c *MediaTranscodeConsumer) finalizeReviewGate(ctx context.Context, mediaID uuid.UUID, p *events.MediaTranscodeCompletedPayload) {
	var decision string
	switch {
	case p.ProcessingStatus == "failed":
		decision = "rejected"
	case p.ProcessingStatus == "ready" && p.ModerationStatus == "rejected":
		decision = "rejected"
	case p.ProcessingStatus == "ready" && (p.ModerationStatus == "passed" || p.ModerationStatus == "approved"):
		decision = "approved"
	default:
		return // intermediate or non-approved status (pending, manual_review, scanner failure, etc.) — wait for a terminal verdict
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
