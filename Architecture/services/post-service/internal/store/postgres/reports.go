package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ContentReport represents a user-submitted report for content (post, comment, reel, video).
type ContentReport struct {
	ID           uuid.UUID `json:"id"`
	ReporterID   uuid.UUID `json:"reporter_id"`
	TargetType   string    `json:"target_type"`   // "post", "comment", "reel", "video"
	TargetID     uuid.UUID `json:"target_id"`
	Reason       string    `json:"reason"`        // "spam", "harassment", "hate_speech", "violence", "nudity", "misinformation", "other"
	Description  string    `json:"description"`
	Status       string    `json:"status"`        // "pending", "reviewed", "resolved", "dismissed"
	ReviewerID   *string   `json:"reviewer_id,omitempty"`
	ReviewNote   *string   `json:"review_note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
}

// InsertContentReport inserts a new content report.
func (s *Store) InsertContentReport(ctx context.Context, r *ContentReport) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO content_reports (id, reporter_id, target_type, target_id, reason, description, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', NOW())
	`, r.ID, r.ReporterID, r.TargetType, r.TargetID, r.Reason, r.Description)
	return err
}

// GetContentReports returns reports filtered by status, for admin dashboard.
func (s *Store) GetContentReports(ctx context.Context, status string, limit, offset int) ([]ContentReport, error) {
	query := `
		SELECT id, reporter_id, target_type, target_id, reason, description, status,
		       reviewer_id, review_note, created_at, reviewed_at
		FROM content_reports
	`
	var args []interface{}

	if status != "" {
		query += ` WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = append(args, status, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []ContentReport
	for rows.Next() {
		var r ContentReport
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID,
			&r.Reason, &r.Description, &r.Status, &r.ReviewerID, &r.ReviewNote,
			&r.CreatedAt, &r.ReviewedAt); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, nil
}

// UpdateReportStatus updates the status and review note of a report (for admin review).
func (s *Store) UpdateReportStatus(ctx context.Context, reportID uuid.UUID, status, reviewerID, reviewNote string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE content_reports
		SET status = $2, reviewer_id = $3, review_note = $4, reviewed_at = NOW()
		WHERE id = $1
	`, reportID, status, reviewerID, reviewNote)
	return err
}

// GetReportCountByTarget returns the number of reports for a specific target.
func (s *Store) GetReportCountByTarget(ctx context.Context, targetType string, targetID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM content_reports WHERE target_type = $1 AND target_id = $2
	`, targetType, targetID).Scan(&count)
	return count, err
}
