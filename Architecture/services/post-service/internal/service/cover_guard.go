package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Cover authority on the WRITE paths (2026-09-29).
//
// POST /v1/posts (and the draft publishes that land in CreatePost) and
// POST /v1/videos/:id/cover-frame stored cover_media_id as sent: any media id
// became the cover, a stranger's included. Only PATCH /v1/posts/:id asked
// (verifyCoverMedia). The read side already refuses to let a post stand for a
// cover its author did not upload (store/postgres/posts.go,
// postCoverOwnedByAuthor); this is the same rule where the value is written,
// so the row never names an asset its author may not use.

// CoverMediaError marks a refusal of the COVER, as opposed to an attachment.
// It wraps the same sentinels (ErrMediaNotFound, ErrMediaNotOwned,
// ErrMediaNotReady, ErrMediaTypeMismatch), so errors.Is still works; the
// handlers use the type to answer with the owner-edit route's statuses
// (PostEditErrorStatus), whichever route the cover arrived on.
type CoverMediaError struct{ Err error }

func (e *CoverMediaError) Error() string { return e.Err.Error() }
func (e *CoverMediaError) Unwrap() error { return e.Err }

// verifyCoverOnWrite is the cover's authority on create and cover-frame.
// attached is the post's own media: what the request attaches on create, what
// the post already attaches on cover-frame.
func (s *Service) verifyCoverOnWrite(ctx context.Context, authorID, coverID uuid.UUID, attached []uuid.UUID) error {
	ownership, err := s.pgStore.BatchGetMediaOwnership(ctx, []uuid.UUID{coverID})
	if err != nil {
		// FAIL CLOSED, like verifyMediaAuthority: an unreadable authority is
		// not permission.
		return fmt.Errorf("verify cover media: %w", err)
	}
	m, ok := ownership[coverID]
	if err := checkCoverAuthority(coverID, authorID, m, ok, attached); err != nil {
		return &CoverMediaError{Err: err}
	}
	return nil
}

// checkCoverAuthority is the whole cover decision, as a pure function.
//
// The rules are verifyCoverMedia's: the asset exists, the author uploaded it,
// it is confirmed and not refused, and it is an IMAGE. One case is added,
// because a shipped client depends on it: the frame picker (web
// extractCoverFrame, then cover-frame) names the VIDEO ITSELF as the cover
// and carries the picture in thumbnail_url. A video is therefore accepted
// only when it is one of this post's own attachments, which adds nothing to
// the post's audience; every other video is a type mismatch, as on PATCH.
//
// Ownership is asked first in both branches, so a stranger's asset answers
// ErrMediaNotOwned whatever its kind, and the answer says nothing about it.
func checkCoverAuthority(
	coverID, authorID uuid.UUID,
	m postgres.MediaOwnership,
	found bool,
	attached []uuid.UUID,
) error {
	if found && m.Kind == mediaKindVideo && containsMediaID(attached, coverID) {
		return checkMediaAuthority(coverID, authorID, m, found, "long_video", "video")
	}
	return checkMediaAuthority(coverID, authorID, m, found, "post", postTypeImage)
}

func containsMediaID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
