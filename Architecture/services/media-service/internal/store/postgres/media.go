package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MediaAssetStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *MediaAssetStore {
	return &MediaAssetStore{db: db}
}

type MediaAsset struct {
	ID               uuid.UUID      `json:"id"`
	UploaderID       uuid.UUID      `json:"uploader_id"`
	FileType         string         `json:"file_type"`
	MediaSubtype     string         `json:"media_subtype"`
	MimeType         string         `json:"mime_type"`
	FileSizeBytes    int64          `json:"file_size_bytes"`
	StorageBucket    string         `json:"storage_bucket"`
	StorageKey       string         `json:"storage_key"`
	ProcessingStatus string         `json:"processing_status"`
	Width            *int           `json:"width,omitempty"`
	Height           *int           `json:"height,omitempty"`
	DurationSeconds  *int           `json:"duration_seconds,omitempty"`
	Blurhash         *string        `json:"blurhash,omitempty"`
	AltText          string         `json:"alt_text"`
	OriginalURL      *string        `json:"original_url,omitempty"`
	CdnURL           *string        `json:"cdn_url,omitempty"`
	ThumbnailURL     *string        `json:"thumbnail_url,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Variants         []MediaVariant `json:"variants,omitempty"`
}

type MediaVariant struct {
	MediaAssetID uuid.UUID `json:"media_asset_id"`
	Name         string    `json:"variant"`
	Width        *int      `json:"width,omitempty"`
	Height       *int      `json:"height,omitempty"`
	SizeBytes    *int64    `json:"size_bytes,omitempty"`
	Mime         string    `json:"mime"`
	ObjectKey    string    `json:"object_key"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateMedia inserts a new media asset record.
func (s *MediaAssetStore) CreateMedia(ctx context.Context, m *MediaAsset) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status, alt_text, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
	`, m.ID, m.UploaderID, m.FileType, m.MediaSubtype, m.MimeType, m.FileSizeBytes, m.StorageBucket, m.StorageKey, m.ProcessingStatus, m.AltText, m.CreatedAt)
	return err
}

// UpdateStatus sets the media processing status.
func (s *MediaAssetStore) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET processing_status = $1, updated_at = NOW() WHERE id = $2
	`, status, id)
	return err
}

// UpdateMediaMeta sets dimensions, blurhash, and optionally duration.
func (s *MediaAssetStore) UpdateMediaMeta(ctx context.Context, id uuid.UUID, width, height int, blurhash string, durationSeconds *int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET width = $1, height = $2, blurhash = $3, duration_seconds = $4, updated_at = NOW()
		WHERE id = $5
	`, width, height, blurhash, durationSeconds, id)
	return err
}

// GetMedia fetches a single media asset record by ID.
func (s *MediaAssetStore) GetMedia(ctx context.Context, id uuid.UUID) (*MediaAsset, error) {
	var m MediaAsset
	err := s.db.QueryRow(ctx, `
		SELECT id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status,
		       width, height, duration_seconds, blurhash, alt_text, original_url, cdn_url, thumbnail_url, created_at, updated_at
		FROM media_assets WHERE id = $1
	`, id).Scan(
		&m.ID, &m.UploaderID, &m.FileType, &m.MediaSubtype, &m.MimeType, &m.FileSizeBytes, &m.StorageBucket, &m.StorageKey, &m.ProcessingStatus,
		&m.Width, &m.Height, &m.DurationSeconds, &m.Blurhash, &m.AltText, &m.OriginalURL, &m.CdnURL, &m.ThumbnailURL, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetMediaWithVariants fetches media and all its variants.
func (s *MediaAssetStore) GetMediaWithVariants(ctx context.Context, id uuid.UUID) (*MediaAsset, error) {
	m, err := s.GetMedia(ctx, id)
	if err != nil {
		return nil, err
	}
	variants, err := s.GetVariants(ctx, id)
	if err != nil {
		return nil, err
	}
	m.Variants = variants
	return m, nil
}

// InsertVariants batch-inserts variant records for a media asset.
func (s *MediaAssetStore) InsertVariants(ctx context.Context, variants []MediaVariant) error {
	if len(variants) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, v := range variants {
		batch.Queue(`
			INSERT INTO media_variants (media_asset_id, variant, width, height, size_bytes, mime, object_key, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT (media_asset_id, variant) DO UPDATE SET
				width = EXCLUDED.width, height = EXCLUDED.height,
				size_bytes = EXCLUDED.size_bytes, object_key = EXCLUDED.object_key
		`, v.MediaAssetID, v.Name, v.Width, v.Height, v.SizeBytes, v.Mime, v.ObjectKey)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	for range variants {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// GetVariants returns all variants for a media asset.
func (s *MediaAssetStore) GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]MediaVariant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT media_asset_id, variant, width, height, size_bytes, mime, object_key, created_at
		FROM media_variants WHERE media_asset_id = $1
		ORDER BY variant
	`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []MediaVariant
	for rows.Next() {
		var v MediaVariant
		if err := rows.Scan(&v.MediaAssetID, &v.Name, &v.Width, &v.Height, &v.SizeBytes, &v.Mime, &v.ObjectKey, &v.CreatedAt); err != nil {
			return nil, err
		}
		variants = append(variants, v)
	}
	return variants, nil
}

// GetMediaBatch fetches multiple media asset records with their variants.
func (s *MediaAssetStore) GetMediaBatch(ctx context.Context, ids []uuid.UUID) ([]MediaAsset, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status,
		       width, height, duration_seconds, blurhash, alt_text, original_url, cdn_url, thumbnail_url, created_at, updated_at
		FROM media_assets WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mediaMap := make(map[uuid.UUID]*MediaAsset)
	var result []MediaAsset
	for rows.Next() {
		var m MediaAsset
		if err := rows.Scan(
			&m.ID, &m.UploaderID, &m.FileType, &m.MediaSubtype, &m.MimeType, &m.FileSizeBytes, &m.StorageBucket, &m.StorageKey, &m.ProcessingStatus,
			&m.Width, &m.Height, &m.DurationSeconds, &m.Blurhash, &m.AltText, &m.OriginalURL, &m.CdnURL, &m.ThumbnailURL, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, m)
		mediaMap[m.ID] = &result[len(result)-1]
	}

	// Batch-load variants
	vRows, err := s.db.Query(ctx, `
		SELECT media_asset_id, variant, width, height, size_bytes, mime, object_key, created_at
		FROM media_variants WHERE media_asset_id = ANY($1)
		ORDER BY media_asset_id, variant
	`, ids)
	if err != nil {
		return result, nil // Return media without variants on error
	}
	defer vRows.Close()

	for vRows.Next() {
		var v MediaVariant
		if err := vRows.Scan(&v.MediaAssetID, &v.Name, &v.Width, &v.Height, &v.SizeBytes, &v.Mime, &v.ObjectKey, &v.CreatedAt); err != nil {
			continue
		}
		if m, ok := mediaMap[v.MediaAssetID]; ok {
			m.Variants = append(m.Variants, v)
		}
	}

	return result, nil
}

// ─── Transcoding Jobs ──────────────────────────────────────────────

type TranscodingJob struct {
	ID              uuid.UUID  `json:"id"`
	MediaAssetID    uuid.UUID  `json:"media_asset_id"`
	TargetQuality   string     `json:"target_quality"`
	Status          string     `json:"status"`
	OutputURL       *string    `json:"output_url,omitempty"`
	OutputSizeBytes *int64     `json:"output_size_bytes,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CreateTranscodingJob inserts a new transcoding job record.
func (s *MediaAssetStore) CreateTranscodingJob(ctx context.Context, job *TranscodingJob) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO transcoding_jobs (id, media_asset_id, target_quality, status, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, job.ID, job.MediaAssetID, job.TargetQuality, job.Status)
	return err
}

// UpdateTranscodingJob updates the status and optional fields of a transcoding job.
func (s *MediaAssetStore) UpdateTranscodingJob(ctx context.Context, jobID uuid.UUID, status string, outputURL *string, outputSizeBytes *int64, errorMessage *string) error {
	var completedAt, startedAt interface{}
	if status == "processing" {
		now := time.Now()
		startedAt = &now
	}
	if status == "completed" || status == "failed" {
		now := time.Now()
		completedAt = &now
	}

	_, err := s.db.Exec(ctx, `
		UPDATE transcoding_jobs SET
			status = $1,
			output_url = COALESCE($2, output_url),
			output_size_bytes = COALESCE($3, output_size_bytes),
			error_message = COALESCE($4, error_message),
			started_at = COALESCE($5, started_at),
			completed_at = COALESCE($6, completed_at)
		WHERE id = $7
	`, status, outputURL, outputSizeBytes, errorMessage, startedAt, completedAt, jobID)
	return err
}

// GetTranscodingJobs returns all transcoding jobs for a media asset.
func (s *MediaAssetStore) GetTranscodingJobs(ctx context.Context, mediaAssetID uuid.UUID) ([]TranscodingJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_asset_id, target_quality, status, output_url, output_size_bytes,
		       started_at, completed_at, error_message, created_at
		FROM transcoding_jobs WHERE media_asset_id = $1
		ORDER BY created_at
	`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []TranscodingJob
	for rows.Next() {
		var j TranscodingJob
		if err := rows.Scan(
			&j.ID, &j.MediaAssetID, &j.TargetQuality, &j.Status,
			&j.OutputURL, &j.OutputSizeBytes, &j.StartedAt, &j.CompletedAt,
			&j.ErrorMessage, &j.CreatedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// ─── Delete ────────────────────────────────────────────────────────

// DeleteMedia removes a media asset and all its variants and transcoding jobs.
// Returns the list of object keys that were associated (for blob cleanup).
func (s *MediaAssetStore) DeleteMedia(ctx context.Context, id uuid.UUID) ([]string, error) {
	// 1. Collect all object keys for blob cleanup
	var objectKeys []string

	var storageKey string
	err := s.db.QueryRow(ctx, `SELECT storage_key FROM media_assets WHERE id = $1`, id).Scan(&storageKey)
	if err != nil {
		return nil, fmt.Errorf("fetch media storage_key: %w", err)
	}
	objectKeys = append(objectKeys, storageKey)

	rows, err := s.db.Query(ctx, `SELECT object_key FROM media_variants WHERE media_asset_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("fetch variant keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		objectKeys = append(objectKeys, key)
	}

	// 2. Delete in correct FK order within a transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM transcoding_jobs WHERE media_asset_id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete transcoding_jobs: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_variants WHERE media_asset_id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete variants: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete media_asset: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return objectKeys, nil
}

// ─── URL Population ────────────────────────────────────────────────

// UpdateMediaURLs sets the original_url, cdn_url, and thumbnail_url for a media asset.
func (s *MediaAssetStore) UpdateMediaURLs(ctx context.Context, id uuid.UUID, originalURL, cdnURL, thumbnailURL *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET original_url = $1, cdn_url = $2, thumbnail_url = $3, updated_at = NOW()
		WHERE id = $4
	`, originalURL, cdnURL, thumbnailURL, id)
	return err
}
