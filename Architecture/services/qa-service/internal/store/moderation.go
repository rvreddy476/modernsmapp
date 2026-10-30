package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) CreateReport(ctx context.Context, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reason, details string) (*ModerationReport, error) {
	r := &ModerationReport{}
	err := s.db.QueryRow(ctx, `
		INSERT INTO moderation_reports (reporter_id, target_type, target_id, reason, details)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, reporter_id, target_type, target_id, reason, details, status, reviewed_by, resolved_at, created_at`,
		reporterID, targetType, targetID, reason, details,
	).Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details,
		&r.Status, &r.ReviewedBy, &r.ResolvedAt, &r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	return r, nil
}

func (s *Store) GetReport(ctx context.Context, reportID uuid.UUID) (*ModerationReport, error) {
	r := &ModerationReport{}
	err := s.db.QueryRow(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, details, status, reviewed_by, resolved_at, created_at
		FROM moderation_reports WHERE id = $1`, reportID,
	).Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details,
		&r.Status, &r.ReviewedBy, &r.ResolvedAt, &r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get report: %w", err)
	}
	return r, nil
}

func (s *Store) ListReports(ctx context.Context, status string, limit, offset int) ([]ModerationReport, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT id, reporter_id, target_type, target_id, reason, details, status, reviewed_by, resolved_at, created_at
	          FROM moderation_reports`
	args := []any{}
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
	var results []ModerationReport
	for rows.Next() {
		var r ModerationReport
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details,
			&r.Status, &r.ReviewedBy, &r.ResolvedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// The legacy /v1/qa/admin writes (UpdateReportStatus, CreateModerationAction,
// HideContent, LockQuestion, MergeQuestion, MarkDuplicate) are gone with
// their routes (audit M1): every moderation write is admin_moderation.go,
// behind an admin-service token.

func (s *Store) GetDuplicates(ctx context.Context, questionID uuid.UUID) ([]QuestionSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q JOIN question_duplicates qd ON q.id = qd.question_id
		WHERE qd.duplicate_of_id = $1 AND q.deleted_at IS NULL`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}
