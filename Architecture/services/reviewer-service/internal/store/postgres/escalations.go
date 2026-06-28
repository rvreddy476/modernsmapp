package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Escalation is a reviewer's "I can't approve this" hand-off to a super-admin,
// carrying the reviewer's comments. The admin resolves it (reject | request_edits
// | approve).
type Escalation struct {
	ID               uuid.UUID  `json:"id"`
	ContentID        uuid.UUID  `json:"content_id"`
	CreatorID        uuid.UUID  `json:"creator_id"`
	ReviewerID       uuid.UUID  `json:"reviewer_id"`
	AssignmentID     *uuid.UUID `json:"assignment_id,omitempty"`
	ReviewerComments string     `json:"reviewer_comments"`
	Status           string     `json:"status"`
	AdminDecision    *string    `json:"admin_decision,omitempty"`
	AdminNotes       *string    `json:"admin_notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
}

const escalationCols = `id, content_id, creator_id, reviewer_id, assignment_id,
	reviewer_comments, status, admin_decision, admin_notes, created_at, resolved_at`

func scanEscalation(row interface {
	Scan(dest ...any) error
}) (*Escalation, error) {
	var e Escalation
	if err := row.Scan(&e.ID, &e.ContentID, &e.CreatorID, &e.ReviewerID, &e.AssignmentID,
		&e.ReviewerComments, &e.Status, &e.AdminDecision, &e.AdminNotes,
		&e.CreatedAt, &e.ResolvedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// CreateEscalation records a reviewer escalation (open) for super-admin review.
func (s *Store) CreateEscalation(ctx context.Context, contentID, creatorID, reviewerID uuid.UUID, assignmentID uuid.UUID, comments string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO reviewer.escalations (content_id, creator_id, reviewer_id, assignment_id, reviewer_comments)
		VALUES ($1, $2, $3, $4, $5)`,
		contentID, creatorID, reviewerID, assignmentID, comments)
	return err
}

// ListOpenEscalations returns the super-admin queue (oldest first).
func (s *Store) ListOpenEscalations(ctx context.Context, limit int) ([]Escalation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+escalationCols+`
		FROM reviewer.escalations WHERE status = 'open'
		ORDER BY created_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Escalation
	for rows.Next() {
		e, err := scanEscalation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ResolveEscalation records the super-admin's decision. Returns the resolved
// row (for the post-service status flip). Idempotent: only resolves 'open'.
func (s *Store) ResolveEscalation(ctx context.Context, id, adminID uuid.UUID, decision, notes string) (*Escalation, error) {
	return scanEscalation(s.db.QueryRow(ctx, `
		UPDATE reviewer.escalations
		SET status = 'resolved', admin_decision = $3, admin_notes = $4,
		    resolved_by = $2, resolved_at = now()
		WHERE id = $1 AND status = 'open'
		RETURNING `+escalationCols, id, adminID, decision, notes))
}

// LatestEscalationForContent returns the most recent escalation for a creator's
// own content (creator-facing "needs changes" feedback).
func (s *Store) LatestEscalationForContent(ctx context.Context, creatorID, contentID uuid.UUID) (*Escalation, error) {
	return scanEscalation(s.db.QueryRow(ctx, `
		SELECT `+escalationCols+`
		FROM reviewer.escalations
		WHERE content_id = $1 AND creator_id = $2
		ORDER BY created_at DESC LIMIT 1`, contentID, creatorID))
}
