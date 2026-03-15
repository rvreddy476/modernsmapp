package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DataExportRequest represents a user's data export request.
type DataExportRequest struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	Status        string     `json:"status"`
	DownloadURL   string     `json:"download_url,omitempty"`
	FileSizeBytes *int64     `json:"file_size_bytes,omitempty"`
	RequestedAt   time.Time  `json:"requested_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// CreateExportRequest inserts a new data export request with status 'queued'.
func (s *Store) CreateExportRequest(ctx context.Context, userID uuid.UUID) (*DataExportRequest, error) {
	req := &DataExportRequest{
		ID:     uuid.New(),
		UserID: userID,
		Status: "queued",
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO data_export_requests (id, user_id, status, requested_at)
		VALUES ($1, $2, 'queued', NOW())
		RETURNING requested_at
	`, req.ID, req.UserID).Scan(&req.RequestedAt)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// GetExportRequest returns a data export request by ID.
func (s *Store) GetExportRequest(ctx context.Context, id uuid.UUID) (*DataExportRequest, error) {
	return scanExportRequest(s.db.QueryRow(ctx, `
		SELECT id, user_id, status, COALESCE(download_url,''), file_size_bytes, requested_at, completed_at, expires_at
		FROM data_export_requests WHERE id = $1
	`, id))
}

// GetLatestExportRequest returns the most recent export request for a user.
func (s *Store) GetLatestExportRequest(ctx context.Context, userID uuid.UUID) (*DataExportRequest, error) {
	return scanExportRequest(s.db.QueryRow(ctx, `
		SELECT id, user_id, status, COALESCE(download_url,''), file_size_bytes, requested_at, completed_at, expires_at
		FROM data_export_requests
		WHERE user_id = $1
		ORDER BY requested_at DESC
		LIMIT 1
	`, userID))
}

// UpdateExportStatus updates the status, download_url, and file_size_bytes of an export request.
func (s *Store) UpdateExportStatus(ctx context.Context, id uuid.UUID, status, downloadURL string, fileSizeBytes *int64) error {
	var completedAt *time.Time
	if status == "ready" || status == "downloaded" || status == "expired" {
		now := time.Now()
		completedAt = &now
	}
	_, err := s.db.Exec(ctx, `
		UPDATE data_export_requests
		SET status = $1, download_url = $2, file_size_bytes = $3, completed_at = $4
		WHERE id = $5
	`, status, downloadURL, fileSizeBytes, completedAt, id)
	return err
}

func scanExportRequest(row pgx.Row) (*DataExportRequest, error) {
	var r DataExportRequest
	err := row.Scan(&r.ID, &r.UserID, &r.Status, &r.DownloadURL, &r.FileSizeBytes, &r.RequestedAt, &r.CompletedAt, &r.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}
