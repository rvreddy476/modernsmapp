package store

import (
	"context"
	"fmt"
	"time"

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

func (s *Store) UpdateReportStatus(ctx context.Context, reportID uuid.UUID, status string, reviewedBy uuid.UUID) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `UPDATE moderation_reports SET status = $2, reviewed_by = $3, resolved_at = $4 WHERE id = $1`,
		reportID, status, reviewedBy, now)
	return err
}

func (s *Store) CreateModerationAction(ctx context.Context, reportID *uuid.UUID, actorID uuid.UUID, actionType, targetType string, targetID uuid.UUID, reason string) (*ModerationAction, error) {
	a := &ModerationAction{}
	err := s.db.QueryRow(ctx, `
		INSERT INTO moderation_actions (report_id, actor_id, action_type, target_type, target_id, reason)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, report_id, actor_id, action_type, target_type, target_id, reason, created_at`,
		reportID, actorID, actionType, targetType, targetID, reason,
	).Scan(&a.ID, &a.ReportID, &a.ActorID, &a.ActionType, &a.TargetType, &a.TargetID, &a.Reason, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create moderation action: %w", err)
	}
	return a, nil
}

func (s *Store) ListModerationActions(ctx context.Context, limit, offset int) ([]ModerationAction, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, report_id, actor_id, action_type, target_type, target_id, reason, created_at
		FROM moderation_actions ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ModerationAction
	for rows.Next() {
		var a ModerationAction
		if err := rows.Scan(&a.ID, &a.ReportID, &a.ActorID, &a.ActionType, &a.TargetType, &a.TargetID, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, nil
}

func (s *Store) HideContent(ctx context.Context, targetType string, targetID, actorID uuid.UUID, reason string) error {
	switch targetType {
	case "question":
		_, _ = s.db.Exec(ctx, `UPDATE questions SET deleted_at = now(), status = 'deleted' WHERE id = $1`, targetID)
	case "answer":
		_, _ = s.db.Exec(ctx, `UPDATE answers SET deleted_at = now() WHERE id = $1`, targetID)
	case "comment":
		_, _ = s.db.Exec(ctx, `UPDATE answer_comments SET deleted_at = now() WHERE id = $1`, targetID)
	}
	_, _ = s.CreateModerationAction(ctx, nil, actorID, "hide", targetType, targetID, reason)
	return nil
}

func (s *Store) LockQuestion(ctx context.Context, questionID, actorID uuid.UUID, reason string) error {
	_, _ = s.db.Exec(ctx, `UPDATE questions SET status = 'closed', closed_by = $2, closed_reason = $3, updated_at = now() WHERE id = $1`, questionID, actorID, reason)
	_, _ = s.CreateModerationAction(ctx, nil, actorID, "lock", "question", questionID, reason)
	return nil
}

func (s *Store) MergeQuestion(ctx context.Context, questionID, mergeIntoID, actorID uuid.UUID) error {
	_, _ = s.db.Exec(ctx, `UPDATE questions SET status = 'merged', merged_into_id = $2, updated_at = now() WHERE id = $1`, questionID, mergeIntoID)
	_, _ = s.CreateModerationAction(ctx, nil, actorID, "merge", "question", questionID, fmt.Sprintf("merged into %s", mergeIntoID))
	return nil
}

func (s *Store) MarkDuplicate(ctx context.Context, questionID, duplicateOfID, markedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `INSERT INTO question_duplicates (question_id, duplicate_of_id, marked_by) VALUES ($1, $2, $3)`,
		questionID, duplicateOfID, markedBy)
	return err
}

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
