package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Feed read paths (2026-10-30 audit: B1 thread, B2 trending, B3 saved).
//
// Every listing that hands post bodies to a viewer must apply the same
// decision the direct read (GetPost) makes, row by row:
//
//   - the account gate: a private account the viewer does not follow, a block
//     in either direction, a deactivated or pending-delete author
//     (canViewPosts, privacy_gate.go, fail closed);
//   - the post's own audience: followers / friends need the graph edge,
//     private and the retired audiences are the author's alone
//     (batchVisibilityAllowsEdges, batch_visibility.go, fail closed);
//   - moderation: only an effectively approved post (no active restriction)
//     reaches anyone but its author.
//
// The author always sees their own rows. viewerID nil is the signed-out
// stranger. The page's order is kept.

// listReadStore is the store slice behind the thread, trending and saved
// reads and the post-body cache. *postgres.Store satisfies it; listReadSeam
// replaces it in tests.
type listReadStore interface {
	GetPost(ctx context.Context, id uuid.UUID) (*postgres.Post, error)
	GetPostAccessState(ctx context.Context, postID uuid.UUID) (*postgres.PostAccessState, error)
	GetThreadPosts(ctx context.Context, rootID uuid.UUID) ([]postgres.Post, error)
	GetTrendingPosts(ctx context.Context, contentTypes []string, limit int, cursor string) ([]postgres.Post, string, error)
	GetBookmarks(ctx context.Context, userID uuid.UUID, contentTypes []string, limit int, cursor string, scope postgres.SavedScope) ([]postgres.Post, string, error)
}

// listReads is the seam, else the Postgres store, else nil (callers fail
// closed with ErrAuthoringStoreUnavailable).
func (s *Service) listReads() listReadStore {
	if s.listReadSeam != nil {
		return s.listReadSeam
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// postsVisibleToViewer keeps the rows of a listed page the viewer may see,
// in order: the account gate, the per-post audience and the moderation
// state, each for every row the viewer did not write. One graph round trip
// covers the page's authors (canViewPosts) and one more the follow edges of
// its followers / friends rows (followEdgesOfRestrictedPosts).
func (s *Service) postsVisibleToViewer(ctx context.Context, viewerID *uuid.UUID, posts []postgres.Post) []postgres.Post {
	if len(posts) == 0 {
		return posts
	}
	seen := make(map[uuid.UUID]struct{}, len(posts))
	authors := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if isOwnPost(viewerID, p.AuthorID) {
			continue
		}
		if _, ok := seen[p.AuthorID]; !ok {
			seen[p.AuthorID] = struct{}{}
			authors = append(authors, p.AuthorID)
		}
	}
	viewable := s.canViewPosts(ctx, viewerID, authors)
	edges := s.followEdgesOfRestrictedPosts(ctx, posts, viewerID)

	out := make([]postgres.Post, 0, len(posts))
	for _, p := range posts {
		if !isOwnPost(viewerID, p.AuthorID) {
			if !viewable[p.AuthorID] {
				continue
			}
			if !batchVisibilityAllowsEdges(p.Visibility, edges[p.AuthorID]) {
				continue
			}
			if !strings.EqualFold(p.EffectiveReviewStatus(), "approved") {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func isOwnPost(viewerID *uuid.UUID, authorID uuid.UUID) bool {
	return viewerID != nil && *viewerID == authorID
}

// pollBatchStore is the batched poll read behind every listed page.
// *postgres.Store satisfies it; pollSeam replaces it in tests.
type pollBatchStore interface {
	GetPollsByPostIDs(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*postgres.PollData, error)
	GetUserPollVotesBatch(ctx context.Context, postIDs []uuid.UUID, userID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
}

func (s *Service) pollReads() pollBatchStore {
	if s.pollSeam != nil {
		return s.pollSeam
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// attachPolls loads the polls of a page's poll posts in two round trips (the
// polls, then the viewer's votes) instead of two per row. Best-effort, as the
// per-row reads were: a failed read leaves the rows without a poll and the
// page loads.
func (s *Service) attachPolls(ctx context.Context, viewerID *uuid.UUID, posts []*postgres.Post) {
	var ids []uuid.UUID
	for _, p := range posts {
		if p != nil && p.ContentType == "poll" {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	store := s.pollReads()
	if store == nil {
		return
	}
	polls, err := store.GetPollsByPostIDs(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "page polls unavailable; serving rows without polls", "posts", len(ids), "err", err)
		return
	}
	var votes map[uuid.UUID][]uuid.UUID
	if viewerID != nil && len(polls) > 0 {
		votes, err = store.GetUserPollVotesBatch(ctx, ids, *viewerID)
		if err != nil {
			slog.WarnContext(ctx, "page poll votes unavailable; serving polls without the viewer's votes", "posts", len(ids), "err", err)
			votes = nil
		}
	}
	for _, p := range posts {
		if p == nil || p.ContentType != "poll" {
			continue
		}
		poll := polls[p.ID]
		if poll == nil {
			continue
		}
		if v := votes[p.ID]; len(v) > 0 {
			poll.ViewerVotes = v
		}
		p.Poll = poll
	}
}

// detailPosts is the rows of a page of details, for the page-wide helpers.
func detailPosts(details []PostDetail) []*postgres.Post {
	out := make([]*postgres.Post, 0, len(details))
	for i := range details {
		out = append(out, details[i].Post)
	}
	return out
}

// pageDetails builds the details of a listed page with batched counts and
// polls: one counts lookup and one poll lookup for the page, never one per
// row.
func (s *Service) pageDetails(ctx context.Context, viewerID *uuid.UUID, posts []postgres.Post) []PostDetail {
	ids := make([]uuid.UUID, len(posts))
	for i := range posts {
		ids[i] = posts[i].ID
	}
	counts := s.pageCounts(ctx, ids)
	details := make([]PostDetail, len(posts))
	for i := range posts {
		post := posts[i]
		details[i] = PostDetail{Post: &post, Counts: counts[post.ID]}
	}
	s.attachPolls(ctx, viewerID, detailPosts(details))
	return details
}
