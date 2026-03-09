package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MediaRendition tracks a single output rendition for a media asset.
type MediaRendition struct {
	ID             uuid.UUID  `json:"id"`
	MediaID        uuid.UUID  `json:"media_id"`
	RenditionType  string     `json:"rendition_type"`  // video, thumbnail, preview_gif, sprite_sheet, audio, waveform, hls_variant, hls_segment
	Quality        string     `json:"quality"`          // 360p, 720p, 1080p, thumb_150, preview, master, audio_aac
	ObjectKey      *string    `json:"object_key,omitempty"`
	MimeType       *string    `json:"mime_type,omitempty"`
	Width          *int       `json:"width,omitempty"`
	Height         *int       `json:"height,omitempty"`
	SizeBytes      *int64     `json:"size_bytes,omitempty"`
	DurationMs     *int       `json:"duration_ms,omitempty"`
	Status         string     `json:"status"`
	RetryCount     int        `json:"retry_count"`
	MaxRetries     int        `json:"max_retries"`
	ErrorMessage   *string    `json:"error_message,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreateRendition inserts a new rendition record in pending status.
func (s *MediaAssetStore) CreateRendition(ctx context.Context, r *MediaRendition) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_renditions (id, media_id, rendition_type, quality, status, max_retries, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (media_id, rendition_type, quality) DO NOTHING
	`, r.ID, r.MediaID, r.RenditionType, r.Quality, r.Status, r.MaxRetries)
	return err
}

// UpdateRenditionProcessing marks a rendition as processing.
func (s *MediaAssetStore) UpdateRenditionProcessing(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_renditions SET status = 'processing', started_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

// UpdateRenditionCompleted marks a rendition as completed with output details.
func (s *MediaAssetStore) UpdateRenditionCompleted(ctx context.Context, id uuid.UUID, objectKey, mimeType string, width, height *int, sizeBytes *int64, durationMs *int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_renditions SET
			status = 'completed', object_key = $2, mime_type = $3,
			width = $4, height = $5, size_bytes = $6, duration_ms = $7,
			completed_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, id, objectKey, mimeType, width, height, sizeBytes, durationMs)
	return err
}

// UpdateRenditionFailed marks a rendition as failed, incrementing retry_count.
// If retry_count < max_retries, sets status to 'retrying' instead.
func (s *MediaAssetStore) UpdateRenditionFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_renditions SET
			retry_count = retry_count + 1,
			error_message = $2,
			status = CASE WHEN retry_count + 1 < max_retries THEN 'retrying' ELSE 'failed' END,
			updated_at = NOW()
		WHERE id = $1
	`, id, errMsg)
	return err
}

// GetRenditionsByMedia returns all renditions for a media asset.
func (s *MediaAssetStore) GetRenditionsByMedia(ctx context.Context, mediaID uuid.UUID) ([]MediaRendition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_id, rendition_type, quality, object_key, mime_type,
		       width, height, size_bytes, duration_ms, status, retry_count, max_retries,
		       error_message, started_at, completed_at, created_at, updated_at
		FROM media_renditions WHERE media_id = $1
		ORDER BY rendition_type, quality
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRenditions(rows)
}

// GetPendingRenditions returns renditions that need processing (pending or retrying).
func (s *MediaAssetStore) GetPendingRenditions(ctx context.Context, limit int) ([]MediaRendition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_id, rendition_type, quality, object_key, mime_type,
		       width, height, size_bytes, duration_ms, status, retry_count, max_retries,
		       error_message, started_at, completed_at, created_at, updated_at
		FROM media_renditions
		WHERE status IN ('pending', 'retrying')
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRenditions(rows)
}

// AreAllRenditionsReady checks if all renditions for a media asset are completed.
func (s *MediaAssetStore) AreAllRenditionsReady(ctx context.Context, mediaID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM media_renditions
		WHERE media_id = $1 AND status NOT IN ('completed', 'failed')
	`, mediaID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func scanRenditions(rows pgx.Rows) ([]MediaRendition, error) {
	var renditions []MediaRendition
	for rows.Next() {
		var r MediaRendition
		if err := rows.Scan(
			&r.ID, &r.MediaID, &r.RenditionType, &r.Quality, &r.ObjectKey, &r.MimeType,
			&r.Width, &r.Height, &r.SizeBytes, &r.DurationMs, &r.Status, &r.RetryCount, &r.MaxRetries,
			&r.ErrorMessage, &r.StartedAt, &r.CompletedAt, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		renditions = append(renditions, r)
	}
	return renditions, nil
}
