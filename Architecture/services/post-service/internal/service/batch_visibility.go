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
	return batchVisibilityAllowsEdges(visibility, followEdges{follows: viewerFollows})
}

// followEdges is the viewer's two follow edges with one author: follows =
// viewer→author, followedBy = author→viewer. The zero value is a stranger.
type followEdges struct {
	follows    bool
	followedBy bool
}

// batchVisibilityAllowsEdges is batchVisibilityAllows with the friends
// audience (2026-10-18): a `friends` post needs BOTH edges.
func batchVisibilityAllowsEdges(visibility string, edges followEdges) bool {
	switch strings.ToLower(visibility) {
	case "", "public", "unlisted":
		return true
	case "followers":
		return edges.follows
	case PostVisibilityFriends:
		return edges.follows && edges.followedBy
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
	followersOnly := make([]postgres.Post, 0, len(posts))
	for _, p := range posts {
		if strings.EqualFold(p.Visibility, "followers") {
			followersOnly = append(followersOnly, p)
		}
	}
	out := map[uuid.UUID]bool{}
	for author, edges := range s.followEdgesOfRestrictedPosts(ctx, followersOnly, viewerID) {
		if edges.follows {
			out[author] = true
		}
	}
	return out
}

// restrictedAudience reports whether a post's visibility needs the graph:
// followers (one edge) or friends (both).
func restrictedAudience(visibility string) bool {
	switch strings.ToLower(visibility) {
	case "followers", PostVisibilityFriends:
		return true
	}
	return false
}

// followEdgesOfRestrictedPosts is followedAuthorsOfFollowersPosts for both
// restricted audiences: one graph lookup per distinct author of a followers
// or friends post the viewer did not write, run concurrently, answering both
// edges. An author whose lookup fails is absent from the result, so their
// restricted posts are dropped: fail closed. An anonymous viewer has no edges.
func (s *Service) followEdgesOfRestrictedPosts(ctx context.Context, posts []postgres.Post, viewerID *uuid.UUID) map[uuid.UUID]followEdges {
	out := map[uuid.UUID]followEdges{}
	if viewerID == nil {
		return out
	}
	seen := map[uuid.UUID]struct{}{}
	var authors []uuid.UUID
	for _, p := range posts {
		if p.AuthorID == *viewerID || !restrictedAudience(p.Visibility) {
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
			follows, followedBy, err := s.fetchFollowEdges(ctx, *viewerID, author)
			if err != nil {
				log.Printf("Warning: batch visibility graph lookup failed; hiding restricted posts by %s: %v", author, err)
				return
			}
			mu.Lock()
			out[author] = followEdges{follows: follows, followedBy: followedBy}
			mu.Unlock()
		}(author)
	}
	wg.Wait()
	return out
}
