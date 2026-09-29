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
func (s *Service) verifyCoverOnWrite(ctx context.Context, authorID, coverID uuid.UUID) error {
	ownership, err := s.pgStore.BatchGetMediaOwnership(ctx, []uuid.UUID{coverID})
	if err != nil {
		// FAIL CLOSED, like verifyMediaAuthority: an unreadable authority is
		// not permission.
		return fmt.Errorf("verify cover media: %w", err)
	}
	m, ok := ownership[coverID]
	if err := checkCoverAuthority(coverID, authorID, m, ok); err != nil {
		return &CoverMediaError{Err: err}
	}
	return nil
}

// checkCoverAuthority is the whole cover decision, as a pure function, and
// it is verifyCoverMedia's: the asset exists, the author uploaded it, it is
// confirmed and not refused, and it is an IMAGE. One rule on every route.
//
// A video is never a cover, the post's own included. The web frame picker
// used to name the video itself and carry the picture in thumbnail_url; it
// now uploads the frame as an image (postbook-ui e00b88b), and every card
// draws the cover as /v1/media/<cover>/serve, where a video is a broken
// picture.
//
// Ownership is asked before kind, so a stranger's asset answers
// ErrMediaNotOwned whatever it is, and the answer says nothing about it.
func checkCoverAuthority(
	coverID, authorID uuid.UUID,
	m postgres.MediaOwnership,
	found bool,
) error {
	return checkMediaAuthority(coverID, authorID, m, found, "post", postTypeImage)
}
