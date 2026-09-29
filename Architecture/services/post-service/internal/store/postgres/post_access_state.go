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
// from the cache while the row says "not yet". ActiveRestrictionCount joined
// with migration 056: a copyright hold placed after the body was cached must
// bind the next read the same way a rejection does.
type PostAccessState struct {
	ReviewStatus           string
	ActiveRestrictionCount int
	Deleted                bool
	Visibility             string
	AgeRestricted          bool
	PublishAt              *time.Time
}

// EffectiveReviewStatus mirrors Post.EffectiveReviewStatus for the
// revalidation read.
func (s *PostAccessState) EffectiveReviewStatus() string {
	if s == nil {
		return ""
	}
	if s.ActiveRestrictionCount > 0 {
		return ReviewStatusRestricted
	}
	return s.ReviewStatus
}

func (s *Store) GetPostAccessState(ctx context.Context, postID uuid.UUID) (*PostAccessState, error) {
	var state PostAccessState
	err := s.db.QueryRow(ctx, `
		SELECT review_status, active_restriction_count, deleted_at IS NOT NULL, visibility, age_restricted, publish_at
		FROM posts
		WHERE id=$1
	`, postID).Scan(&state.ReviewStatus, &state.ActiveRestrictionCount, &state.Deleted, &state.Visibility, &state.AgeRestricted, &state.PublishAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}
