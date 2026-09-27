package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

/*
	Tune = the viewer's PRIVATE dislike (MTube, 2026-09-27).

	  POST   /v1/posts/:id/tune      dislike
	  DELETE /v1/posts/:id/tune      undo
	  GET    /v1/posts/:id/tune/me   {tuned}

	Like and tune are mutually exclusive: tuning a post the viewer likes
	removes the like (through the same ToggleLike path the like button
	uses, so the counters, Scylla and the engagement events all agree), and
	liking a tuned post removes the tune row (ToggleLike, post.go). The tune
	row lives in Postgres and the like in Redis + Scylla, so "one
	transaction" here is one ordered operation: the tune row is written
	first and the like is toggled off after it; if the toggle fails the
	tune stands and the like is retried by the client's next tap. No dislike
	count exists anywhere: the row is only ever read back as the viewer's
	own viewer_disliked.
*/

// CreateTune records the viewer's dislike and removes their like, if any.
func (s *Service) CreateTune(ctx context.Context, userID, postID uuid.UUID) error {
	if err := s.pgStore.CreateTune(ctx, userID, postID); err != nil {
		return err
	}
	if s.rdb != nil && s.IsLikedFromRedis(ctx, userID, postID) {
		// ToggleLike on a liked post is the unlike; it does not touch the
		// tune row on the way down (only a like being SET clears the tune).
		if _, err := s.ToggleLike(ctx, postID, userID); err != nil {
			slog.WarnContext(ctx, "tune: could not remove the like", "post_id", postID, "err", err)
			return fmt.Errorf("tune recorded but the like could not be removed: %w", err)
		}
	}
	return nil
}

func (s *Service) DeleteTune(ctx context.Context, userID, postID uuid.UUID) error {
	return s.pgStore.DeleteTune(ctx, userID, postID)
}

func (s *Service) HasTune(ctx context.Context, userID, postID uuid.UUID) (bool, error) {
	return s.pgStore.HasTune(ctx, userID, postID)
}

// viewerDisliked answers viewer_disliked for one post; best-effort false.
func (s *Service) viewerDisliked(ctx context.Context, viewerID *uuid.UUID, postID uuid.UUID) bool {
	if viewerID == nil || s.pgStore == nil {
		return false
	}
	tuned, err := s.pgStore.HasTune(ctx, *viewerID, postID)
	return err == nil && tuned
}

// viewerDislikedBatch answers viewer_disliked for a page in one query.
func (s *Service) viewerDislikedBatch(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) map[uuid.UUID]bool {
	if s.pgStore == nil || viewerID == uuid.Nil || len(postIDs) == 0 {
		return map[uuid.UUID]bool{}
	}
	out, err := s.pgStore.BatchHasTune(ctx, viewerID, postIDs)
	if err != nil {
		slog.WarnContext(ctx, "viewer_disliked batch skipped", "err", err)
		return map[uuid.UUID]bool{}
	}
	return out
}
