// Phase F2.3 — bulk SKU import job data access.
// Reuses the existing product_import_jobs table (status, totals, etc.)
// added in earlier commerce work.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ImportJob struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	SellerID      uuid.UUID  `db:"seller_id" json:"seller_id"`
	Filename      string     `db:"filename" json:"filename"`
	FileMediaID   *uuid.UUID `db:"file_media_id" json:"file_media_id,omitempty"`
	Status        string     `db:"status" json:"status"`
	TotalRows     int        `db:"total_rows" json:"total_rows"`
	ValidRows     int        `db:"valid_rows" json:"valid_rows"`
	ImportedRows  int        `db:"imported_rows" json:"imported_rows"`
	ErrorRows     int        `db:"error_rows" json:"error_rows"`
	ErrorFileID   *uuid.UUID `db:"error_file_id" json:"error_file_id,omitempty"`
	DryRun        bool       `db:"dry_run" json:"dry_run"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
	CompletedAt   *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	// Storage key for the seller's uploaded CSV in MinIO.
	StorageKey    string     `json:"storage_key,omitempty"`
}

// CreateImportJob inserts a freshly-uploaded job. storageKey is the
// MinIO object key the seller PUT to; we stash it as the filename so
// the worker can fetch it later (file_media_id isn't used for CSV-only).
func (s *Store) CreateImportJob(ctx context.Context, sellerID uuid.UUID, filename, storageKey string) (*ImportJob, error) {
	j := &ImportJob{
		SellerID:   sellerID,
		Filename:   filename,
		Status:     "uploaded",
		StorageKey: storageKey,
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO product_import_jobs (seller_id, filename, status)
		VALUES ($1, $2, 'uploaded')
		RETURNING id, created_at`,
		sellerID, storageKey, // store key in the filename column so worker can fetch
	).Scan(&j.ID, &j.CreatedAt)
	return j, err
}

func (s *Store) GetImportJob(ctx context.Context, id uuid.UUID) (*ImportJob, error) {
	j := &ImportJob{}
	err := s.db.QueryRow(ctx, `
		SELECT id, seller_id, filename, file_media_id, status,
		       total_rows, valid_rows, imported_rows, error_rows,
		       error_file_id, dry_run, created_at, completed_at
		FROM product_import_jobs WHERE id = $1`, id).Scan(
		&j.ID, &j.SellerID, &j.Filename, &j.FileMediaID, &j.Status,
		&j.TotalRows, &j.ValidRows, &j.ImportedRows, &j.ErrorRows,
		&j.ErrorFileID, &j.DryRun, &j.CreatedAt, &j.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	// Filename column doubles as storage key for now.
	j.StorageKey = j.Filename
	return j, nil
}

// UpdateImportJobStatus is the small state-machine setter. Valid
// transitions are enforced in the service layer.
func (s *Store) UpdateImportJobStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE product_import_jobs SET status = $2,
		  completed_at = CASE WHEN $2 IN ('completed','failed','partially_imported') THEN NOW() ELSE completed_at END
		 WHERE id = $1`, id, status)
	return err
}

// UpdateImportJobCounts updates validation / import row totals. Use
// during the worker's validate and execute phases.
func (s *Store) UpdateImportJobCounts(ctx context.Context, id uuid.UUID, total, valid, imported, errorRows int, errorFileID *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE product_import_jobs SET
			total_rows = $2,
			valid_rows = $3,
			imported_rows = $4,
			error_rows = $5,
			error_file_id = NULLIF($6, '')::uuid
		WHERE id = $1`, id, total, valid, imported, errorRows, derefOrEmpty(errorFileID))
	return err
}

func (s *Store) ListImportJobsForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*ImportJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, seller_id, filename, file_media_id, status,
		       total_rows, valid_rows, imported_rows, error_rows,
		       error_file_id, dry_run, created_at, completed_at
		FROM product_import_jobs WHERE seller_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ImportJob
	for rows.Next() {
		j := &ImportJob{}
		if err := rows.Scan(&j.ID, &j.SellerID, &j.Filename, &j.FileMediaID, &j.Status,
			&j.TotalRows, &j.ValidRows, &j.ImportedRows, &j.ErrorRows,
			&j.ErrorFileID, &j.DryRun, &j.CreatedAt, &j.CompletedAt); err != nil {
			return nil, err
		}
		j.StorageKey = j.Filename
		out = append(out, j)
	}
	return out, rows.Err()
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
