// DPDP data export store — Sprint 5. See PULSE_DATING_SPEC.md §15.8.
//
// The data export job is async: a row is created in `dating_data_exports`
// with status='pending', a `dating.data.export.requested` event is emitted
// to Kafka, and cmd/data-exporter consumes it. The exporter writes the JSON
// blob to media-service blob storage, allocates a signed URL with 7-day
// expiry, and flips status='ready'.
//
// Rate limit (1 export per 7 days) is enforced at the service layer using
// the latest pending/ready row's requested_at.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DataExport is one row of dating_data_exports.
type DataExport struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"user_id"`
	RequestedAt       time.Time  `json:"requested_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	DownloadURL       *string    `json:"download_url,omitempty"`
	DownloadExpiresAt *time.Time `json:"download_expires_at,omitempty"`
	Status            string     `json:"status"`
}

// ErrDataExportNotFound is returned when the export id does not exist.
var ErrDataExportNotFound = errors.New("not_found: data export not found")

const dataExportCols = `id, user_id, requested_at, completed_at, download_url, download_expires_at, status`

func scanDataExport(row pgx.Row) (*DataExport, error) {
	e := &DataExport{}
	if err := row.Scan(&e.ID, &e.UserID, &e.RequestedAt, &e.CompletedAt, &e.DownloadURL, &e.DownloadExpiresAt, &e.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDataExportNotFound
		}
		return nil, fmt.Errorf("scan data export: %w", err)
	}
	return e, nil
}

// CreateDataExportRequest inserts a 'pending' row and returns it.
func (s *Store) CreateDataExportRequest(ctx context.Context, userID uuid.UUID) (*DataExport, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	row := s.db.QueryRow(ctx, `
        INSERT INTO dating_data_exports (user_id, status)
        VALUES ($1, 'pending')
        RETURNING `+dataExportCols, userID)
	return scanDataExport(row)
}

// GetDataExport returns one export row.
func (s *Store) GetDataExport(ctx context.Context, id uuid.UUID) (*DataExport, error) {
	row := s.db.QueryRow(ctx, `SELECT `+dataExportCols+` FROM dating_data_exports WHERE id = $1`, id)
	return scanDataExport(row)
}

// ListDataExportsForUser returns the user's export history newest-first.
func (s *Store) ListDataExportsForUser(ctx context.Context, userID uuid.UUID) ([]*DataExport, error) {
	rows, err := s.db.Query(ctx, `SELECT `+dataExportCols+`
        FROM dating_data_exports WHERE user_id = $1 ORDER BY requested_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("list exports: %w", err)
	}
	defer rows.Close()
	out := make([]*DataExport, 0, 4)
	for rows.Next() {
		e, err := scanDataExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestExportForUser returns the most recent export row, or nil + nil if
// none exists. Used by the rate-limit gate.
func (s *Store) LatestExportForUser(ctx context.Context, userID uuid.UUID) (*DataExport, error) {
	row := s.db.QueryRow(ctx, `SELECT `+dataExportCols+`
        FROM dating_data_exports WHERE user_id = $1 ORDER BY requested_at DESC LIMIT 1`, userID)
	out, err := scanDataExport(row)
	if errors.Is(err, ErrDataExportNotFound) {
		return nil, nil
	}
	return out, err
}

// MarkDataExportProcessing flips status='processing'.
func (s *Store) MarkDataExportProcessing(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_data_exports
        SET status = 'processing'
        WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Not an error: another worker may have picked it up.
		return nil
	}
	return nil
}

// CompleteDataExport stamps download_url, download_expires_at, completed_at
// and flips status='ready'.
func (s *Store) CompleteDataExport(ctx context.Context, id uuid.UUID, downloadURL string, expiresAt time.Time) error {
	if downloadURL == "" {
		return fmt.Errorf("invalid: download_url required")
	}
	if expiresAt.IsZero() {
		return fmt.Errorf("invalid: download_expires_at required")
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_data_exports
        SET status = 'ready',
            download_url = $2,
            download_expires_at = $3,
            completed_at = COALESCE(completed_at, now())
        WHERE id = $1`, id, downloadURL, expiresAt)
	if err != nil {
		return fmt.Errorf("complete export: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDataExportNotFound
	}
	return nil
}

// FailDataExport flips status='failed'.
func (s *Store) FailDataExport(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
        UPDATE dating_data_exports
        SET status = 'failed', completed_at = now()
        WHERE id = $1 AND status NOT IN ('ready','expired','failed')`, id)
	if err != nil {
		return fmt.Errorf("fail export: %w", err)
	}
	return nil
}

// ExpireOldExports flips ready+over-7-days exports to status='expired' and
// nulls the download_url. Called periodically by a cron (data-purger).
func (s *Store) ExpireOldExports(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_data_exports
        SET status = 'expired', download_url = NULL
        WHERE status = 'ready'
          AND download_expires_at IS NOT NULL
          AND download_expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("expire exports: %w", err)
	}
	return tag.RowsAffected(), nil
}
