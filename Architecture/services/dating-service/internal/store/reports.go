// Reports store (Dating plan lane D8): fixed reason codes, evidence
// references, the per-reporter limit, the auto-block marker and the
// trust-safety grievance link with its retry bookkeeping.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Report reason codes. Every new report carries exactly one.
const (
	ReportReasonHarassment  = "harassment"
	ReportReasonFakeProfile = "fake_profile"
	ReportReasonUnderage    = "underage"
	ReportReasonNudity      = "nudity"
	ReportReasonScam        = "scam"
	ReportReasonHate        = "hate"
	ReportReasonViolence    = "violence"
	ReportReasonSpam        = "spam"
	ReportReasonOther       = "other"
)

// ReportReasons lists the reason codes in display order.
var ReportReasons = []string{
	ReportReasonHarassment, ReportReasonFakeProfile, ReportReasonUnderage, ReportReasonNudity,
	ReportReasonScam, ReportReasonHate, ReportReasonViolence, ReportReasonSpam, ReportReasonOther,
}

// IsReportReason reports whether r is a reason code.
func IsReportReason(r string) bool {
	for _, want := range ReportReasons {
		if r == want {
			return true
		}
	}
	return false
}

// Report limits.
const (
	MaxReportDetailsChars     = 500
	MaxReportEvidencePhotos   = 10
	MaxReportEvidenceMessages = 20
	MaxReportMessageRefLen    = 128
	MaxReportEvidenceSparks   = 10
	DefaultReportDailyLimit   = 10
	ReportQuotaWindow         = 24 * time.Hour
)

// ErrReportRateLimited is returned when a reporter is over the daily limit.
var ErrReportRateLimited = errors.New("report rate limited")

// ErrReportEvidenceInvalid is returned when a photo or spark reference does
// not belong to the reported user (or the pair).
var ErrReportEvidenceInvalid = errors.New("invalid: evidence must reference the reported person's photos or sparks between you")

// ErrReportNotFound is returned when a report id does not exist.
var ErrReportNotFound = errors.New("not_found: report not found")

// ReportEvidence holds references only. Message ids are opaque to dating:
// chat owns the messages and resolves them when a moderator looks.
type ReportEvidence struct {
	PhotoIDs   []uuid.UUID `json:"photo_ids,omitempty"`
	MessageIDs []string    `json:"message_ids,omitempty"`
	SparkIDs   []uuid.UUID `json:"spark_ids,omitempty"`
}

// Report is a row of dating_reports.
type Report struct {
	ID                 uuid.UUID      `json:"id"`
	ReporterID         uuid.UUID      `json:"reporter_id"`
	TargetID           uuid.UUID      `json:"target_id"`
	Category           string         `json:"category"`
	Reason             string         `json:"reason"`
	Details            string         `json:"details"`
	Status             string         `json:"status"`
	Evidence           ReportEvidence `json:"evidence"`
	AutoBlocked        bool           `json:"auto_blocked"`
	GrievanceID        *uuid.UUID     `json:"grievance_id,omitempty"`
	ReporterAnonymised bool           `json:"reporter_anonymised"`
	RetainUntil        *time.Time     `json:"retain_until,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
}

const reportCols = `id, reporter_id, target_id, category, details, status, evidence,
    auto_blocked, grievance_id, reporter_anonymised_at IS NOT NULL, retain_until, created_at`

func scanReport(row pgx.Row) (*Report, error) {
	r := &Report{}
	var evidence []byte
	if err := row.Scan(&r.ID, &r.ReporterID, &r.TargetID, &r.Category, &r.Details, &r.Status, &evidence,
		&r.AutoBlocked, &r.GrievanceID, &r.ReporterAnonymised, &r.RetainUntil, &r.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReportNotFound
		}
		return nil, fmt.Errorf("scan report: %w", err)
	}
	r.Reason = r.Category
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &r.Evidence)
	}
	return r, nil
}

// CreateReportParams is one report. DailyLimit <= 0 applies no limit.
type CreateReportParams struct {
	ReporterID uuid.UUID
	TargetID   uuid.UUID
	Reason     string
	Details    string
	Evidence   ReportEvidence
	DailyLimit int
}

func uniqueUUIDs(in []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(in))
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if _, ok := seen[id]; ok || id == uuid.Nil {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// CreateReportWithParams persists a report. Under a per-reporter advisory
// lock it enforces the daily limit and checks that evidence photos belong
// to the target and evidence sparks were exchanged by the pair.
func (s *Store) CreateReportWithParams(ctx context.Context, p CreateReportParams) (*Report, error) {
	if p.ReporterID == uuid.Nil || p.TargetID == uuid.Nil {
		return nil, fmt.Errorf("invalid: reporter and target ids required")
	}
	if p.ReporterID == p.TargetID {
		return nil, fmt.Errorf("invalid: cannot report yourself")
	}
	if strings.TrimSpace(p.Reason) == "" {
		return nil, fmt.Errorf("invalid: reason required")
	}
	p.Evidence.PhotoIDs = uniqueUUIDs(p.Evidence.PhotoIDs)
	p.Evidence.SparkIDs = uniqueUUIDs(p.Evidence.SparkIDs)
	evidence, err := json.Marshal(p.Evidence)
	if err != nil {
		return nil, fmt.Errorf("invalid: evidence: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin report: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "dating_reports:"+p.ReporterID.String()); err != nil {
		return nil, fmt.Errorf("lock reports: %w", err)
	}
	if p.DailyLimit > 0 {
		var n int
		if err := tx.QueryRow(ctx, `
            SELECT COUNT(*)::int FROM dating_reports
            WHERE reporter_id = $1 AND created_at > now() - make_interval(secs => $2)`,
			p.ReporterID, ReportQuotaWindow.Seconds()).Scan(&n); err != nil {
			return nil, fmt.Errorf("count reports: %w", err)
		}
		if n >= p.DailyLimit {
			return nil, ErrReportRateLimited
		}
	}
	if len(p.Evidence.PhotoIDs) > 0 {
		var n int
		if err := tx.QueryRow(ctx, `
            SELECT COUNT(*)::int FROM dating_photos WHERE id = ANY($1) AND user_id = $2`,
			p.Evidence.PhotoIDs, p.TargetID).Scan(&n); err != nil {
			return nil, fmt.Errorf("check evidence photos: %w", err)
		}
		if n != len(p.Evidence.PhotoIDs) {
			return nil, ErrReportEvidenceInvalid
		}
	}
	if len(p.Evidence.SparkIDs) > 0 {
		var n int
		if err := tx.QueryRow(ctx, `
            SELECT COUNT(*)::int FROM dating_sparks
            WHERE id = ANY($1)
              AND ((from_user_id = $2 AND to_user_id = $3) OR (from_user_id = $3 AND to_user_id = $2))`,
			p.Evidence.SparkIDs, p.ReporterID, p.TargetID).Scan(&n); err != nil {
			return nil, fmt.Errorf("check evidence sparks: %w", err)
		}
		if n != len(p.Evidence.SparkIDs) {
			return nil, ErrReportEvidenceInvalid
		}
	}
	r, err := scanReport(tx.QueryRow(ctx, `
        INSERT INTO dating_reports (reporter_id, target_id, category, details, evidence, grievance_next_attempt_at)
        VALUES ($1, $2, $3, $4, $5::jsonb, now())
        RETURNING `+reportCols, p.ReporterID, p.TargetID, p.Reason, p.Details, evidence))
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit report: %w", err)
	}
	return r, nil
}

// CreateReport persists a report with no evidence and no daily limit. The
// service path is CreateReportWithParams.
func (s *Store) CreateReport(ctx context.Context, reporterID, targetID uuid.UUID, category, details string) (*Report, error) {
	return s.CreateReportWithParams(ctx, CreateReportParams{
		ReporterID: reporterID, TargetID: targetID, Reason: category, Details: details,
	})
}

// ListReports returns dating_reports newest-first with simple filters. Used
// by /admin/dating/reports.
func (s *Store) ListReports(ctx context.Context, status, category string, limit, offset int) ([]*Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+reportCols+`
        FROM dating_reports
        WHERE ($1 = '' OR status = $1) AND ($2 = '' OR category = $2)
        ORDER BY created_at DESC, id
        LIMIT $3 OFFSET $4`, status, category, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	out := make([]*Report, 0, limit)
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReportByID returns a single report or ErrReportNotFound.
func (s *Store) GetReportByID(ctx context.Context, reportID uuid.UUID) (*Report, error) {
	if reportID == uuid.Nil {
		return nil, fmt.Errorf("invalid: report_id required")
	}
	return scanReport(s.db.QueryRow(ctx, `SELECT `+reportCols+` FROM dating_reports WHERE id = $1`, reportID))
}

// SetReportStatus moves a report through the §P0-8 state machine.
func (s *Store) SetReportStatus(ctx context.Context, reportID uuid.UUID, status string) error {
	switch status {
	case "submitted", "under_review", "investigating", "actioned",
		"resolved", "dismissed", "closed_no_action":
	default:
		return fmt.Errorf("invalid: status %q", status)
	}
	tag, err := s.db.Exec(ctx, `UPDATE dating_reports SET status = $2 WHERE id = $1`, reportID, status)
	if err != nil {
		return fmt.Errorf("update report status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrReportNotFound
	}
	return nil
}

// MarkReportAutoBlocked records that filing the report blocked the target.
func (s *Store) MarkReportAutoBlocked(ctx context.Context, reportID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE dating_reports SET auto_blocked = true WHERE id = $1`, reportID); err != nil {
		return fmt.Errorf("mark report auto-blocked: %w", err)
	}
	return nil
}

// SetReportGrievance stores the linked trust-safety grievance and stops the
// retries. A report already linked keeps its first grievance.
func (s *Store) SetReportGrievance(ctx context.Context, reportID, grievanceID uuid.UUID) error {
	if grievanceID == uuid.Nil {
		return fmt.Errorf("invalid: grievance_id required")
	}
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_reports
        SET grievance_id = $2, grievance_next_attempt_at = NULL, grievance_last_error = NULL
        WHERE id = $1 AND grievance_id IS NULL`, reportID, grievanceID); err != nil {
		return fmt.Errorf("set report grievance: %w", err)
	}
	return nil
}

// RecordGrievanceLinkFailure schedules the next link attempt with capped
// exponential backoff (30s doubling, at most an hour).
func (s *Store) RecordGrievanceLinkFailure(ctx context.Context, reportID uuid.UUID, cause string) error {
	if len(cause) > 500 {
		cause = cause[:500]
	}
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_reports
        SET grievance_attempts = grievance_attempts + 1,
            grievance_last_error = $2,
            grievance_next_attempt_at = now() + make_interval(secs =>
                LEAST(3600, 30 * power(2, LEAST(grievance_attempts, 7))))
        WHERE id = $1 AND grievance_id IS NULL`, reportID, cause); err != nil {
		return fmt.Errorf("record grievance link failure: %w", err)
	}
	return nil
}

// grievanceClaimLease keeps a claimed report away from other replicas while
// one attempt runs.
const grievanceClaimLease = 5 * time.Minute

// ClaimReportsForGrievanceLink claims reports whose grievance link is due,
// pushing their next attempt out by a lease so concurrent sweepers never
// link the same report at once.
func (s *Store) ClaimReportsForGrievanceLink(ctx context.Context, limit int) ([]*Report, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
        UPDATE dating_reports
        SET grievance_next_attempt_at = now() + make_interval(secs => $2)
        WHERE id IN (
            SELECT id FROM dating_reports
            WHERE grievance_id IS NULL
              AND grievance_next_attempt_at IS NOT NULL
              AND grievance_next_attempt_at <= now()
            ORDER BY grievance_next_attempt_at
            LIMIT $1
            FOR UPDATE SKIP LOCKED)
        RETURNING `+reportCols, limit, grievanceClaimLease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim reports for grievance link: %w", err)
	}
	defer rows.Close()
	var out []*Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
