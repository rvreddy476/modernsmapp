package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateProductTagInput is the validated request shape from the HTTP
// handler. The handler is responsible for binding + per-field
// validation; this layer authorises and persists.
type CreateProductTagInput struct {
	PostID          uuid.UUID
	AffiliateLinkID uuid.UUID
	// CallerID must equal the post's author. Handler pulls it from
	// the JWT.
	CallerID uuid.UUID

	TimeStartMS *int32
	TimeEndMS   *int32
	PositionX   *float32
	PositionY   *float32

	// Display payload — cached at tag time so the player avoids an
	// extra fetch into commerce on every viewer. Handler should
	// resolve these from the linked listing.
	Label    string
	ImageURL string
}

// CreateProductTag persists a tag after authorising the caller as the
// post's author. Cross-service validation of the affiliate link itself
// (does it exist? is it the caller's?) is the handler's job — keeping
// the service layer free of monetization-service HTTP calls in the
// unit-test path. The handler short-circuits with a 400 if the
// monetization-service lookup fails.
//
// Permission model
//   - Only the post's author can tag products on their post. Sponsored
//     placements from other creators would need a separate "sponsor
//     request" flow (not in this scaffold).
//   - We deliberately do NOT prevent tagging on someone else's
//     repost / cross-post — those copies are independent posts with
//     their own author_id.
func (s *Service) CreateProductTag(ctx context.Context, in CreateProductTagInput) (*postgres.PostProductTag, error) {
	if in.PostID == uuid.Nil || in.AffiliateLinkID == uuid.Nil || in.CallerID == uuid.Nil {
		return nil, errors.New("post_id, affiliate_link_id, caller_id required")
	}

	// GetPostAuthorID returns ErrNoRows when the post doesn't exist
	// OR has been soft-deleted (deleted_at IS NOT NULL). Both surface
	// as 404 to the caller.
	authorID, err := s.pgStore.GetPostAuthorID(ctx, in.PostID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPostNotFound
		}
		return nil, fmt.Errorf("lookup post: %w", err)
	}
	if authorID != in.CallerID {
		return nil, ErrProductTagNotAuthorized
	}

	tag := &postgres.PostProductTag{
		PostID:          in.PostID,
		AffiliateLinkID: in.AffiliateLinkID,
		CreatorID:       in.CallerID,
		TimeStartMS:     in.TimeStartMS,
		TimeEndMS:       in.TimeEndMS,
		PositionX:       in.PositionX,
		PositionY:       in.PositionY,
		Label:           in.Label,
		ImageURL:        in.ImageURL,
	}
	if err := s.pgStore.CreateProductTag(ctx, tag); err != nil {
		return nil, err
	}

	slog.Info("post product tag created",
		"tag_id", tag.ID, "post_id", in.PostID, "creator_id", in.CallerID)
	return tag, nil
}

// ListProductTagsByPost is the read endpoint the video player calls.
// Anyone who can see the post can see its tags — visibility gating is
// the caller's job (this matches the engagement-count endpoint shape).
func (s *Service) ListProductTagsByPost(ctx context.Context, postID uuid.UUID) ([]*postgres.PostProductTag, error) {
	return s.pgStore.ListProductTagsByPost(ctx, postID)
}

// ListProductTagsByCreator backs the creator-analytics dashboard.
func (s *Service) ListProductTagsByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]*postgres.PostProductTag, error) {
	return s.pgStore.ListProductTagsByCreator(ctx, creatorID, limit, offset)
}

// DeleteProductTag soft-deletes a tag after authorising the caller as
// its original creator.
func (s *Service) DeleteProductTag(ctx context.Context, tagID, callerID uuid.UUID) error {
	tag, err := s.pgStore.GetProductTagByID(ctx, tagID)
	if err != nil {
		return err
	}
	if tag.CreatorID != callerID {
		return ErrProductTagNotAuthorized
	}
	return s.pgStore.SoftDeleteProductTag(ctx, tagID)
}

// RecordProductTagImpression accepts an impression event from the
// player, dedups against the (tag, ipHash) tuple via Redis, and (when
// the dedup window is fresh) bumps the counter. The handler is
// responsible for hashing the IP — keeping a salt on the service
// would leak it through the func signature.
//
// Returns nil on dedup (the player gets 204; the counter just didn't
// move). Errors propagate when the underlying UPDATE fails.
func (s *Service) RecordProductTagImpression(
	ctx context.Context,
	tagID uuid.UUID,
	ipHash string,
) error {
	if !s.AcceptProductTagImpression(ctx, tagID, ipHash) {
		return nil
	}
	return s.pgStore.BumpProductTagImpression(ctx, tagID, 1)
}

// RecordProductTagClick — sibling. Click dedup is shorter than
// impression dedup (15m vs 1h) — see product_tag_dedup.go.
func (s *Service) RecordProductTagClick(
	ctx context.Context,
	tagID uuid.UUID,
	ipHash string,
) error {
	if !s.AcceptProductTagClick(ctx, tagID, ipHash) {
		return nil
	}
	return s.pgStore.BumpProductTagClick(ctx, tagID, 1)
}

// ErrPostNotFound is declared in my_uploads.go and reused here.

// ErrProductTagNotAuthorized — the handler maps this to 403. Not
// reusing a generic "unauthorized" name because future moderator /
// admin overrides might want to distinguish "wrong user" from
// "permission denied for this action".
var ErrProductTagNotAuthorized = errors.New("caller is not the post's author")
