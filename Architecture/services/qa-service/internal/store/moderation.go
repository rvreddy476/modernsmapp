package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// openReportPredicateSQL is what "open" means for a report: the moderators'
// queue (AdminDecideReport decides only these, AdminStats counts only
// these). The partial unique index uq_moderation_reports_open_once
// (migration 011) has exactly this predicate, and createReportSQL's ON
// CONFLICT repeats it so Postgres infers that index.
const openReportPredicateSQL = `status IN ('pending', 'reviewed')`

const reportColumns = `id, reporter_id, target_type, target_id, reason, details, status, reviewed_by, resolved_at, created_at`

// createReportSQL inserts a report unless the reporter already has an open
// one on the same target; a repeat (or the loser of a race between two
// first reports) inserts nothing and returns no row.
const createReportSQL = `
		INSERT INTO moderation_reports (reporter_id, target_type, target_id, reason, details)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (reporter_id, target_type, target_id) WHERE ` + openReportPredicateSQL + ` DO NOTHING
		RETURNING ` + reportColumns

const openReportSQL = `
		SELECT ` + reportColumns + `
		FROM moderation_reports
		WHERE reporter_id = $1 AND target_type = $2 AND target_id = $3 AND ` + openReportPredicateSQL

func scanReport(row pgx.Row) (*ModerationReport, error) {
	r := &ModerationReport{}
	if err := row.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details,
		&r.Status, &r.ReviewedBy, &r.ResolvedAt, &r.CreatedAt); err != nil {
		return nil, err
	}
	return r, nil
}

// OpenReport returns the reporter's open report on the target, or nil when
// there is none.
func (s *Store) OpenReport(ctx context.Context, reporterID uuid.UUID, targetType string, targetID uuid.UUID) (*ModerationReport, error) {
	r, err := scanReport(s.db.QueryRow(ctx, openReportSQL, reporterID, targetType, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open report: %w", err)
	}
	return r, nil
}

// CreateReport files one report: one open report per (reporter, target_type,
// target_id), enforced by uq_moderation_reports_open_once. A repeat while
// the first is open returns that report with created false and writes
// nothing; concurrent first reports insert one row and all return it.
func (s *Store) CreateReport(ctx context.Context, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reason, details string) (*ModerationReport, bool, error) {
	// A conflict means an open report existed at insert time; it can be
	// closed by a moderator before the read below sees it, and then the
	// next insert succeeds. Two rounds cover that; a third miss is a 500.
	for attempt := 0; attempt < 3; attempt++ {
		r, err := scanReport(s.db.QueryRow(ctx, createReportSQL, reporterID, targetType, targetID, reason, details))
		if err == nil {
			return r, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("create report: %w", err)
		}
		existing, err := s.OpenReport(ctx, reporterID, targetType, targetID)
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			return existing, false, nil
		}
	}
	return nil, false, fmt.Errorf("create report: the open report kept changing")
}

func (s *Store) GetReport(ctx context.Context, reportID uuid.UUID) (*ModerationReport, error) {
	r, err := scanReport(s.db.QueryRow(ctx, `SELECT `+reportColumns+` FROM moderation_reports WHERE id = $1`, reportID))
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
