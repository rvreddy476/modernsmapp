package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Admin console (Wave 2 — Content, Chat): the community report queue behind
// admin-service's token family. A decision updates the report and appends
// one community_admin_audit row in the same transaction. The actor is always the
// signed act claim the HTTP layer hands down.
//
// A decision records the moderator's judgement only. It does not remove a
// member, delete a post, or archive or suspend a community: removal, ban,
// post deletion and archive exist for community owners and moderators, and
// none of them is wired to admin-service or audited here yet.

var (
	ErrAdminNotFound      = errors.New("not found")
	ErrAdminConflict      = errors.New("conflict")
	ErrAdminActorRequired = errors.New("admin actor is required")
)

// Report states. 'pending' is the table's default.
const (
	ReportPending   = "pending"
	ReportUpheld    = "upheld"
	ReportDismissed = "dismissed"
)

// Audit actions.
const (
	AuditCommunityReportUpheld    = "community_report.upheld"
	AuditCommunityReportDismissed = "community_report.dismissed"
)

// CommunityReport is one row of the admin queue, with the community's name.
type CommunityReport struct {
	ID            uuid.UUID  `json:"id"`
	CommunityID   uuid.UUID  `json:"community_id"`
	CommunityName *string    `json:"community_name,omitempty"`
	ReporterID    string     `json:"reporter_id"`
	TargetType    string     `json:"target_type"`
	TargetID      uuid.UUID  `json:"target_id"`
	Reason        string     `json:"reason"`
	Description   *string    `json:"description,omitempty"`
	Status        string     `json:"status"`
	ReviewedBy    *string    `json:"reviewed_by,omitempty"`
	ReviewReason  *string    `json:"review_reason,omitempty"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CommunityAdminStats are the counts the Chat dashboard shows for communities.
type CommunityAdminStats struct {
	OpenReports        int `json:"open_reports"`
	ReportsDecided7d   int `json:"reports_decided_7d"`
	ReportsUpheld7d    int `json:"reports_upheld_7d"`
	ReportsDismissed7d int `json:"reports_dismissed_7d"`
	// SuspendedCommunities: the CHECK admits 'suspended'; nothing in this
	// service writes it yet, so this is expected to read 0.
	SuspendedCommunities int        `json:"suspended_communities"`
	OldestOpenReportAt   *time.Time `json:"oldest_open_report_at,omitempty"`
	GeneratedAt          time.Time  `json:"generated_at"`
}

const communityReportSelect = `SELECT r.id, r.community_id, g.name, r.reporter_id, r.target_type, r.target_id, r.reason,
		r.description, COALESCE(r.status, 'pending'), r.reviewed_by, r.review_reason, r.reviewed_at, r.created_at
	FROM community_reports r
	LEFT JOIN communities g ON g.id = r.community_id`

func scanCommunityReport(row pgx.Row) (*CommunityReport, error) {
	var r CommunityReport
	var created *time.Time
	if err := row.Scan(&r.ID, &r.CommunityID, &r.CommunityName, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason,
		&r.Description, &r.Status, &r.ReviewedBy, &r.ReviewReason, &r.ReviewedAt, &created); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	if created != nil {
		r.CreatedAt = *created
	}
	return &r, nil
}

// EncodeCommunityReportCursor packs a keyset cursor over (created_at, id).
func EncodeCommunityReportCursor(createdAt time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCommunityReportCursor(cur string) (time.Time, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return t, id, true
}

// AdminListCommunityReports pages reports newest first. status "" means pending;
// "all" lists every state.
func (s *Store) AdminListCommunityReports(ctx context.Context, status string, limit int, cursor string) ([]CommunityReport, string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = ReportPending
	}
	if status != "all" && status != ReportPending && status != ReportUpheld && status != ReportDismissed {
		return nil, "", fmt.Errorf("invalid: status must be pending, upheld, dismissed or all")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := communityReportSelect + ` WHERE 1 = 1`
	args := []any{}
	if status != "all" {
		args = append(args, status)
		query += fmt.Sprintf(` AND COALESCE(r.status, 'pending') = $%d`, len(args))
	}
	if ts, id, ok := decodeCommunityReportCursor(cursor); ok {
		args = append(args, ts, id)
		query += fmt.Sprintf(` AND (r.created_at, r.id) < ($%d, $%d)`, len(args)-1, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY r.created_at DESC, r.id DESC LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []CommunityReport{}
	for rows.Next() {
		r, err := scanCommunityReport(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = EncodeCommunityReportCursor(last.CreatedAt, last.ID)
	}
	return out, next, nil
}

// AdminGetCommunityReport reads one report.
func (s *Store) AdminGetCommunityReport(ctx context.Context, id uuid.UUID) (*CommunityReport, error) {
	return scanCommunityReport(s.db.QueryRow(ctx, communityReportSelect+` WHERE r.id = $1`, id))
}

// AdminDecideCommunityReport upholds or dismisses a PENDING report and audits it.
// status is ReportUpheld or ReportDismissed; reason is required.
func (s *Store) AdminDecideCommunityReport(ctx context.Context, actor, id uuid.UUID, status, reason string) (*CommunityReport, error) {
	if actor == uuid.Nil {
		return nil, ErrAdminActorRequired
	}
	action := AuditCommunityReportDismissed
	switch status {
	case ReportUpheld:
		action = AuditCommunityReportUpheld
	case ReportDismissed:
	default:
		return nil, fmt.Errorf("invalid: decision must be uphold or dismiss")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	var communityID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT COALESCE(status, 'pending'), community_id FROM community_reports WHERE id = $1 FOR UPDATE`, id).Scan(&current, &communityID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	if current != ReportPending {
		return nil, fmt.Errorf("report is already %s: %w", current, ErrAdminConflict)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE community_reports SET status = $2, reviewed_by = $3, review_reason = $4, reviewed_at = NOW()
		WHERE id = $1`, id, status, actor.String(), reason); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO community_admin_audit (actor_id, action, target_type, target_id, reason, metadata)
		VALUES ($1, $2, 'community_report', $3, $4, $5)`,
		actor, action, id, reason, map[string]any{"community_id": communityID, "previous_status": current, "status": status}); err != nil {
		return nil, fmt.Errorf("community admin audit %s: %w", action, err)
	}
	out, err := scanCommunityReport(tx.QueryRow(ctx, communityReportSelect+` WHERE r.id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AdminCommunityStats reads the community report counts in one round trip.
func (s *Store) AdminCommunityStats(ctx context.Context) (*CommunityAdminStats, error) {
	out := &CommunityAdminStats{}
	err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM community_reports WHERE COALESCE(status, 'pending') = 'pending')::int,
			(SELECT COUNT(*) FROM community_reports WHERE status IN ('upheld', 'dismissed') AND reviewed_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM community_reports WHERE status = 'upheld' AND reviewed_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM community_reports WHERE status = 'dismissed' AND reviewed_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM communities WHERE status = 'suspended')::int,
			(SELECT MIN(created_at) FROM community_reports WHERE COALESCE(status, 'pending') = 'pending'),
			NOW()`).Scan(
		&out.OpenReports, &out.ReportsDecided7d, &out.ReportsUpheld7d, &out.ReportsDismissed7d,
		&out.SuspendedCommunities, &out.OldestOpenReportAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("community admin stats: %w", err)
	}
	return out, nil
}
