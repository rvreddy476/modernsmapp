package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// applyRepostOriginalAuthorFilter drops reposts whose ORIGINAL author is in
// the viewer's block/mute set (audit L2, 2026-10-30).
//
// The block/mute set is applied to every candidate's AuthorID before
// hydration, and for a repost row that is the reposter. A block of the
// original author was still caught by graph `can` at the privacy step, and
// "Don't recommend" checks both ids, but a one-way graph mute of the
// original author (and a /v1/feed/mute of them) was checked nowhere, so the
// muted account came back through anyone who reposted it. HydratedPost's
// AuthorID is the original author, so the same set is applied to it here.
//
// Only pages that hold a repost pay for it, and resolveBlockedSet is
// memoised per request, so a page that already resolved the set costs
// nothing more. Fails closed like the set's other callers.
func (s *Service) applyRepostOriginalAuthorFilter(ctx context.Context, viewerID uuid.UUID, posts []HydratedPost) ([]HydratedPost, error) {
	hasRepost := false
	for _, p := range posts {
		if p.IsRepost {
			hasRepost = true
			break
		}
	}
	if !hasRepost {
		return posts, nil
	}
	blocked, err := s.resolveBlockedSet(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: block/mute state for reposts could not be resolved: %w", err)
	}
	return dropRepostsOfBlocked(posts, blocked), nil
}

// dropRepostsOfBlocked is the pure step: a repost whose original author is
// in blocked goes; every other row stays.
func dropRepostsOfBlocked(posts []HydratedPost, blocked map[uuid.UUID]struct{}) []HydratedPost {
	if len(blocked) == 0 {
		return posts
	}
	out := posts[:0]
	for _, p := range posts {
		if _, gone := blocked[p.AuthorID]; p.IsRepost && gone {
			continue
		}
		out = append(out, p)
	}
	return out
}
