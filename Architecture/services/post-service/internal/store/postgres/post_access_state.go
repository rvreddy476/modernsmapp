package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostAccessState is the small canonical state that may revoke public access
// while a cached post body still exists. It is deliberately read from
// PostgreSQL on every cache hit: cache invalidation is best-effort and cannot
// be the safety boundary for moderation or deletion.
//
// Visibility and AgeRestricted joined it with the Creator Hub (2026-09-28):
// a bulk edit to private or 18+ must bind the next read even if the cache
// drop that follows it was lost. PublishAt joined on 2026-09-29 (P-8 d): a
// post rescheduled after its body was cached would otherwise read as live
// from the cache while the row says "not yet".
type PostAccessState struct {
	ReviewStatus  string
	Deleted       bool
	Visibility    string
	AgeRestricted bool
	PublishAt     *time.Time
}

func (s *Store) GetPostAccessState(ctx context.Context, postID uuid.UUID) (*PostAccessState, error) {
	var state PostAccessState
	err := s.db.QueryRow(ctx, `
		SELECT review_status, deleted_at IS NOT NULL, visibility, age_restricted, publish_at
		FROM posts
		WHERE id=$1
	`, postID).Scan(&state.ReviewStatus, &state.Deleted, &state.Visibility, &state.AgeRestricted, &state.PublishAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}
