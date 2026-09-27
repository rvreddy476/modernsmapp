package service

import (
	"context"

	"github.com/google/uuid"
)

// GetLiveRecordings is the public listing of promoted live recordings
// (MTube, 2026-09-27): every post the live VOD consumer created (source =
// 'live') that its creator has since made public, newest first, hydrated
// exactly like the by-author and recent listings — counts from Scylla, the
// live media-state overlay, and the same fail-closed author gate
// (privacy_gate.go), so a private, deactivated or blocked author's
// recording never surfaces here either.
func (s *Service) GetLiveRecordings(ctx context.Context, viewerID *uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.ListLiveRecordings(ctx, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	authorSet := make(map[uuid.UUID]struct{}, len(posts))
	authorIDs := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := authorSet[p.AuthorID]; !ok {
			authorSet[p.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, p.AuthorID)
		}
	}
	viewableAuthor := s.canViewPosts(ctx, viewerID, authorIDs)

	details := make([]PostDetail, 0, len(posts))
	for _, p := range posts {
		if (viewerID == nil || *viewerID != p.AuthorID) && !viewableAuthor[p.AuthorID] {
			continue
		}
		post := p
		counts, _ := s.countsForPost(ctx, p.ID)
		details = append(details, PostDetail{Post: &post, Counts: counts})
	}

	details, err = s.attachMediaStateToDetails(ctx, details, viewerID)
	if err != nil {
		return nil, "", err
	}
	// A shelf of many authors, unlike a profile grid: each card needs its
	// channel and view count, so both ride along (the same best-effort
	// attach trending and my-uploads do; the row type is unchanged).
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		details[i].ViewCount = s.getViewCount(ctx, details[i].Post.ID)
		ptrs[i] = &details[i]
	}
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, ptrs)
	return details, nextCursor, nil
}
