package postgres

import (
	"context"

	"github.com/google/uuid"
)

// ReviewerStats are the per-reviewer dashboard numbers.
type ReviewerStats struct {
	ReviewsCompleted    int   `json:"reviews_completed"`
	Escalated           int   `json:"escalated"`
	LifetimeEarnedPaise int64 `json:"lifetime_earned_paise"`
}

// StatsForReviewer aggregates a reviewer's completed reviews, escalations, and
// net ledger earnings.
func (s *Store) StatsForReviewer(ctx context.Context, reviewerID uuid.UUID) (ReviewerStats, error) {
	var st ReviewerStats
	err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM reviewer.review_assignments
			   WHERE reviewer_id = $1 AND status = 'completed' AND kind = 'primary'),
			(SELECT count(*) FROM reviewer.review_assignments
			   WHERE reviewer_id = $1 AND decision = 'escalate'),
			(SELECT COALESCE(SUM(amount_minor), 0) FROM reviewer.reviewer_ledger
			   WHERE reviewer_id = $1)`,
		reviewerID).Scan(&st.ReviewsCompleted, &st.Escalated, &st.LifetimeEarnedPaise)
	return st, err
}

// QueueDepth is the number of unclaimed items waiting for a human.
func (s *Store) QueueDepth(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM reviewer.review_queue WHERE claimed = false`).Scan(&n)
	return n, err
}

// OpenEscalationCount is the super-admin queue depth.
func (s *Store) OpenEscalationCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM reviewer.escalations WHERE status = 'open'`).Scan(&n)
	return n, err
}
