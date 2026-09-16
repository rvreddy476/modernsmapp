package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Admin console (Wave 2 — Content, Chat). The writes behind admin-service's
// token family. Each one changes its row and appends one channel_admin_audit
// row in the same transaction, so a decision and its trail commit or roll
// back together. The actor is always the signed act claim the HTTP layer
// hands down.

var (
	// ErrAdminNotFound: the report or channel does not exist (or is deleted).
	ErrAdminNotFound = errors.New("not found")
	// ErrAdminConflict: the target is not in a state the action applies to
	// (a report already decided, a channel that is not active/suspended).
	ErrAdminConflict = errors.New("conflict")
	// ErrAdminActorRequired: a write without an actor is refused before SQL.
	ErrAdminActorRequired = errors.New("admin actor is required")
)

// Audit actions.
const (
	AuditReportUpheld     = "channel_report.upheld"
	AuditReportDismissed  = "channel_report.dismissed"
	AuditChannelSuspended = "channel.suspended"
	AuditChannelRestored  = "channel.unsuspended"
)

// ChannelStatusChange is the outcome of an admin suspend or unsuspend.
type ChannelStatusChange struct {
	ChannelID      uuid.UUID `json:"channel_id"`
	PreviousStatus string    `json:"previous_status"`
	Status         string    `json:"status"`
}

// AdminStats are the counts the Chat dashboard shows for channels.
type AdminStats struct {
	OpenReports        int        `json:"open_reports"`
	ReportsDecided7d   int        `json:"reports_decided_7d"`
	ReportsUpheld7d    int        `json:"reports_upheld_7d"`
	ReportsDismissed7d int        `json:"reports_dismissed_7d"`
	SuspendedChannels  int        `json:"suspended_channels"`
	OldestOpenReportAt *time.Time `json:"oldest_open_report_at,omitempty"`
	GeneratedAt        time.Time  `json:"generated_at"`
}

func writeChannelAdminAudit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action, targetType string, targetID uuid.UUID, reason string, metadata map[string]any) error {
	if actor == uuid.Nil {
		return ErrAdminActorRequired
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_admin_audit (actor_id, action, target_type, target_id, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		actor, action, targetType, targetID, reason, metadata); err != nil {
		return fmt.Errorf("channel admin audit %s: %w", action, err)
	}
	return nil
}

const reportDetailSelect = `SELECT r.id, r.channel_id, r.update_id, r.reporter_id, r.reason, r.details, r.status, r.created_at,
		r.reviewer_id, r.review_note, r.reviewed_at,
		c.handle, c.name, c.status
	FROM channel_reports r
	JOIN broadcast_channels c ON c.id = r.channel_id
	WHERE r.id = $1`

func scanReportItem(row pgx.Row) (*ReportListItem, error) {
	var it ReportListItem
	var note *string
	if err := row.Scan(&it.ID, &it.ChannelID, &it.UpdateID, &it.ReporterID, &it.Reason, &it.Details,
		&it.Status, &it.CreatedAt, &it.ReviewerID, &note, &it.ReviewedAt,
		&it.ChannelHandle, &it.ChannelName, &it.ChannelStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	if note != nil {
		it.ReviewNote = *note
	}
	return &it, nil
}

// AdminGetReport reads one report with its channel.
func (s *Store) AdminGetReport(ctx context.Context, reportID uuid.UUID) (*ReportListItem, error) {
	return scanReportItem(s.db.QueryRow(ctx, reportDetailSelect, reportID))
}

// AdminDecideReport closes an OPEN report as reviewed (upheld) or dismissed,
// recording the actor as reviewer, and audits it. A report that is already
// decided is a conflict: a second decision must not silently overwrite the
// first reviewer.
func (s *Store) AdminDecideReport(ctx context.Context, actor, reportID uuid.UUID, status, reason string) (*ReportListItem, error) {
	if actor == uuid.Nil {
		return nil, ErrAdminActorRequired
	}
	action := AuditReportDismissed
	if status == "reviewed" {
		action = AuditReportUpheld
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	var channelID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status, channel_id FROM channel_reports WHERE id = $1 FOR UPDATE`, reportID).Scan(&current, &channelID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	if current != "open" {
		return nil, fmt.Errorf("report is already %s: %w", current, ErrAdminConflict)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_reports SET status = $2, reviewer_id = $3, review_note = $4, reviewed_at = NOW()
		WHERE id = $1`, reportID, status, actor, reason); err != nil {
		return nil, err
	}
	if err := writeChannelAdminAudit(ctx, tx, actor, action, "channel_report", reportID, reason,
		map[string]any{"channel_id": channelID, "previous_status": current, "status": status}); err != nil {
		return nil, err
	}
	out, err := scanReportItem(tx.QueryRow(ctx, reportDetailSelect, reportID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AdminSetChannelSuspended suspends an ACTIVE channel (suspend=true) or
// restores a SUSPENDED one to active (suspend=false), and audits it.
// Archived and deleted channels are never touched: restoring must not
// resurrect an archived channel the way the key route's plain UPDATE can.
func (s *Store) AdminSetChannelSuspended(ctx context.Context, actor, channelID uuid.UUID, suspend bool, reason string) (*ChannelStatusChange, error) {
	if actor == uuid.Nil {
		return nil, ErrAdminActorRequired
	}
	from, to, action := "active", "suspended", AuditChannelSuspended
	if !suspend {
		from, to, action = "suspended", "active", AuditChannelRestored
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM broadcast_channels WHERE id = $1 AND status != 'deleted' FOR UPDATE`, channelID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	if current != from {
		return nil, fmt.Errorf("channel is %s, not %s: %w", current, from, ErrAdminConflict)
	}
	if _, err := tx.Exec(ctx, `UPDATE broadcast_channels SET status = $2, updated_at = NOW() WHERE id = $1`, channelID, to); err != nil {
		return nil, err
	}
	if err := writeChannelAdminAudit(ctx, tx, actor, action, "channel", channelID, reason,
		map[string]any{"previous_status": current, "status": to}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ChannelStatusChange{ChannelID: channelID, PreviousStatus: current, Status: to}, nil
}

// AdminStats reads the channel moderation counts in one round trip.
func (s *Store) AdminStats(ctx context.Context) (*AdminStats, error) {
	out := &AdminStats{}
	err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM channel_reports WHERE status = 'open')::int,
			(SELECT COUNT(*) FROM channel_reports WHERE status <> 'open' AND reviewed_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM channel_reports WHERE status = 'reviewed' AND reviewed_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM channel_reports WHERE status = 'dismissed' AND reviewed_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM broadcast_channels WHERE status = 'suspended')::int,
			(SELECT MIN(created_at) FROM channel_reports WHERE status = 'open'),
			NOW()`).Scan(
		&out.OpenReports, &out.ReportsDecided7d, &out.ReportsUpheld7d, &out.ReportsDismissed7d,
		&out.SuspendedChannels, &out.OldestOpenReportAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("channel admin stats: %w", err)
	}
	return out, nil
}
