package store

// Admin console (Wave 2 — Content apps, Q&A): the moderation writes behind
// the token-only family /v1/qa/internal/admin/* (http/admin_token.go).
//
// The LEGACY /v1/qa/admin/* writes (moderation.go) run the change and the
// moderation_actions insert as separate statements and ignore both errors, so
// a change can land without its audit row (or the reverse) and a missing
// target still answers 200. These methods do each change and its one
// moderation_actions row in ONE transaction, refuse a missing or already
// moderated target, and take the actor from the caller (the signed act claim
// of an admin-service token, never a header). The legacy methods are left
// as they were.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Admin write errors. The HTTP layer maps them to 404, 409 and 400.
var (
	ErrAdminNotFound = errors.New("not found")
	ErrAdminConflict = errors.New("conflict")
	ErrAdminInvalid  = errors.New("invalid")
)

// AdminActionSource marks moderation_actions rows written by this path.
const AdminActionSource = "admin-service"

// Action types written by the admin console path. hide, lock and merge match
// the legacy rows so history reads the same.
const (
	ActionHide          = "hide"
	ActionLock          = "lock"
	ActionMerge         = "merge"
	ActionMarkDuplicate = "mark_duplicate"
	ActionReportResolve = "report_resolve"
	ActionReportDismiss = "report_dismiss"
)

func adminErr(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, args...))
}

// insertModerationActionTx appends the one audit row for an admin change
// inside the caller's transaction.
func insertModerationActionTx(ctx context.Context, tx pgx.Tx, reportID *uuid.UUID, actor uuid.UUID, actionType, targetType string, targetID uuid.UUID, reason string, meta map[string]any) (*ModerationAction, error) {
	if actor == uuid.Nil {
		return nil, fmt.Errorf("moderation action %s: actor is required", actionType)
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["source"] = AdminActionSource
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("moderation action %s: metadata: %w", actionType, err)
	}
	a := &ModerationAction{}
	err = tx.QueryRow(ctx, `
		INSERT INTO moderation_actions (report_id, actor_id, action_type, target_type, target_id, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		RETURNING id, report_id, actor_id, action_type, target_type, target_id, reason, created_at`,
		reportID, actor, actionType, targetType, targetID, reason, string(metaJSON),
	).Scan(&a.ID, &a.ReportID, &a.ActorID, &a.ActionType, &a.TargetType, &a.TargetID, &a.Reason, &a.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, adminErr(ErrAdminInvalid, "report_id does not name a report")
		}
		return nil, fmt.Errorf("moderation action %s: %w", actionType, err)
	}
	return a, nil
}

// withAdminTx runs fn in one transaction and commits only if fn succeeds.
func (s *Store) withAdminTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminDecideReport resolves or dismisses one open report (pending or
// reviewed) and records who did it. A closed or missing report writes nothing.
func (s *Store) AdminDecideReport(ctx context.Context, actor, reportID uuid.UUID, status, reason string) (*ModerationAction, error) {
	actionType := ActionReportResolve
	switch status {
	case "resolved":
	case "dismissed":
		actionType = ActionReportDismiss
	default:
		return nil, adminErr(ErrAdminInvalid, "unknown report decision %q", status)
	}
	var out *ModerationAction
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		var current, targetType string
		var targetID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT status, target_type, target_id FROM moderation_reports WHERE id = $1 FOR UPDATE`, reportID).
			Scan(&current, &targetType, &targetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return adminErr(ErrAdminNotFound, "report not found")
		}
		if err != nil {
			return err
		}
		if current != "pending" && current != "reviewed" {
			return adminErr(ErrAdminConflict, "report is already %s", current)
		}
		if _, err := tx.Exec(ctx, `UPDATE moderation_reports SET status = $2, reviewed_by = $3, resolved_at = now() WHERE id = $1`,
			reportID, status, actor); err != nil {
			return err
		}
		rid := reportID
		out, err = insertModerationActionTx(ctx, tx, &rid, actor, actionType, targetType, targetID, reason, nil)
		return err
	})
	return out, err
}

// AdminHideContent hides one question, answer or comment that is not already
// hidden, with the counters a user delete would adjust, and records it.
func (s *Store) AdminHideContent(ctx context.Context, actor uuid.UUID, targetType string, targetID uuid.UUID, reportID *uuid.UUID, reason string) (*ModerationAction, error) {
	var out *ModerationAction
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		var deletedAt *time.Time
		switch targetType {
		case "question":
			err := tx.QueryRow(ctx, `SELECT deleted_at FROM questions WHERE id = $1 FOR UPDATE`, targetID).Scan(&deletedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return adminErr(ErrAdminNotFound, "question not found")
			}
			if err != nil {
				return err
			}
			if deletedAt != nil {
				return adminErr(ErrAdminConflict, "question is already hidden")
			}
			if _, err := tx.Exec(ctx, `UPDATE questions SET deleted_at = now(), status = 'deleted' WHERE id = $1`, targetID); err != nil {
				return err
			}
		case "answer":
			var questionID uuid.UUID
			var communityID *uuid.UUID
			err := tx.QueryRow(ctx, `
				SELECT a.deleted_at, a.question_id, q.community_id
				FROM answers a JOIN questions q ON q.id = a.question_id
				WHERE a.id = $1 FOR UPDATE OF a`, targetID).Scan(&deletedAt, &questionID, &communityID)
			if errors.Is(err, pgx.ErrNoRows) {
				return adminErr(ErrAdminNotFound, "answer not found")
			}
			if err != nil {
				return err
			}
			if deletedAt != nil {
				return adminErr(ErrAdminConflict, "answer is already hidden")
			}
			if _, err := tx.Exec(ctx, `UPDATE answers SET deleted_at = now() WHERE id = $1`, targetID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE questions SET answer_count = GREATEST(answer_count - 1, 0), updated_at = now() WHERE id = $1`, questionID); err != nil {
				return err
			}
			if communityID != nil {
				if err := s.syncCommunityAnswerStatsTx(ctx, tx, *communityID); err != nil {
					return err
				}
			}
		case "comment":
			var answerID uuid.UUID
			err := tx.QueryRow(ctx, `SELECT deleted_at, answer_id FROM answer_comments WHERE id = $1 FOR UPDATE`, targetID).Scan(&deletedAt, &answerID)
			if errors.Is(err, pgx.ErrNoRows) {
				return adminErr(ErrAdminNotFound, "comment not found")
			}
			if err != nil {
				return err
			}
			if deletedAt != nil {
				return adminErr(ErrAdminConflict, "comment is already hidden")
			}
			if _, err := tx.Exec(ctx, `UPDATE answer_comments SET deleted_at = now() WHERE id = $1`, targetID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE answers SET comment_count = GREATEST(comment_count - 1, 0) WHERE id = $1`, answerID); err != nil {
				return err
			}
		default:
			return adminErr(ErrAdminInvalid, "unknown target type %q", targetType)
		}
		var err error
		out, err = insertModerationActionTx(ctx, tx, reportID, actor, ActionHide, targetType, targetID, reason, nil)
		return err
	})
	return out, err
}

// lockQuestionRow reads one question's state under a row lock.
func lockQuestionRow(ctx context.Context, tx pgx.Tx, id uuid.UUID, what string) (status string, deleted bool, err error) {
	var deletedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT status, deleted_at FROM questions WHERE id = $1 FOR UPDATE`, id).Scan(&status, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, adminErr(ErrAdminNotFound, "%s not found", what)
	}
	return status, deletedAt != nil, err
}

// AdminLockQuestion closes an open question to new answers and records it.
func (s *Store) AdminLockQuestion(ctx context.Context, actor, questionID uuid.UUID, reportID *uuid.UUID, reason string) (*ModerationAction, error) {
	var out *ModerationAction
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		status, deleted, err := lockQuestionRow(ctx, tx, questionID, "question")
		if err != nil {
			return err
		}
		if deleted || status != "open" {
			return adminErr(ErrAdminConflict, "only an open question can be locked (status %s)", status)
		}
		if _, err := tx.Exec(ctx, `UPDATE questions SET status = 'closed', closed_by = $2, closed_reason = $3, updated_at = now() WHERE id = $1`,
			questionID, actor, reason); err != nil {
			return err
		}
		out, err = insertModerationActionTx(ctx, tx, reportID, actor, ActionLock, "question", questionID, reason, nil)
		return err
	})
	return out, err
}

// AdminMergeQuestion merges one live question into another live, unmerged
// question and records it. Merging is not reversible through any route.
func (s *Store) AdminMergeQuestion(ctx context.Context, actor, questionID, intoID uuid.UUID, reportID *uuid.UUID, reason string) (*ModerationAction, error) {
	if questionID == intoID {
		return nil, adminErr(ErrAdminInvalid, "a question cannot be merged into itself")
	}
	var out *ModerationAction
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		status, deleted, err := lockQuestionRow(ctx, tx, questionID, "question")
		if err != nil {
			return err
		}
		if deleted || status == "merged" || status == "deleted" {
			return adminErr(ErrAdminConflict, "question cannot be merged (status %s)", status)
		}
		intoStatus, intoDeleted, err := lockQuestionRow(ctx, tx, intoID, "merge target")
		if err != nil {
			return err
		}
		if intoDeleted || intoStatus == "merged" || intoStatus == "deleted" {
			return adminErr(ErrAdminConflict, "merge target is not a live question (status %s)", intoStatus)
		}
		if _, err := tx.Exec(ctx, `UPDATE questions SET status = 'merged', merged_into_id = $2, updated_at = now() WHERE id = $1`,
			questionID, intoID); err != nil {
			return err
		}
		out, err = insertModerationActionTx(ctx, tx, reportID, actor, ActionMerge, "question", questionID, reason,
			map[string]any{"merged_into_id": intoID.String(), "previous_status": status})
		return err
	})
	return out, err
}

// AdminMarkDuplicate links a question to the one it duplicates, once, and
// records it. It changes no visibility.
func (s *Store) AdminMarkDuplicate(ctx context.Context, actor, questionID, duplicateOfID uuid.UUID, reportID *uuid.UUID, reason string) (*ModerationAction, error) {
	if questionID == duplicateOfID {
		return nil, adminErr(ErrAdminInvalid, "a question cannot duplicate itself")
	}
	var out *ModerationAction
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		if _, _, err := lockQuestionRow(ctx, tx, questionID, "question"); err != nil {
			return err
		}
		if _, _, err := lockQuestionRow(ctx, tx, duplicateOfID, "duplicate_of question"); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM question_duplicates WHERE question_id = $1 AND duplicate_of_id = $2)`,
			questionID, duplicateOfID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return adminErr(ErrAdminConflict, "question is already marked as that duplicate")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO question_duplicates (question_id, duplicate_of_id, marked_by) VALUES ($1, $2, $3)`,
			questionID, duplicateOfID, actor); err != nil {
			return err
		}
		var err error
		out, err = insertModerationActionTx(ctx, tx, reportID, actor, ActionMarkDuplicate, "question", questionID, reason,
			map[string]any{"duplicate_of_id": duplicateOfID.String()})
		return err
	})
	return out, err
}

// ListModerationActionsFiltered is the admin history read: newest first,
// optionally narrowed to one target, one actor or one action type.
func (s *Store) ListModerationActionsFiltered(ctx context.Context, targetID, actorID *uuid.UUID, actionType string, limit, offset int) ([]ModerationAction, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, report_id, actor_id, action_type, target_type, target_id, reason, created_at
		FROM moderation_actions
		WHERE ($1::uuid IS NULL OR target_id = $1)
		  AND ($2::uuid IS NULL OR actor_id = $2)
		  AND ($3 = '' OR action_type = $3)
		ORDER BY created_at DESC, id
		LIMIT $4 OFFSET $5`, targetID, actorID, actionType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []ModerationAction{}
	for rows.Next() {
		var a ModerationAction
		if err := rows.Scan(&a.ID, &a.ReportID, &a.ActorID, &a.ActionType, &a.TargetType, &a.TargetID, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, rows.Err()
}

// AdminStats are the Q&A dashboard counts (admin console Wave 2). "Today"
// starts at midnight India time. Hidden counts come from moderation_actions
// (a hide by either admin path), so a user deleting their own post is not
// counted; created counts include posts later hidden or deleted.
type AdminStats struct {
	OpenReportsByReason map[string]int `json:"open_reports_by_reason"`
	OpenReportsTotal    int            `json:"open_reports_total"`

	HiddenQuestionsLast7Days int `json:"hidden_questions_last_7_days"`
	HiddenAnswersLast7Days   int `json:"hidden_answers_last_7_days"`

	// LockedQuestions: live questions closed by a moderator lock.
	LockedQuestions int `json:"locked_questions"`

	QuestionsCreatedToday     int `json:"questions_created_today"`
	QuestionsCreatedLast7Days int `json:"questions_created_last_7_days"`
	AnswersCreatedToday       int `json:"answers_created_today"`
	AnswersCreatedLast7Days   int `json:"answers_created_last_7_days"`

	DayStartsAt time.Time `json:"day_starts_at"`
	GeneratedAt time.Time `json:"generated_at"`
}

// AdminStats reads the dashboard counts.
func (s *Store) AdminStats(ctx context.Context) (*AdminStats, error) {
	out := &AdminStats{OpenReportsByReason: map[string]int{}}
	err := s.db.QueryRow(ctx, `
		WITH bounds AS (
			SELECT (date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') AS day_start,
				now() - interval '7 days' AS week_start,
				now() AS generated_at
		)
		SELECT
			(SELECT COUNT(DISTINCT target_id) FROM moderation_actions, bounds
				WHERE action_type = 'hide' AND target_type = 'question' AND created_at >= bounds.week_start)::int,
			(SELECT COUNT(DISTINCT target_id) FROM moderation_actions, bounds
				WHERE action_type = 'hide' AND target_type = 'answer' AND created_at >= bounds.week_start)::int,
			(SELECT COUNT(*) FROM questions q
				WHERE q.status = 'closed' AND q.deleted_at IS NULL
				  AND EXISTS (SELECT 1 FROM moderation_actions m
				              WHERE m.action_type = 'lock' AND m.target_type = 'question' AND m.target_id = q.id))::int,
			(SELECT COUNT(*) FROM questions, bounds WHERE created_at >= bounds.day_start)::int,
			(SELECT COUNT(*) FROM questions, bounds WHERE created_at >= bounds.week_start)::int,
			(SELECT COUNT(*) FROM answers, bounds WHERE created_at >= bounds.day_start)::int,
			(SELECT COUNT(*) FROM answers, bounds WHERE created_at >= bounds.week_start)::int,
			bounds.day_start, bounds.generated_at
		FROM bounds`,
	).Scan(&out.HiddenQuestionsLast7Days, &out.HiddenAnswersLast7Days, &out.LockedQuestions,
		&out.QuestionsCreatedToday, &out.QuestionsCreatedLast7Days,
		&out.AnswersCreatedToday, &out.AnswersCreatedLast7Days,
		&out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("qa admin stats: %w", err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT reason, COUNT(*)::int FROM moderation_reports
		WHERE status IN ('pending', 'reviewed')
		GROUP BY reason`)
	if err != nil {
		return nil, fmt.Errorf("qa admin stats: reports: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		var n int
		if err := rows.Scan(&reason, &n); err != nil {
			return nil, err
		}
		out.OpenReportsByReason[reason] = n
		out.OpenReportsTotal += n
	}
	return out, rows.Err()
}
