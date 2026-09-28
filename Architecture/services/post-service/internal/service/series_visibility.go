package service

import (
	"context"
	"errors"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	The post detail's read gate on single-post surfaces that had none
	(2026-09-29, the leak list from the Creator Hub audit).

	PostReadGate is singlePostRead for the handlers: visibility incl. private
	shares, then the age gate. Each route that serves or writes something
	about ONE post runs it before the service call and answers its refusal
	exactly as GET /v1/posts/:postId does (404 NOT_FOUND, 401/403 AGE_*).

	Episode lists: a series is public, its episodes need not be. Every list
	of episodes drops the ones the viewer may not open (private, unlisted to
	a stranger is still openable, followers-only, an 18+ post for someone who
	does not pass, a still-processing one); the series owner sees all.
*/

// ErrFlickSeriesNotFound is the reels series read's 404.
var ErrFlickSeriesNotFound = errors.New("series not found")

// PostReadGate is the direct read's decision for one post id: nil when the
// viewer may open it, else ErrPostNotVisible or an age refusal.
func (s *Service) PostReadGate(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) error {
	return s.singlePostRead(ctx, postID, viewerID)
}

// viewablePostSet answers PostReadGate for each id; an id the gate refuses
// for any reason (an error included) is absent. Fail closed.
func (s *Service) viewablePostSet(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if _, seen := out[id]; seen {
			continue
		}
		out[id] = s.singlePostRead(ctx, id, viewerID) == nil
	}
	return out
}

// visibleVideoEpisodes drops the episodes the viewer may not open.
func (s *Service) visibleVideoEpisodes(ctx context.Context, eps []postgres.VideoSeriesEpisode, viewerID *uuid.UUID) []postgres.VideoSeriesEpisode {
	ids := make([]uuid.UUID, len(eps))
	for i, ep := range eps {
		ids[i] = ep.PostID
	}
	ok := s.viewablePostSet(ctx, ids, viewerID)
	out := make([]postgres.VideoSeriesEpisode, 0, len(eps))
	for _, ep := range eps {
		if ok[ep.PostID] {
			out = append(out, ep)
		}
	}
	return out
}

// flickSeriesStore is the reels-series read; an interface so the episode
// filter is testable without a database.
type flickSeriesStore interface {
	GetFlickSeries(ctx context.Context, seriesID uuid.UUID) (*postgres.FlickSeries, error)
	GetSeriesEpisodes(ctx context.Context, seriesID uuid.UUID) ([]postgres.FlickSeriesItem, error)
}

// GetSeriesEpisodes is GET /v1/series/:seriesId/episodes: the series
// owner's full list, everyone else's without the episodes they may not open.
func (s *Service) GetSeriesEpisodes(ctx context.Context, seriesID uuid.UUID, viewerID *uuid.UUID) ([]postgres.FlickSeriesItem, error) {
	if s.flickSeries == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	fs, err := s.flickSeries.GetFlickSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, ErrFlickSeriesNotFound
	}
	items, err := s.flickSeries.GetSeriesEpisodes(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if viewerID != nil && *viewerID == fs.CreatorID {
		return items, nil
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.PostID
	}
	ok := s.viewablePostSet(ctx, ids, viewerID)
	out := make([]postgres.FlickSeriesItem, 0, len(items))
	for _, it := range items {
		if ok[it.PostID] {
			out = append(out, it)
		}
	}
	return out, nil
}

// gateCommentPost is the post detail's gate for a route keyed by a comment
// id: COMMENT_NOT_FOUND for a missing comment or a post the viewer may not
// open (the answer the comment like toggle gives), an age refusal as is.
func (s *Service) gateCommentPost(ctx context.Context, commentID uuid.UUID, viewerID *uuid.UUID) error {
	if s.pgStore == nil {
		return ErrAuthoringStoreUnavailable
	}
	c, err := s.pgStore.GetVisibleCommentByID(ctx, commentID)
	if err != nil {
		return err
	}
	if err := s.singlePostRead(ctx, c.PostID, viewerID); err != nil {
		if errors.Is(err, ErrPostNotVisible) || errors.Is(err, ErrPostNotFound) {
			return errCommentNotFound
		}
		return err
	}
	return nil
}

// errCommentNotFound carries the text the comment handlers switch on.
var errCommentNotFound = errors.New("COMMENT_NOT_FOUND")

// ViewablePostIDs keeps, in order, the ids PostReadGate lets the viewer open.
func (s *Service) ViewablePostIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) []uuid.UUID {
	ok := s.viewablePostSet(ctx, ids, viewerID)
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if ok[id] {
			out = append(out, id)
		}
	}
	return out
}
