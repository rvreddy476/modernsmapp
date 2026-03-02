package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditLog struct {
	ID         uuid.UUID       `json:"id"`
	AdminActor string          `json:"admin_actor"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// DashboardStats holds aggregate counts for the admin dashboard.
type DashboardStats struct {
	TotalUsers          int `json:"total_users"`
	ActiveUsersToday    int `json:"active_users_today"`
	TotalPosts          int `json:"total_posts"`
	OpenReports         int `json:"open_reports"`
	ActiveSuspensions   int `json:"active_suspensions"`
	TakedownsLast7d     int `json:"takedowns_last_7d"`
	NewUsersLast7d      int `json:"new_users_last_7d"`
	ReportsResolvedLast7d int `json:"reports_resolved_last_7d"`
}

// GetAuditLogs returns paginated audit log entries ordered by most recent first.
func (s *Store) GetAuditLogs(ctx context.Context, limit, offset int) ([]AuditLog, int, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin.audit_log`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, admin_actor, action, entity_type, entity_id, payload, created_at
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
		if err := rows.Scan(&l.ID, &l.AdminActor, &l.Action, &l.EntityType, &l.EntityID, &l.Payload, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, nil
}

// GetDashboardStats gathers aggregate counts across schemas for the admin dashboard.
func (s *Store) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{}

	// Total users (from social schema)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM social.users`).Scan(&stats.TotalUsers)

	// Active users today (users with activity today from social.counts)
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM social.counts
		WHERE updated_at >= CURRENT_DATE
	`).Scan(&stats.ActiveUsersToday)

	// Total posts
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM social.posts`).Scan(&stats.TotalPosts)

	// Open reports (from trust schema)
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM trust.reports WHERE status = 'open'
	`).Scan(&stats.OpenReports)

	// Active suspensions
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin.suspensions WHERE until > NOW()
	`).Scan(&stats.ActiveSuspensions)

	// Takedowns in last 7 days
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin.audit_log
		WHERE action = 'TAKEDOWN' AND created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&stats.TakedownsLast7d)

	// New users in last 7 days
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM social.users
		WHERE created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&stats.NewUsersLast7d)

	// Reports resolved in last 7 days
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM trust.reports
		WHERE status != 'open' AND updated_at >= NOW() - INTERVAL '7 days'
	`).Scan(&stats.ReportsResolvedLast7d)

	return stats, nil
}

func (s *Store) LogAction(ctx context.Context, actor, action, entityType, entityID string, payload interface{}) error {
	pBytes, _ := json.Marshal(payload)

	query := `
		INSERT INTO admin.audit_log (id, admin_actor, action, entity_type, entity_id, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.Exec(ctx, query,
		uuid.New(),
		actor,
		action,
		entityType,
		entityID,
		pBytes,
		time.Now(),
	)
	return err
}
