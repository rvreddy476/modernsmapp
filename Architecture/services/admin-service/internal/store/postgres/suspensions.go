package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Report mirrors the trust.reports table for cross-schema reads.
type Report struct {
	ID         uuid.UUID `json:"id"`
	ReporterID uuid.UUID `json:"reporter_id"`
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	Reason     string    `json:"reason"`
	Details    string    `json:"details"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Suspension struct {
	UserID    uuid.UUID `json:"user_id"`
	Until     time.Time `json:"until"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetSuspensions returns paginated active suspensions.
func (s *Store) GetSuspensions(ctx context.Context, limit, offset int) ([]Suspension, int, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin.suspensions WHERE until > NOW()`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT user_id, until, reason, created_at, updated_at
		FROM admin.suspensions
		WHERE until > NOW()
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var suspensions []Suspension
	for rows.Next() {
		var su Suspension
		if err := rows.Scan(&su.UserID, &su.Until, &su.Reason, &su.CreatedAt, &su.UpdatedAt); err != nil {
			return nil, 0, err
		}
		suspensions = append(suspensions, su)
	}
	return suspensions, total, nil
}

// UnsuspendUser removes a suspension by deleting the record.
func (s *Store) UnsuspendUser(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM admin.suspensions WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no active suspension found for user %s", userID)
	}
	return nil
}

// GetReports returns paginated reports from the trust schema.
func (s *Store) GetReports(ctx context.Context, status string, limit, offset int) ([]Report, int, error) {
	var total int
	countQuery := `SELECT COUNT(*) FROM trust.reports`
	args := []interface{}{}
	if status != "" {
		countQuery += ` WHERE status = $1`
		args = append(args, status)
	}
	err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, reporter_id, entity_type, entity_id, reason, details, status, created_at, updated_at
		FROM trust.reports
	`
	if status != "" {
		query += ` WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = append(args, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var reports []Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.EntityType, &r.EntityID, &r.Reason, &r.Details, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, 0, err
		}
		reports = append(reports, r)
	}
	return reports, total, nil
}

func (s *Store) SuspendUser(ctx context.Context, userID uuid.UUID, until time.Time, reason string) error {
	query := `
		INSERT INTO admin.suspensions (user_id, until, reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (user_id) DO UPDATE 
		SET until = EXCLUDED.until, reason = EXCLUDED.reason, updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.Exec(ctx, query, userID, until, reason, time.Now())
	return err
}
