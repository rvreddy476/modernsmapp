package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ContentReport represents a user-submitted report for content (post, comment, reel, video).
type ContentReport struct {
	ID          uuid.UUID  `json:"id"`
	ReporterID  uuid.UUID  `json:"reporter_id"`
	TargetType  string     `json:"target_type"` // "post", "comment", "reel", "video"
	TargetID    uuid.UUID  `json:"target_id"`
	Reason      string     `json:"reason"` // "spam", "harassment", "hate_speech", "violence", "nudity", "misinformation", "other"
	Description string     `json:"description"`
	Status      string     `json:"status"` // "pending", "reviewed", "resolved", "dismissed"
	ReviewerID  *string    `json:"reviewer_id,omitempty"`
	ReviewNote  *string    `json:"review_note,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
}

// ErrReportNotFound is returned when a report id names no content_reports row.
var ErrReportNotFound = errors.New("content report not found")

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

const contentReportColumns = `id, reporter_id, target_type, target_id, reason, description, status,
	       reviewer_id, review_note, created_at, reviewed_at`

func scanContentReport(row pgx.Row) (ContentReport, error) {
	var r ContentReport
	err := row.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID,
		&r.Reason, &r.Description, &r.Status, &r.ReviewerID, &r.ReviewNote,
		&r.CreatedAt, &r.ReviewedAt)
	return r, err
}

// GetContentReports returns reports filtered by status, for admin dashboard.
func (s *Store) GetContentReports(ctx context.Context, status string, limit, offset int) ([]ContentReport, error) {
	return s.ListContentReports(ctx, status, nil, limit, offset)
}

// ListContentReports returns reports newest first, optionally filtered by
// status and by target type. A nil targetTypes means every type; an empty
// non-nil slice matches nothing (the caller may see no report kind).
func (s *Store) ListContentReports(ctx context.Context, status string, targetTypes []string, limit, offset int) ([]ContentReport, error) {
	if targetTypes != nil && len(targetTypes) == 0 {
		return []ContentReport{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+contentReportColumns+`
		FROM content_reports
		WHERE ($1::text = '' OR status = $1::text)
		  AND ($2::text[] IS NULL OR target_type = ANY($2::text[]))
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`, status, targetTypes, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []ContentReport
	for rows.Next() {
		r, err := scanContentReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// GetContentReport returns one report (ErrReportNotFound when absent).
func (s *Store) GetContentReport(ctx context.Context, reportID uuid.UUID) (*ContentReport, error) {
	r, err := scanContentReport(s.db.QueryRow(ctx, `SELECT `+contentReportColumns+` FROM content_reports WHERE id = $1`, reportID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReportNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateReportStatus records a moderator's review of a report and appends one
// post_admin_audit row (report.review) in the same transaction. actor is the
// acting human and is required; reviewer_id stores the same id. A missing
// report is ErrReportNotFound and writes nothing.
func (s *Store) UpdateReportStatus(ctx context.Context, reportID uuid.UUID, status string, actor uuid.UUID, reviewNote string) error {
	if actor == uuid.Nil {
		return ErrAdminAuditActor
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var previous string
	if err := tx.QueryRow(ctx, `SELECT status FROM content_reports WHERE id = $1 FOR UPDATE`, reportID).Scan(&previous); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReportNotFound
		}
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE content_reports
		SET status = $2, reviewer_id = $3, review_note = $4, reviewed_at = NOW()
		WHERE id = $1
	`, reportID, status, actor.String(), reviewNote); err != nil {
		return err
	}
	if err := insertAdminAudit(ctx, tx, actor, "report.review", "content_report", reportID, previous, status, reviewNote); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetReportCountByTarget returns the number of reports for a specific target.
func (s *Store) GetReportCountByTarget(ctx context.Context, targetType string, targetID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM content_reports WHERE target_type = $1 AND target_id = $2
	`, targetType, targetID).Scan(&count)
	return count, err
}
