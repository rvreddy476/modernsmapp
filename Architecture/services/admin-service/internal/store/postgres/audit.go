package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditLog struct {
	ID         uuid.UUID       `json:"id"`
	AdminActor string          `json:"admin_actor"`
	App        *string         `json:"app"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Reason     *string         `json:"reason"`
	RequestID  *string         `json:"request_id"`
	Outcome    *string         `json:"outcome"`
	StatusCode *int            `json:"status_code"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Audit outcomes. A request whose operation answered 2xx is a success; any
// other status, or no answer at all, is a failure. Denied means admin-service
// refused it before it ran (permission, MFA, step-up, identity unavailable);
// pending and rejected belong to two-person approval (migration 003).
const (
	AuditOutcomeSuccess  = "success"
	AuditOutcomeFailure  = "failure"
	AuditOutcomeDenied   = "denied"
	AuditOutcomePending  = "pending"
	AuditOutcomeRejected = "rejected"
)

func validAuditOutcome(o string) bool {
	switch o {
	case AuditOutcomeSuccess, AuditOutcomeFailure, AuditOutcomeDenied, AuditOutcomePending, AuditOutcomeRejected:
		return true
	}
	return false
}

// AdminAuditEntry is one admin write that passed through admin-service.
type AdminAuditEntry struct {
	Actor      string // the admin's user id
	App        string // the owning app, e.g. "commerce"
	Operation  string // e.g. "seller.approve"
	TargetType string // e.g. "seller"
	TargetID   string
	Reason     string // empty when none was given
	RequestID  string
	Outcome    string // one of the AuditOutcome constants
	StatusCode int    // downstream status; 0 when the downstream never answered
	Payload    map[string]any
}

var ErrInvalidAuditEntry = errors.New("audit entry needs actor, app, operation, target and outcome")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// RecordAdminWrite appends one row to admin.audit_log. The table is
// append-only (migration 002); there is no update path.
func (s *Store) RecordAdminWrite(ctx context.Context, e AdminAuditEntry) error {
	if e.Actor == "" || e.App == "" || e.Operation == "" || e.TargetType == "" || !validAuditOutcome(e.Outcome) {
		return ErrInvalidAuditEntry
	}
	var payload []byte
	if len(e.Payload) > 0 {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		payload = b
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO admin.audit_log
			(id, admin_actor, app, action, entity_type, entity_id, reason, request_id, outcome, status_code, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11, NOW())
	`, uuid.New(), e.Actor, e.App, e.Operation, e.TargetType, e.TargetID,
		e.Reason, e.RequestID, e.Outcome, e.StatusCode, payload)
	return err
}

// GetAuditLogs returns paginated audit log entries ordered by most recent first.
func (s *Store) GetAuditLogs(ctx context.Context, limit, offset int) ([]AuditLog, int, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin.audit_log`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, admin_actor, app, action, entity_type, entity_id, reason, request_id,
		       outcome, status_code, payload, created_at
		FROM admin.audit_log
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var l AuditLog
		if err := rows.Scan(&l.ID, &l.AdminActor, &l.App, &l.Action, &l.EntityType, &l.EntityID,
			&l.Reason, &l.RequestID, &l.Outcome, &l.StatusCode, &l.Payload, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

// DashboardStats holds the admin dashboard numbers.
//
// A nil value means the number is not available, and Unavailable carries the
// reason under the same key. A zero is only ever a real count.
type DashboardStats struct {
	TotalUsers               *int64            `json:"total_users"`
	ActiveUsersToday         *int64            `json:"active_users_today"`
	TotalPosts               *int64            `json:"total_posts"`
	NewUsersLast7d           *int64            `json:"new_users_last_7d"`
	OpenReports              *int64            `json:"open_reports"`
	ReportsResolvedLast7d    *int64            `json:"reports_resolved_last_7d"`
	TakedownsLast7d          *int64            `json:"takedowns_last_7d"`
	ActiveSuspensions        *int64            `json:"active_suspensions"`
	AdminWritesLast7d        *int64            `json:"admin_writes_last_7d"`
	AdminWriteFailuresLast7d *int64            `json:"admin_write_failures_last_7d"`
	Unavailable              map[string]string `json:"unavailable"`
}

// Reasons a dashboard number is unavailable.
const (
	DashboardReasonOwnedElsewhere   = "owned_by_another_service"
	DashboardReasonTakedownDisabled = "takedown_disabled_in_admin_service"
	DashboardReasonQueryFailed      = "query_failed"
)

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetDashboardStats returns the numbers admin-service can compute from its own
// tables. Users, posts and reports live in other services' databases; they are
// reported unavailable rather than as a zero until per-service stats exist.
func (s *Store) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	return dashboardStats(ctx, s.db), nil
}

func dashboardStats(ctx context.Context, q rowQuerier) *DashboardStats {
	stats := &DashboardStats{Unavailable: map[string]string{
		"total_users":              DashboardReasonOwnedElsewhere,
		"active_users_today":       DashboardReasonOwnedElsewhere,
		"total_posts":              DashboardReasonOwnedElsewhere,
		"new_users_last_7d":        DashboardReasonOwnedElsewhere,
		"open_reports":             DashboardReasonOwnedElsewhere,
		"reports_resolved_last_7d": DashboardReasonOwnedElsewhere,
		"takedowns_last_7d":        DashboardReasonTakedownDisabled,
	}}

	count := func(key, sql string) *int64 {
		var n int64
		if err := q.QueryRow(ctx, sql).Scan(&n); err != nil {
			slog.ErrorContext(ctx, "admin dashboard count failed", "metric", key, "error", err)
			stats.Unavailable[key] = DashboardReasonQueryFailed
			return nil
		}
		return &n
	}

	stats.ActiveSuspensions = count("active_suspensions",
		`SELECT COUNT(*) FROM admin.suspensions WHERE until > NOW()`)
	stats.AdminWritesLast7d = count("admin_writes_last_7d",
		`SELECT COUNT(*) FROM admin.audit_log WHERE created_at >= NOW() - INTERVAL '7 days'`)
	stats.AdminWriteFailuresLast7d = count("admin_write_failures_last_7d",
		`SELECT COUNT(*) FROM admin.audit_log
		 WHERE outcome = 'failure' AND created_at >= NOW() - INTERVAL '7 days'`)
	return stats
}

// PurgeAuditLogsOlderThan deletes audit-log rows older than retentionDays
// and returns how many were removed. CERT-In requires security logs be
// retained for at least 180 days; the retention window is operator-tunable
// (AUDIT_LOG_RETENTION_DAYS) and the caller must not set it below 180. The
// append-only trigger refuses to delete anything younger than 180 days.
func (s *Store) PurgeAuditLogsOlderThan(ctx context.Context, retentionDays int) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM admin.audit_log WHERE created_at < NOW() - make_interval(days => $1)`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
