package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PrimaryDecision is the completed primary review for a piece of content, used
// to compare a secondary (audit/shadow) review against.
type PrimaryDecision struct {
	AssignmentID uuid.UUID
	ReviewerID   uuid.UUID
	Decision     string
}

// FlagTarget identifies an assignment + its reviewer for a penalty.
type FlagTarget struct {
	AssignmentID uuid.UUID
	ReviewerID   uuid.UUID
}

// RingPair is a reviewer↔creator pair with a suspiciously high approval count.
type RingPair struct {
	ReviewerID uuid.UUID
	CreatorID  uuid.UUID
	Approvals  int
}

// PenaltyParams drives ApplyPenalty.
type PenaltyParams struct {
	ReviewerID       uuid.UUID
	AssignmentID     *uuid.UUID // nil for reviewer-level (e.g. ring) flags
	FlagType         string
	Severity         int
	Details          string
	Clawback         bool    // reverse base+bonus accrued for the assignment
	PenaltyScore     float64 // EWMA target for the wrong call (0 = fully wrong)
	EWMAAlpha        float64
	SuspendThreshold int // flags in the last 30d that trigger auto-suspension
}

// PromotableContent returns content that a human APPROVED and whose graded
// engagement percentile is healthy — ready to promote from 'staged' to full
// distribution (Phase 4b). post-service no-ops if the post isn't actually staged.
func (s *Store) PromotableContent(ctx context.Context, minPctile float64, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.content_id
		FROM reviewer.review_assignments a
		JOIN reviewer.content_review_outcome o ON o.content_id = a.content_id
		WHERE a.kind = 'primary' AND a.decision = 'approve' AND a.graded = true
		  AND o.engagement_pctile >= $1
		ORDER BY o.finalized_at DESC
		LIMIT $2`, minPctile, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PrimaryDecisionForContent returns the completed primary review for content.
func (s *Store) PrimaryDecisionForContent(ctx context.Context, contentID uuid.UUID) (*PrimaryDecision, error) {
	var d PrimaryDecision
	var dec *string
	err := s.db.QueryRow(ctx, `
		SELECT id, reviewer_id, decision
		FROM reviewer.review_assignments
		WHERE content_id = $1 AND kind = 'primary' AND status = 'completed'
		ORDER BY decided_at DESC LIMIT 1`, contentID).Scan(&d.AssignmentID, &d.ReviewerID, &dec)
	if err != nil {
		return nil, err
	}
	if dec != nil {
		d.Decision = *dec
	}
	return &d, nil
}

// SecondaryCandidates returns online, non-suspended reviewers (excluding one)
// ordered by accuracy then random, for second-review assignment. Graph
// (relationship) eligibility is checked by the caller.
func (s *Store) SecondaryCandidates(ctx context.Context, exclude uuid.UUID, limit int) ([]Reviewer, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+reviewerCols+`
		FROM reviewer.reviewers
		WHERE id <> $1 AND status IN ('active','probation') AND is_online = true
		ORDER BY reviewer_accuracy DESC, random()
		LIMIT $2`, exclude, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reviewer
	for rows.Next() {
		var r Reviewer
		if err := rows.Scan(&r.ID, &r.UserID, &r.Status, &r.Tier, &r.Languages, &r.Region,
			&r.ReviewerAccuracy, &r.MaxConcurrent, &r.KYCVerified, &r.IsOnline); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertSecondaryAssignment creates an audit/shadow assignment directly (no
// queue claim — the content already has a completed primary review).
func (s *Store) InsertSecondaryAssignment(ctx context.Context, contentID, creatorID, reviewerID uuid.UUID, contentSeconds int, kind string, ttl time.Duration) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
		INSERT INTO reviewer.review_assignments
			(content_id, creator_id, reviewer_id, kind, content_seconds, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'assigned', now() + $6::interval)
		RETURNING id`,
		contentID, creatorID, reviewerID, kind, contentSeconds, ttl.String()).Scan(&id)
	return id, err
}

// ApplyPenalty records a flag and (optionally) claws back pay, dings the EWMA
// accuracy toward PenaltyScore, and auto-suspends when recent flags pile up.
// Idempotent per (assignment_id, flag_type). Returns whether the reviewer is
// now suspended.
func (s *Store) ApplyPenalty(ctx context.Context, p PenaltyParams) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if p.AssignmentID != nil {
		ct, err := tx.Exec(ctx, `
			INSERT INTO reviewer.reviewer_flags (reviewer_id, assignment_id, flag_type, severity, details)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (assignment_id, flag_type) WHERE assignment_id IS NOT NULL DO NOTHING`,
			p.ReviewerID, *p.AssignmentID, p.FlagType, p.Severity, p.Details)
		if err != nil {
			return false, err
		}
		if ct.RowsAffected() == 0 {
			return false, tx.Commit(ctx) // already flagged — no double penalty
		}
	} else {
		if _, err := tx.Exec(ctx, `
			INSERT INTO reviewer.reviewer_flags (reviewer_id, flag_type, severity, details)
			VALUES ($1, $2, $3, $4)`, p.ReviewerID, p.FlagType, p.Severity, p.Details); err != nil {
			return false, err
		}
	}

	if p.Clawback && p.AssignmentID != nil {
		var pos int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_minor), 0) FROM reviewer.reviewer_ledger
			WHERE assignment_id = $1 AND entry_type IN ('base','bonus') AND amount_minor > 0`,
			*p.AssignmentID).Scan(&pos); err != nil {
			return false, err
		}
		if pos > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO reviewer.reviewer_ledger (reviewer_id, assignment_id, entry_type, amount_minor)
				VALUES ($1, $2, 'clawback', $3)`, p.ReviewerID, *p.AssignmentID, -pos); err != nil {
				return false, err
			}
		}
	}

	if p.EWMAAlpha > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE reviewer.reviewers
			SET reviewer_accuracy = round(($2 * $3 + reviewer_accuracy * (1 - $2))::numeric, 4),
			    updated_at = now()
			WHERE id = $1`, p.ReviewerID, p.EWMAAlpha, p.PenaltyScore); err != nil {
			return false, err
		}
	}

	suspended := false
	if p.SuspendThreshold > 0 {
		var flagCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM reviewer.reviewer_flags
			WHERE reviewer_id = $1 AND created_at > now() - interval '30 days'`,
			p.ReviewerID).Scan(&flagCount); err != nil {
			return false, err
		}
		if flagCount >= p.SuspendThreshold {
			ct, err := tx.Exec(ctx, `
				UPDATE reviewer.reviewers SET status = 'suspended', updated_at = now()
				WHERE id = $1 AND status <> 'suspended'`, p.ReviewerID)
			if err != nil {
				return false, err
			}
			suspended = ct.RowsAffected() > 0
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return suspended, nil
}

// RubberstampTargets returns recently-completed primary approvals where the
// reviewer watched far less than the content length (rubber-stamping), excluding
// ones already flagged.
func (s *Store) RubberstampTargets(ctx context.Context, window time.Duration, minWatchRatio float64, limit int) ([]FlagTarget, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.reviewer_id
		FROM reviewer.review_assignments a
		WHERE a.status = 'completed' AND a.kind = 'primary' AND a.decision = 'approve'
		  AND a.content_seconds > 0
		  AND a.watched_seconds < a.content_seconds * $2
		  AND a.decided_at > now() - $1::interval
		  AND NOT EXISTS (
		      SELECT 1 FROM reviewer.reviewer_flags f
		      WHERE f.assignment_id = a.id AND f.flag_type = 'anomaly_rubberstamp')
		ORDER BY a.decided_at DESC
		LIMIT $3`, window.String(), minWatchRatio, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlagTarget
	for rows.Next() {
		var t FlagTarget
		if err := rows.Scan(&t.AssignmentID, &t.ReviewerID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// HighApprovalReviewers returns reviewers whose approval rate over their recent
// completed primary reviews exceeds the threshold (rubber-stamp-at-scale).
func (s *Store) HighApprovalReviewers(ctx context.Context, minDecisions int, threshold float64) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT reviewer_id
		FROM reviewer.review_assignments
		WHERE status = 'completed' AND kind = 'primary'
		  AND decision IN ('approve','reject','flag_unsafe')
		  AND decided_at > now() - interval '7 days'
		GROUP BY reviewer_id
		HAVING count(*) >= $1
		   AND avg((decision = 'approve')::int)::float8 >= $2
		   AND NOT EXISTS (
		       SELECT 1 FROM reviewer.reviewer_flags f
		       WHERE f.reviewer_id = reviewer.review_assignments.reviewer_id
		         AND f.flag_type = 'anomaly_approval_rate'
		         AND f.created_at > now() - interval '24 hours')`, minDecisions, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RingSuspects returns reviewer↔creator pairs with an outsized number of
// approvals in the window (collusion ring signal), excluding pairs already
// flagged recently.
func (s *Store) RingSuspects(ctx context.Context, windowDays, minApprovals int) ([]RingPair, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.reviewer_id, a.creator_id, count(*) AS approvals
		FROM reviewer.review_assignments a
		WHERE a.status = 'completed' AND a.kind = 'primary' AND a.decision = 'approve'
		  AND a.decided_at > now() - ($1 || ' days')::interval
		GROUP BY a.reviewer_id, a.creator_id
		HAVING count(*) >= $2
		   AND NOT EXISTS (
		       SELECT 1 FROM reviewer.reviewer_flags f
		       WHERE f.reviewer_id = a.reviewer_id AND f.flag_type = 'ring_suspect'
		         AND f.details = a.creator_id::text
		         AND f.created_at > now() - interval '7 days')`, windowDays, minApprovals)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RingPair
	for rows.Next() {
		var p RingPair
		if err := rows.Scan(&p.ReviewerID, &p.CreatorID, &p.Approvals); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
