package service

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// batchVisibilityAllows is viewerMayViewPost's per-post rule for a viewer
// who is NOT the author and has already passed the account-level gate,
// applied to a page instead of one post. viewerFollows is whether the
// viewer follows the post's author (only consulted for `followers`).
//
// It differs from viewerMayViewPost in one place on purpose: a `private`
// post is refused even to the users it is shared with, because sharing is
// a single-post gate and the batch serves listings (feed hydration,
// playlists, watch history).
func batchVisibilityAllows(visibility string, viewerFollows bool) bool {
	switch strings.ToLower(visibility) {
	case "", "public", "unlisted":
		return true
	case "followers":
		return viewerFollows
	default:
		// private, the retired circle / trusted / close_friends, staged and
		// unknown values: the author's alone, as in viewerMayViewPost.
		return false
	}
}

// followedAuthorsLookupConcurrency bounds the graph lookups one batch page
// may have in flight.
const followedAuthorsLookupConcurrency = 8

// followedAuthorsOfFollowersPosts answers "does the viewer follow this
// author" for every author of a `followers` post on the page that the viewer
// did not write — one graph lookup per distinct author, run concurrently.
// An author whose lookup fails is absent from the result, so their
// followers-only posts are dropped: fail closed, as in viewerMayViewPost.
// An anonymous viewer follows nobody.
func (s *Service) followedAuthorsOfFollowersPosts(ctx context.Context, posts []postgres.Post, viewerID *uuid.UUID) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	if viewerID == nil {
		return out
	}
	seen := map[uuid.UUID]struct{}{}
	var authors []uuid.UUID
	for _, p := range posts {
		if p.AuthorID == *viewerID || !strings.EqualFold(p.Visibility, "followers") {
			continue
		}
		if _, ok := seen[p.AuthorID]; ok {
			continue
		}
		seen[p.AuthorID] = struct{}{}
		authors = append(authors, p.AuthorID)
	}
	if len(authors) == 0 {
		return out
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, followedAuthorsLookupConcurrency)
	)
	for _, author := range authors {
		wg.Add(1)
		sem <- struct{}{}
		go func(author uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			follows, err := s.checkViewerFollowsAuthor(ctx, *viewerID, author)
			if err != nil {
				log.Printf("Warning: batch visibility graph lookup failed; hiding followers-only posts by %s: %v", author, err)
				return
			}
			if follows {
				mu.Lock()
				out[author] = true
				mu.Unlock()
			}
		}(author)
	}
	wg.Wait()
	return out
}
