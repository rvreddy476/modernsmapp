package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// hashtagGatedAuthors is the distinct authors of rows whose visibility
// depends on the author gate: everyone but the viewer themself.
func hashtagGatedAuthors(viewerID *uuid.UUID, posts []postgres.Post) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(posts))
	out := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := seen[p.AuthorID]; !ok {
			seen[p.AuthorID] = struct{}{}
			out = append(out, p.AuthorID)
		}
	}
	return out
}

// hashtagPostsVisibleTo applies the author gate to a hashtag page: the
// viewer's own posts stay, everyone else's only when canViewPosts allows
// (fail-closed). Order is kept.
func (s *Service) hashtagPostsVisibleTo(ctx context.Context, viewerID *uuid.UUID, posts []postgres.Post) []postgres.Post {
	viewable := s.canViewPosts(ctx, viewerID, hashtagGatedAuthors(viewerID, posts))
	out := make([]postgres.Post, 0, len(posts))
	for _, p := range posts {
		if (viewerID != nil && *viewerID == p.AuthorID) || viewable[p.AuthorID] {
			out = append(out, p)
		}
	}
	return out
}

// hashtagTotalMaxRounds bounds HashtagTotal's narrowing loop. A round past
// the first only happens when the bounded scan was full AND held authors
// the viewer may not see; a tag that has not settled after this many
// rounds gets no count (the page itself still loads).
const hashtagTotalMaxRounds = 8

// ErrHashtagTotalUnsettled: the count could not be made exact within the
// round bound, or graph-service could not answer. The handler omits the
// count rather than show a wrong one.
var ErrHashtagTotalUnsettled = errors.New("hashtag total unavailable")

// HashtagTotal counts the posts GetPostsByHashtag lists for this viewer and
// tag — the same store filter (postgres hashtagPostsFilter: public,
// effectively approved, live, content types) AND the same author gate
// (canViewPosts: private accounts, blocks, deactivated authors) — bounded
// at postgres.HashtagTotalCap (T-34; capped reports the bound was hit).
func (s *Service) HashtagTotal(ctx context.Context, viewerID *uuid.UUID, hashtag string, contentTypes []string) (total int, capped bool, err error) {
	count := func(excluded []uuid.UUID) (map[uuid.UUID]int, bool, error) {
		return s.pgStore.CountPostsByHashtag(ctx, hashtag, contentTypes, excluded)
	}
	gate := func(authors []uuid.UUID) (map[uuid.UUID]bool, bool) {
		return s.canViewPostsResolved(ctx, viewerID, authors)
	}
	return settleHashtagTotal(viewerID, count, gate)
}

// settleHashtagTotal is HashtagTotal's loop, over seams for the store and
// the author gate.
//
// count returns per-author counts over at most cap+1 eligible rows (minus
// the excluded authors) and whether that bound was hit. The authors are put
// to gate and the denied ones summed out. If the scan was not full, that
// sum is exact. If it was full and nobody was denied, more than cap visible
// posts exist (capped). If it was full and someone was denied, the denied
// authors are excluded in SQL and the scan runs again, so the bound always
// applies to posts this viewer can see.
func settleHashtagTotal(
	viewerID *uuid.UUID,
	count func(excluded []uuid.UUID) (map[uuid.UUID]int, bool, error),
	gate func(authors []uuid.UUID) (map[uuid.UUID]bool, bool),
) (int, bool, error) {
	decided := make(map[uuid.UUID]bool)
	var excluded []uuid.UUID
	for round := 0; round < hashtagTotalMaxRounds; round++ {
		byAuthor, truncated, err := count(excluded)
		if err != nil {
			return 0, false, err
		}
		var ask []uuid.UUID
		for author := range byAuthor {
			if viewerID != nil && *viewerID == author {
				decided[author] = true // self-view is never gated
				continue
			}
			if _, ok := decided[author]; !ok {
				ask = append(ask, author)
			}
		}
		if len(ask) > 0 {
			answers, resolved := gate(ask)
			if !resolved {
				// The list fails closed on an outage; a count built on
				// those denials would be a wrong number, not a safe one.
				return 0, false, fmt.Errorf("%w: author gate unresolved", ErrHashtagTotalUnsettled)
			}
			for _, author := range ask {
				decided[author] = answers[author]
			}
		}
		visible, denied := 0, false
		for author, n := range byAuthor {
			if decided[author] {
				visible += n
			} else {
				denied = true
				excluded = append(excluded, author)
			}
		}
		switch {
		case !truncated:
			return visible, false, nil
		case !denied:
			return postgres.HashtagTotalCap, true, nil
		}
	}
	return 0, false, fmt.Errorf("%w: not settled in %d rounds", ErrHashtagTotalUnsettled, hashtagTotalMaxRounds)
}
