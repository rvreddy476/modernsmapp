package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAlreadyClaimed is returned when a queue item was claimed by another
// reviewer between candidate selection and the atomic claim.
var ErrAlreadyClaimed = errors.New("content already claimed")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

type Reviewer struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	Status           string    `json:"status"`
	Tier             string    `json:"tier"`
	Languages        []string  `json:"languages"`
	Region           string    `json:"region"`
	ReviewerAccuracy float64   `json:"reviewer_accuracy"`
	MaxConcurrent    int       `json:"max_concurrent"`
	KYCVerified      bool      `json:"kyc_verified"`
	IsOnline         bool      `json:"is_online"`
}

type QueueItem struct {
	ContentID      uuid.UUID `json:"content_id"`
	CreatorID      uuid.UUID `json:"creator_id"`
	ContentType    string    `json:"content_type"`
	Languages      []string  `json:"languages"`
	ContentSeconds int       `json:"content_seconds"`
	// SpamScore is a transient pre-filter signal (not persisted in review_queue).
	SpamScore float64 `json:"spam_score"`
}

type Assignment struct {
	ID             uuid.UUID  `json:"id"`
	ContentID      uuid.UUID  `json:"content_id"`
	CreatorID      uuid.UUID  `json:"creator_id,omitempty"` // omitted from blind responses
	ReviewerID     uuid.UUID  `json:"reviewer_id"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Decision       *string    `json:"decision,omitempty"`
	ContentSeconds int        `json:"content_seconds"`
	WatchedSeconds int        `json:"watched_seconds"`
	AssignedAt     time.Time  `json:"assigned_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
}

const reviewerCols = `id, user_id, status, tier, languages, region,
	reviewer_accuracy::float8, max_concurrent, kyc_verified, is_online`

func scanReviewer(row pgx.Row) (*Reviewer, error) {
	var r Reviewer
	err := row.Scan(&r.ID, &r.UserID, &r.Status, &r.Tier, &r.Languages, &r.Region,
		&r.ReviewerAccuracy, &r.MaxConcurrent, &r.KYCVerified, &r.IsOnline)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// OptIn creates (or returns) the reviewer row for a user.
func (s *Store) OptIn(ctx context.Context, userID uuid.UUID, languages []string, region string) (*Reviewer, error) {
	if languages == nil {
		languages = []string{}
	}
	row := s.db.QueryRow(ctx, `
		INSERT INTO reviewer.reviewers (user_id, languages, region)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE
			SET languages = EXCLUDED.languages, region = EXCLUDED.region, updated_at = now()
		RETURNING `+reviewerCols, userID, languages, region)
	return scanReviewer(row)
}

func (s *Store) GetReviewerByUser(ctx context.Context, userID uuid.UUID) (*Reviewer, error) {
	return scanReviewer(s.db.QueryRow(ctx,
		`SELECT `+reviewerCols+` FROM reviewer.reviewers WHERE user_id = $1`, userID))
}

func (s *Store) SetOnline(ctx context.Context, reviewerID uuid.UUID, online bool) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reviewer.reviewers SET is_online = $2, updated_at = now() WHERE id = $1`,
		reviewerID, online)
	return err
}

// Enqueue adds content awaiting human review (idempotent on content_id).
func (s *Store) Enqueue(ctx context.Context, q QueueItem) error {
	if q.Languages == nil {
		q.Languages = []string{}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO reviewer.review_queue (content_id, creator_id, content_type, languages, content_seconds)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (content_id) DO NOTHING`,
		q.ContentID, q.CreatorID, q.ContentType, q.Languages, q.ContentSeconds)
	return err
}

func (s *Store) ActiveAssignmentCount(ctx context.Context, reviewerID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM reviewer.review_assignments
		WHERE reviewer_id = $1 AND status IN ('assigned','in_progress')`, reviewerID).Scan(&n)
	return n, err
}

// CandidateQueue returns unclaimed queue items the reviewer is language-eligible
// for and under the rotation cap K (assignments for the same creator in the
// rolling window). Graph (relationship) eligibility is checked by the caller.
func (s *Store) CandidateQueue(ctx context.Context, reviewerID uuid.UUID, languages []string, rotationCapK, limit int) ([]QueueItem, error) {
	if languages == nil {
		languages = []string{}
	}
	rows, err := s.db.Query(ctx, `
		SELECT q.content_id, q.creator_id, q.content_type, q.languages, q.content_seconds
		FROM reviewer.review_queue q
		WHERE q.claimed = false
		  AND (cardinality($2::text[]) = 0 OR cardinality(q.languages) = 0 OR q.languages && $2::text[])
		  AND (SELECT count(*) FROM reviewer.review_assignments a
		        WHERE a.reviewer_id = $1 AND a.creator_id = q.creator_id
		          AND a.assigned_at > now() - interval '7 days') < $3
		ORDER BY q.enqueued_at ASC
		LIMIT $4`, reviewerID, languages, rotationCapK, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueueItem
	for rows.Next() {
		var q QueueItem
		if err := rows.Scan(&q.ContentID, &q.CreatorID, &q.ContentType, &q.Languages, &q.ContentSeconds); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ClaimAndAssign atomically claims a queue item and creates the assignment.
// Returns ErrAlreadyClaimed if another reviewer won the race.
func (s *Store) ClaimAndAssign(ctx context.Context, item QueueItem, reviewerID uuid.UUID, ttl time.Duration) (*Assignment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var creatorID uuid.UUID
	var contentSeconds int
	err = tx.QueryRow(ctx, `
		UPDATE reviewer.review_queue SET claimed = true
		WHERE content_id = $1 AND claimed = false
		RETURNING creator_id, content_seconds`, item.ContentID).Scan(&creatorID, &contentSeconds)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAlreadyClaimed
		}
		return nil, err
	}

	var a Assignment
	err = tx.QueryRow(ctx, `
		INSERT INTO reviewer.review_assignments
			(content_id, creator_id, reviewer_id, content_seconds, status, expires_at)
		VALUES ($1, $2, $3, $4, 'assigned', now() + $5::interval)
		RETURNING id, content_id, creator_id, reviewer_id, kind, status,
			content_seconds, watched_seconds, assigned_at, expires_at`,
		item.ContentID, creatorID, reviewerID, contentSeconds,
		ttl.String()).Scan(&a.ID, &a.ContentID, &a.CreatorID, &a.ReviewerID, &a.Kind,
		&a.Status, &a.ContentSeconds, &a.WatchedSeconds, &a.AssignedAt, &a.ExpiresAt)
	if err != nil {
		// Partial unique index (one_active_review) means another active
		// assignment exists for this content — treat as a lost race.
		return nil, ErrAlreadyClaimed
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &a, nil
}

// Heartbeat adds active watch time, capped at content_seconds × 1.2.
func (s *Store) Heartbeat(ctx context.Context, assignmentID, reviewerID uuid.UUID, addSeconds int) (int, error) {
	var watched int
	err := s.db.QueryRow(ctx, `
		UPDATE reviewer.review_assignments
		SET watched_seconds = LEAST(watched_seconds + $3, (content_seconds * 12) / 10),
		    status = 'in_progress'
		WHERE id = $1 AND reviewer_id = $2 AND status IN ('assigned','in_progress')
		RETURNING watched_seconds`, assignmentID, reviewerID, addSeconds).Scan(&watched)
	return watched, err
}

// Decide records the reviewer's decision and completes the assignment.
func (s *Store) Decide(ctx context.Context, assignmentID, reviewerID uuid.UUID, decision, reason string) (*Assignment, error) {
	var a Assignment
	err := s.db.QueryRow(ctx, `
		UPDATE reviewer.review_assignments
		SET decision = $3, decision_reason = $4, status = 'completed', decided_at = now()
		WHERE id = $1 AND reviewer_id = $2 AND status IN ('assigned','in_progress')
		RETURNING id, content_id, creator_id, reviewer_id, kind, status,
			content_seconds, watched_seconds, assigned_at, expires_at, decided_at`,
		assignmentID, reviewerID, decision, reason).Scan(&a.ID, &a.ContentID, &a.CreatorID,
		&a.ReviewerID, &a.Kind, &a.Status, &a.ContentSeconds, &a.WatchedSeconds,
		&a.AssignedAt, &a.ExpiresAt, &a.DecidedAt)
	if err != nil {
		return nil, err
	}
	a.Decision = &decision
	return &a, nil
}

// MarkBasePaid flips base_paid and appends the base ledger accrual row.
func (s *Store) MarkBasePaid(ctx context.Context, assignmentID, reviewerID uuid.UUID, amountMinor int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `
		UPDATE reviewer.review_assignments SET base_paid = true
		WHERE id = $1 AND base_paid = false`, assignmentID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return tx.Commit(ctx) // already paid; no double accrual
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO reviewer.reviewer_ledger (reviewer_id, assignment_id, entry_type, amount_minor)
		VALUES ($1, $2, 'base', $3)`, reviewerID, assignmentID, amountMinor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GradeTarget is a completed assignment awaiting engagement backtest.
type GradeTarget struct {
	AssignmentID uuid.UUID
	ContentID    uuid.UUID
	ReviewerID   uuid.UUID
	Decision     string
}

// AssignmentsToGrade returns completed primary assignments not yet graded whose
// decision is engagement-gradable (approve/reject) and mature enough that the
// content has had exposure time.
func (s *Store) AssignmentsToGrade(ctx context.Context, maturity time.Duration, limit int) ([]GradeTarget, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, content_id, reviewer_id, decision
		FROM reviewer.review_assignments
		WHERE status = 'completed' AND graded = false AND kind = 'primary'
		  AND decision IN ('approve','reject')
		  AND decided_at < now() - $1::interval
		ORDER BY decided_at ASC
		LIMIT $2`, maturity.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GradeTarget
	for rows.Next() {
		var t GradeTarget
		var dec *string
		if err := rows.Scan(&t.AssignmentID, &t.ContentID, &t.ReviewerID, &dec); err != nil {
			return nil, err
		}
		if dec != nil {
			t.Decision = *dec
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EngagementPercentile computes the content's cohort-normalized engagement
// percentile (0..1) within its content_type over the recent window, reading the
// shared analytics rollup (analytics.content_daily_summary, same app DB). ok is
// false when the content has no analytics rows yet (not mature → grade later).
func (s *Store) EngagementPercentile(ctx context.Context, contentID uuid.UUID, windowDays int) (float64, bool, error) {
	var pctile float64
	err := s.db.QueryRow(ctx, `
		WITH cohort AS (
			SELECT content_id, content_type, SUM(views_display) AS views
			FROM analytics.content_daily_summary
			WHERE day_bucket >= CURRENT_DATE - ($1 || ' days')::interval
			GROUP BY content_id, content_type
		),
		ranked AS (
			SELECT content_id,
			       PERCENT_RANK() OVER (PARTITION BY content_type ORDER BY views) AS pctile
			FROM cohort
		)
		SELECT pctile FROM ranked WHERE content_id = $2`, windowDays, contentID).Scan(&pctile)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return pctile, true, nil
}

// RecordGrade atomically: writes the content outcome, marks the assignment
// graded, updates the reviewer's EWMA accuracy + tier, and appends a bonus
// ledger row when earned. Returns the new accuracy and tier.
func (s *Store) RecordGrade(ctx context.Context, t GradeTarget, pctile, score, ewmaAlpha float64, bonusMinor int64) (float64, string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO reviewer.content_review_outcome (content_id, engagement_pctile, finalized_at)
		VALUES ($1, $2, now())
		ON CONFLICT (content_id) DO UPDATE
			SET engagement_pctile = EXCLUDED.engagement_pctile, finalized_at = now()`,
		t.ContentID, pctile); err != nil {
		return 0, "", err
	}

	ct, err := tx.Exec(ctx, `
		UPDATE reviewer.review_assignments SET graded = true
		WHERE id = $1 AND graded = false`, t.AssignmentID)
	if err != nil {
		return 0, "", err
	}
	if ct.RowsAffected() == 0 {
		return 0, "", tx.Commit(ctx) // already graded concurrently
	}

	var newAccuracy float64
	if err := tx.QueryRow(ctx, `
		UPDATE reviewer.reviewers
		SET reviewer_accuracy = round(($2 * $3 + reviewer_accuracy * (1 - $2))::numeric, 4),
		    updated_at = now()
		WHERE id = $1
		RETURNING reviewer_accuracy::float8`,
		t.ReviewerID, ewmaAlpha, score).Scan(&newAccuracy); err != nil {
		return 0, "", err
	}

	if bonusMinor > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO reviewer.reviewer_ledger (reviewer_id, assignment_id, entry_type, amount_minor)
			VALUES ($1, $2, 'bonus', $3)`, t.ReviewerID, t.AssignmentID, bonusMinor); err != nil {
			return 0, "", err
		}
	}

	var gradedCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM reviewer.review_assignments
		WHERE reviewer_id = $1 AND graded = true`, t.ReviewerID).Scan(&gradedCount); err != nil {
		return 0, "", err
	}
	newTier := deriveTier(newAccuracy, gradedCount)
	if _, err := tx.Exec(ctx, `
		UPDATE reviewer.reviewers
		SET tier = $2,
		    status = CASE WHEN $2 <> 'probation' AND status = 'probation' THEN 'active' ELSE status END,
		    updated_at = now()
		WHERE id = $1`, t.ReviewerID, newTier); err != nil {
		return 0, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, "", err
	}
	return newAccuracy, newTier, nil
}

// deriveTier maps accuracy + graded volume to a reviewer tier.
func deriveTier(accuracy float64, gradedCount int) string {
	switch {
	case gradedCount < 5:
		return "probation"
	case accuracy >= 0.85 && gradedCount >= 20:
		return "senior"
	case accuracy >= 0.70:
		return "trusted"
	default:
		return "probation"
	}
}

// ExpireOverdue marks overdue active assignments expired and re-opens their
// queue items for reassignment. Returns the number expired.
func (s *Store) ExpireOverdue(ctx context.Context) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		UPDATE reviewer.review_assignments SET status = 'expired'
		WHERE status IN ('assigned','in_progress') AND expires_at < now()
		RETURNING content_id`)
	if err != nil {
		return 0, err
	}
	var contentIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		contentIDs = append(contentIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range contentIDs {
		if _, err := tx.Exec(ctx,
			`UPDATE reviewer.review_queue SET claimed = false WHERE content_id = $1`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(contentIDs), nil
}
