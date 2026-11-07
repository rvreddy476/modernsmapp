// post_video_tools.go holds the creator tools for a video post: video
// detail, trim, category override, cover frame, publishing a staged video,
// and changing where a post is distributed. Moved out of post.go unchanged.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// ── Video Creator Tools ────────────────────────────────────────

// GetVideoDetail returns the video metadata for a post. INTERNAL USE ONLY —
// it returns storage_video_url, the object-storage path of the source file,
// which no viewer may see. Handlers must call GetVideoDetailForCaller.
func (s *Service) GetVideoDetail(ctx context.Context, postID uuid.UUID) (*postgres.VideoMetadata, error) {
	return s.pgStore.GetVideoMetadata(ctx, postID)
}

// GetVideoDetailForCaller is the read behind GET /v1/videos/:videoId. It
// returns the same metadata as GetVideoDetail with the owner-only fields
// redacted for anyone who is not the post's author.
//
// storage_video_url is the internal object-storage location of the source
// upload — not the CDN playback URL a player needs. The endpoint is
// unauthenticated at the edge (the gateway forwards tokenless requests and
// leaves the 401 to us), so before this it handed that path to anyone who
// could guess or scrape a post id. Redacting rather than 401-ing keeps the
// duration / dimensions / playback_url that a viewer legitimately reads
// available, and closes the leak for every caller who is not the owner.
//
// Fail closed: a nil caller, a failed author lookup, or an unwired store all
// redact. The only branch that keeps the field is a positive match between
// the caller and the post's author.
func (s *Service) GetVideoDetailForCaller(ctx context.Context, postID uuid.UUID, callerID *uuid.UUID) (*postgres.VideoMetadata, error) {
	if s.authoringOwners == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	// The post's own read decision first (2026-09-28): this route answered
	// playback_url for any post id, private and age-restricted ones
	// included. Same gate as GET /v1/posts/:id, private shares honoured.
	if err := s.singlePostRead(ctx, postID, callerID); err != nil {
		return nil, err
	}
	vm, err := s.authoringOwners.GetVideoMetadata(ctx, postID)
	if err != nil {
		return nil, err
	}
	if vm == nil {
		return nil, ErrPostNotFound
	}
	if callerID == nil || s.requirePostAuthor(ctx, *callerID, postID) != nil {
		redacted := *vm
		redacted.StorageVideoURL = nil
		// Members-only video (2026-10-23): a non-member gets the metadata —
		// duration, dimensions, category — but no URL to play it from. The
		// one membership rule (members_read.go): members and the author keep
		// playback_url; a signed-out caller, a non-member and an unresolved
		// gate or lookup lose it.
		if s.postMembersGate(ctx, postID, callerID) != nil {
			redacted.PlaybackURL = nil
		}
		return &redacted, nil
	}
	return vm, nil
}

// UpdateVideoTrim updates trim points for a video.
func (s *Service) UpdateVideoTrim(ctx context.Context, postID, userID uuid.UUID, startMs int, endMs *int) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
	if err != nil {
		return fmt.Errorf("video metadata not found")
	}

	// Validate: 0 <= start < end <= duration*1000
	maxMs := int(vm.DurationSeconds * 1000)
	if startMs < 0 {
		return fmt.Errorf("trim_start_ms must be >= 0")
	}
	effectiveEnd := maxMs
	if endMs != nil {
		effectiveEnd = *endMs
	}
	if startMs >= effectiveEnd {
		return fmt.Errorf("trim_start_ms must be less than trim_end_ms")
	}
	if effectiveEnd > maxMs {
		return fmt.Errorf("trim_end_ms exceeds video duration")
	}

	return s.pgStore.UpdateTrim(ctx, postID, startMs, endMs)
}

// OverrideCategory overrides the video category classification.
func (s *Service) OverrideCategory(ctx context.Context, postID, userID uuid.UUID, category string) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	if category != "flick" && category != "long_video" {
		return fmt.Errorf("invalid category: must be flick or long_video")
	}

	vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
	if err != nil {
		return fmt.Errorf("video metadata not found")
	}

	if err := ValidateCategoryOverride(vm, category); err != nil {
		return err
	}

	return s.pgStore.UpdateFinalCategory(ctx, postID, category)
}

// SetCoverFrame sets the cover frame for a video.
func (s *Service) SetCoverFrame(ctx context.Context, postID, userID uuid.UUID, coverMediaID *uuid.UUID, thumbnailURL *string) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	// Update cover_media_id on the post
	if coverMediaID != nil {
		// Authority before the write (cover_guard.go); nothing is written,
		// thumbnail included, when it fails.
		if err := s.verifyCoverOnWrite(ctx, authorID, *coverMediaID); err != nil {
			return err
		}
		// Claimed before the row names it; the cover it replaces is
		// released after (media_claims.go).
		if err := s.setPostCoverClaimed(ctx, postID, *coverMediaID, func() error {
			return s.pgStore.UpdatePostCoverMedia(ctx, postID, coverMediaID)
		}); err != nil {
			return err
		}
		// Tier 1b: cover_media_id is in the cached body.
		s.InvalidatePostBodyCache(ctx, postID)
	}

	// Update thumbnail_url on video_metadata
	if thumbnailURL != nil {
		vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
		if err != nil {
			return fmt.Errorf("video metadata not found")
		}
		vm.ThumbnailURL = thumbnailURL
		return s.pgStore.UpdateVideoMetadata(ctx, vm)
	}

	return nil
}

// PublishVideo is POST /v1/videos/:videoId/publish: a SCHEDULED video goes
// live now, keeping the visibility its creator chose (PublishScheduled, the
// same flip "publish now" and the schedule worker make).
//
// It used to UPDATE visibility = 'public' on any post — a private or
// unlisted video, or one staged for review, went public on one call
// (2026-09-29 audit). A post that is already live is left exactly as it is
// and the call answers 200, as before. No client calls this route today: the
// upload studio publishes through the create path and PATCH
// /v1/posts/:id/schedule, so nothing relied on the old flip.
func (s *Service) PublishVideo(ctx context.Context, postID, userID uuid.UUID) error {
	authorID, err := s.pgStore.GetPostAuthorID(ctx, postID)
	if err != nil {
		return fmt.Errorf("post not found")
	}
	if authorID != userID {
		return fmt.Errorf("unauthorized")
	}

	vm, err := s.pgStore.GetVideoMetadata(ctx, postID)
	if err != nil {
		return fmt.Errorf("video metadata not found")
	}

	if vm.UploadStatus != "ready" {
		return fmt.Errorf("video not ready: current status is %s", vm.UploadStatus)
	}

	if _, err := s.PublishScheduled(ctx, postID, &userID, false); err != nil {
		return err
	}
	return nil
}

// UpdateDistribution replaces a post's distribution policy (owner-only).
// The row update, the monotonic rev bump, and the PostDistributionUpdated
// outbox event commit in one transaction. Returns the updated post.
func (s *Service) UpdateDistribution(ctx context.Context, postID, actorID uuid.UUID, raw json.RawMessage) (*postgres.Post, error) {
	policy, err := ParseDistributionPolicy(raw)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		// Explicitly clearing a policy back to legacy behavior is not a
		// supported operation — reject rather than silently accept.
		return nil, fmt.Errorf("%w: policy document required", ErrInvalidDistribution)
	}
	stored, err := MarshalPolicy(policy)
	if err != nil {
		return nil, err
	}

	// Fetch first for the event's content_type + existence/ownership
	// pre-check (the UPDATE re-checks ownership atomically).
	existing, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrPostNotFound
	}
	if existing.AuthorID != actorID {
		return nil, ErrNotPostAuthor
	}

	resolved := ResolveDistribution(policy)
	_, err = s.pgStore.UpdateDistribution(ctx, postID, actorID, stored,
		func(rev int64) (string, interface{}) {
			return events.PostDistributionUpdated, events.PostDistributionUpdatedPayload{
				PostID:            postID.String(),
				AuthorID:          actorID.String(),
				ContentType:       existing.ContentType,
				MainFeed:          resolved.MainFeed,
				NotifySubscribers: resolved.NotifySubscribers,
				DistributionRev:   rev,
				UpdatedAt:         time.Now().UTC(),
			}
		})
	if err != nil {
		return nil, err
	}
	return s.pgStore.GetPost(ctx, postID)
}
