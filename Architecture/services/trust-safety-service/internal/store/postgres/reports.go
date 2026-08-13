package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrActiveReportExists = errors.New("an active report already exists for this entity")

type Report struct {
	ID              uuid.UUID  `json:"id"`
	ReporterID      uuid.UUID  `json:"reporter_id"`
	EntityType      string     `json:"entity_type"`
	EntityID        uuid.UUID  `json:"entity_id"`
	Reason          string     `json:"reason"`
	Details         string     `json:"details"`
	Status          string     `json:"status"`
	AssignedTo      *uuid.UUID `json:"assigned_to,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolutionNotes string     `json:"resolution_notes,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_reports_one_active_per_reporter_entity" {
		return ErrActiveReportExists
	}
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
		SELECT id, reporter_id, entity_type, entity_id, reason, details, status,
		       assigned_to, resolved_at, COALESCE(resolution_notes,''), created_at, updated_at
		FROM trust.reports
		ORDER BY
			CASE reason
				WHEN 'child_safety' THEN 0
				WHEN 'self_harm' THEN 1
				WHEN 'violence_threat' THEN 2
				ELSE 3
			END,
			created_at ASC,
			id ASC
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
			&r.ID, &r.ReporterID, &r.EntityType, &r.EntityID, &r.Reason, &r.Details, &r.Status,
			&r.AssignedTo, &r.ResolvedAt, &r.ResolutionNotes, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, nil
}

// GetReportsByReporter returns only the authenticated reporter's receipts.
// It deliberately omits queue priority: this is history, so newest first is
// the useful and least surprising order for the user.
func (s *ReportStore) GetReportsByReporter(ctx context.Context, reporterID uuid.UUID, limit, offset int) ([]Report, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, reporter_id, entity_type, entity_id, reason, details, status,
		       assigned_to, resolved_at, COALESCE(resolution_notes,''), created_at, updated_at
		FROM trust.reports
		WHERE reporter_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, reporterID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(
			&r.ID, &r.ReporterID, &r.EntityType, &r.EntityID, &r.Reason, &r.Details, &r.Status,
			&r.AssignedTo, &r.ResolvedAt, &r.ResolutionNotes, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

func (s *ReportStore) GetReport(ctx context.Context, reportID uuid.UUID) (*Report, error) {
	query := `
        SELECT id, reporter_id, entity_type, entity_id, reason, details, status,
               assigned_to, resolved_at, COALESCE(resolution_notes,''), created_at, updated_at
        FROM trust.reports WHERE id = $1
    `
	var r Report
	err := s.db.QueryRow(ctx, query, reportID).Scan(
		&r.ID, &r.ReporterID, &r.EntityType, &r.EntityID, &r.Reason, &r.Details, &r.Status,
		&r.AssignedTo, &r.ResolvedAt, &r.ResolutionNotes, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *ReportStore) UpdateReport(ctx context.Context, reportID uuid.UUID, status string, assignedTo *uuid.UUID, resolutionNotes string) (*Report, error) {
	query := `
        UPDATE trust.reports
        SET status = $2,
            assigned_to = $3,
            resolved_at = CASE WHEN $2 IN ('resolved', 'dismissed') THEN NOW() ELSE resolved_at END,
            resolution_notes = $4,
            updated_at = NOW()
        WHERE id = $1
        RETURNING id, reporter_id, entity_type, entity_id, reason, details, status,
                  assigned_to, resolved_at, COALESCE(resolution_notes,''), created_at, updated_at
    `
	var r Report
	err := s.db.QueryRow(ctx, query, reportID, status, assignedTo, resolutionNotes).Scan(
		&r.ID, &r.ReporterID, &r.EntityType, &r.EntityID, &r.Reason, &r.Details, &r.Status,
		&r.AssignedTo, &r.ResolvedAt, &r.ResolutionNotes, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}
