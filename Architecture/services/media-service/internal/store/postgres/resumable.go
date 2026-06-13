package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ResumableUpload tracks a multipart/resumable upload session.
type ResumableUpload struct {
	UploadID      uuid.UUID `json:"upload_id"`
	MediaID       uuid.UUID `json:"media_id"`
	UploaderID    uuid.UUID `json:"uploader_id"`
	TotalBytes    int64     `json:"total_bytes"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	ChunkSize     int       `json:"chunk_size"`
	TotalParts    int       `json:"total_parts"`
	Status        string    `json:"status"`
	MimeType      string    `json:"mime_type"`
	ObjectKey     string    `json:"object_key"`
	StorageUploadID string  `json:"storage_upload_id"`
	UploadToken   *string   `json:"upload_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CreateResumableUpload inserts a new resumable upload session.
func (s *MediaAssetStore) CreateResumableUpload(ctx context.Context, u *ResumableUpload) error {
	if u.UploadID == uuid.Nil {
		u.UploadID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO resumable_uploads (upload_id, media_id, uploader_id, total_bytes, chunk_size, total_parts,
		    status, mime_type, object_key, storage_upload_id, upload_token, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
	`, u.UploadID, u.MediaID, u.UploaderID, u.TotalBytes, u.ChunkSize, u.TotalParts,
		u.Status, u.MimeType, u.ObjectKey, u.StorageUploadID, u.UploadToken, u.ExpiresAt)
	return err
}

// GetResumableUpload fetches an upload session by ID.
func (s *MediaAssetStore) GetResumableUpload(ctx context.Context, uploadID uuid.UUID) (*ResumableUpload, error) {
	var u ResumableUpload
	err := s.db.QueryRow(ctx, `
		SELECT upload_id, media_id, uploader_id, total_bytes, uploaded_bytes, chunk_size, total_parts,
		       status, mime_type, object_key, storage_upload_id, upload_token, expires_at, created_at, updated_at
		FROM resumable_uploads WHERE upload_id = $1
	`, uploadID).Scan(
		&u.UploadID, &u.MediaID, &u.UploaderID, &u.TotalBytes, &u.UploadedBytes, &u.ChunkSize, &u.TotalParts,
		&u.Status, &u.MimeType, &u.ObjectKey, &u.StorageUploadID, &u.UploadToken, &u.ExpiresAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateResumableUploadProgress updates bytes uploaded and status.
func (s *MediaAssetStore) UpdateResumableUploadProgress(ctx context.Context, uploadID uuid.UUID, uploadedBytes int64, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE resumable_uploads SET uploaded_bytes = $2, status = $3, updated_at = NOW()
		WHERE upload_id = $1
	`, uploadID, uploadedBytes, status)
	return err
}

// CompleteResumableUpload marks an upload as completed.
func (s *MediaAssetStore) CompleteResumableUpload(ctx context.Context, uploadID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE resumable_uploads SET status = 'completed', uploaded_bytes = total_bytes, updated_at = NOW()
		WHERE upload_id = $1
	`, uploadID)
	return err
}

// CleanupExpiredUploads marks expired upload sessions.
func (s *MediaAssetStore) CleanupExpiredUploads(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE resumable_uploads SET status = 'expired', updated_at = NOW()
		WHERE status IN ('initiated', 'uploading') AND expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UploadPartRecord is one stored part of a resumable multipart upload.
type UploadPartRecord struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
	SizeBytes  int64  `json:"size_bytes"`
}

// RecordUploadPart upserts a part's ETag. Idempotent on (upload_id,
// part_number) so a re-uploaded part simply overwrites the prior ETag.
func (s *MediaAssetStore) RecordUploadPart(ctx context.Context, uploadID uuid.UUID, partNumber int, etag string, sizeBytes int64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO resumable_upload_parts (upload_id, part_number, etag, size_bytes, uploaded_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (upload_id, part_number)
		DO UPDATE SET etag = EXCLUDED.etag, size_bytes = EXCLUDED.size_bytes, uploaded_at = NOW()
	`, uploadID, partNumber, etag, sizeBytes)
	return err
}

// ListUploadParts returns all recorded parts for an upload, ordered by
// part number — the order CompleteMultipartUpload requires.
func (s *MediaAssetStore) ListUploadParts(ctx context.Context, uploadID uuid.UUID) ([]UploadPartRecord, error) {
	rows, err := s.db.Query(ctx, `
		SELECT part_number, etag, size_bytes FROM resumable_upload_parts
		WHERE upload_id = $1 ORDER BY part_number
	`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var parts []UploadPartRecord
	for rows.Next() {
		var p UploadPartRecord
		if err := rows.Scan(&p.PartNumber, &p.ETag, &p.SizeBytes); err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	return parts, rows.Err()
}
