package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// MTube download (2026-09-27) — the content-authority half of
// GET /v1/media/:id/download in media-service.
//
// media-service can tell that a caller is the uploader; it cannot tell
// whether the post that shows the video allows downloads, nor whether the
// caller may see that post at all. Both are answered here, in that order:
// the ordinary media-access decision first (the same one every playback
// byte goes through), then posts.allow_download on a post the viewer can
// actually see. A story, a channel avatar, and a post whose author turned
// downloads off all answer no; so does anything unresolved.

// ViewerMayDownloadMedia reports whether viewerID may save the bytes of
// mediaID. The error is non-nil only for an unresolved dependency
// (ErrStoryPolicyUnresolved and store errors); a resolved no is (false, nil).
func (s *Service) ViewerMayDownloadMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	if viewerID == uuid.Nil || mediaID == uuid.Nil {
		return false, nil
	}
	access, err := s.ViewerMayAccessMedia(ctx, viewerID, mediaID)
	if err != nil {
		return false, err
	}
	if !access.Allowed || access.Decision != DecisionAllowed {
		// not_ready is "you may watch it once it is ready", which is not a
		// file to hand out yet.
		return false, nil
	}

	posts, err := s.approvedPostsForMedia(ctx, mediaID)
	if err != nil {
		return false, err
	}
	var candidates []*postgres.Post
	for _, p := range posts {
		if p == nil || p.DeletedAt != nil || !p.AllowDownload {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return false, nil
	}
	// The same per-post rule the media-access route applies
	// (postMediaJudge: account privacy, hidden authors, schedule, blocks,
	// audience, shares, age), so a download can never be allowed on a post
	// the viewer could not watch.
	judge, err := s.postMediaJudge(ctx, viewerID, candidates)
	if err != nil {
		return false, err
	}
	for _, p := range candidates {
		if judge(p) {
			return true, nil
		}
	}
	return false, nil
}
