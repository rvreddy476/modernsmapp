package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

type ReportStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *ReportStore {
	return &ReportStore{db: db}
}

func (s *ReportStore) CreateReport(ctx context.Context, report *Report) error {
	query := `
		INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, reason, details, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := s.db.Exec(ctx, query,
		report.ID,
		report.ReporterID,
		report.EntityType,
		report.EntityID,
		report.Reason,
		report.Details,
		report.Status,
		report.CreatedAt,
		report.UpdatedAt,
	)
	return err
}

// CheckDuplicate returns true if an open report already exists for this reporter+entity pair.
func (s *ReportStore) CheckDuplicate(ctx context.Context, reporterID, entityID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trust.reports
			WHERE reporter_id = $1 AND entity_id = $2 AND status = 'open'
		)
	`, reporterID, entityID).Scan(&exists)
	return exists, err
}

func (s *ReportStore) GetReports(ctx context.Context, limit int, offset int) ([]Report, error) {
	query := `
		SELECT id, reporter_id, entity_type, entity_id, reason, details, status, created_at, updated_at
		FROM trust.reports
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := s.db.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(
			&r.ID, &r.ReporterID, &r.EntityType, &r.EntityID, &r.Reason, &r.Details, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, nil
}
