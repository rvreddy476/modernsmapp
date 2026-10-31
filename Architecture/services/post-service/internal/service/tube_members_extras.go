package service

import (
	"context"
	"errors"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Members-only extras (founder decision, 31 Oct 2026). A members-only
// video's end screens, cards and product tags are body content, like its
// chapters: members and the author see them, everyone else gets an empty
// list (not an error, so the player simply draws none). The gate is the one
// the post detail and the chapters route apply, postMembersGate
// (members_read.go); a gate lookup that fails is an error, never the extras.

// membersExtrasOpen reports whether viewerID may see postID's members-only
// extras: true for an ungated video, its author and a member.
func (s *Service) membersExtrasOpen(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (bool, error) {
	err := s.postMembersGate(ctx, postID, viewerID)
	if errors.Is(err, ErrMembersOnly) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListProductTagsForViewer is GET /v1/posts/:postId/product-tags after the
// read gate: the video's tags, or none for a non-member of a members-only
// video.
func (s *Service) ListProductTagsForViewer(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) ([]*postgres.PostProductTag, error) {
	open, err := s.membersExtrasOpen(ctx, postID, viewerID)
	if err != nil {
		return nil, err
	}
	if !open {
		return []*postgres.PostProductTag{}, nil
	}
	if s.productTagListSeam != nil {
		return s.productTagListSeam(ctx, postID)
	}
	return s.ListProductTagsByPost(ctx, postID)
}
