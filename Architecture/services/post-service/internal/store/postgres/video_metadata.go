package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// VideoMetadata represents the video_metadata table row.
type VideoMetadata struct {
	PostID           uuid.UUID  `json:"post_id"`
	DurationSeconds  float64    `json:"duration_seconds"`
	Width            *int       `json:"width,omitempty"`
	Height           *int       `json:"height,omitempty"`
	AspectRatio      *string    `json:"aspect_ratio,omitempty"`
	Orientation      string     `json:"orientation"`
	FileSizeBytes    *int64     `json:"file_size_bytes,omitempty"`
	MimeType         *string    `json:"mime_type,omitempty"`
	CodecVideo       *string    `json:"codec_video,omitempty"`
	CodecAudio       *string    `json:"codec_audio,omitempty"`
	FrameRate        *float64   `json:"frame_rate,omitempty"`
	StorageVideoURL  *string    `json:"storage_video_url,omitempty"`
	PlaybackURL      *string    `json:"playback_url,omitempty"`
	ThumbnailURL     *string    `json:"thumbnail_url,omitempty"`
	TrimStartMs      int        `json:"trim_start_ms"`
	TrimEndMs        *int       `json:"trim_end_ms,omitempty"`
	ComputedCategory string     `json:"computed_category"`
	FinalCategory    string     `json:"final_category"`
	UploadStatus     string     `json:"upload_status"`
	MediaAssetID     *uuid.UUID `json:"media_asset_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const videoMetaCols = `post_id, duration_seconds, width, height, aspect_ratio, orientation,
	file_size_bytes, mime_type, codec_video, codec_audio, frame_rate,
	storage_video_url, playback_url, thumbnail_url,
	trim_start_ms, trim_end_ms, computed_category, final_category,
	upload_status, media_asset_id, created_at, updated_at`

func scanVideoMetadata(row pgx.Row) (*VideoMetadata, error) {
	var vm VideoMetadata
	err := row.Scan(
		&vm.PostID, &vm.DurationSeconds, &vm.Width, &vm.Height, &vm.AspectRatio, &vm.Orientation,
		&vm.FileSizeBytes, &vm.MimeType, &vm.CodecVideo, &vm.CodecAudio, &vm.FrameRate,
		&vm.StorageVideoURL, &vm.PlaybackURL, &vm.ThumbnailURL,
		&vm.TrimStartMs, &vm.TrimEndMs, &vm.ComputedCategory, &vm.FinalCategory,
		&vm.UploadStatus, &vm.MediaAssetID, &vm.CreatedAt, &vm.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &vm, nil
}

// CreateVideoMetadata inserts a new video_metadata row.
func (s *Store) CreateVideoMetadata(ctx context.Context, vm *VideoMetadata) error {
	now := time.Now()
	vm.CreatedAt = now
	vm.UpdatedAt = now

	_, err := s.db.Exec(ctx, `
		INSERT INTO video_metadata (
			post_id, duration_seconds, width, height, aspect_ratio, orientation,
			file_size_bytes, mime_type, codec_video, codec_audio, frame_rate,
			storage_video_url, playback_url, thumbnail_url,
			trim_start_ms, trim_end_ms, computed_category, final_category,
			upload_status, media_asset_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
	`, vm.PostID, vm.DurationSeconds, vm.Width, vm.Height, vm.AspectRatio, vm.Orientation,
		vm.FileSizeBytes, vm.MimeType, vm.CodecVideo, vm.CodecAudio, vm.FrameRate,
		vm.StorageVideoURL, vm.PlaybackURL, vm.ThumbnailURL,
		vm.TrimStartMs, vm.TrimEndMs, vm.ComputedCategory, vm.FinalCategory,
		vm.UploadStatus, vm.MediaAssetID, vm.CreatedAt, vm.UpdatedAt,
	)
	return err
}

// GetVideoMetadata returns the video_metadata row for a given post.
func (s *Store) GetVideoMetadata(ctx context.Context, postID uuid.UUID) (*VideoMetadata, error) {
	row := s.db.QueryRow(ctx, `SELECT `+videoMetaCols+` FROM video_metadata WHERE post_id = $1`, postID)
	return scanVideoMetadata(row)
}

// UpdateVideoMetadata updates mutable fields on video_metadata.
func (s *Store) UpdateVideoMetadata(ctx context.Context, vm *VideoMetadata) error {
	vm.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		UPDATE video_metadata SET
			duration_seconds=$2, width=$3, height=$4, aspect_ratio=$5, orientation=$6,
			file_size_bytes=$7, mime_type=$8, codec_video=$9, codec_audio=$10, frame_rate=$11,
			storage_video_url=$12, playback_url=$13, thumbnail_url=$14,
			trim_start_ms=$15, trim_end_ms=$16, computed_category=$17, final_category=$18,
			upload_status=$19, media_asset_id=$20, updated_at=$21
		WHERE post_id = $1
	`, vm.PostID, vm.DurationSeconds, vm.Width, vm.Height, vm.AspectRatio, vm.Orientation,
		vm.FileSizeBytes, vm.MimeType, vm.CodecVideo, vm.CodecAudio, vm.FrameRate,
		vm.StorageVideoURL, vm.PlaybackURL, vm.ThumbnailURL,
		vm.TrimStartMs, vm.TrimEndMs, vm.ComputedCategory, vm.FinalCategory,
		vm.UploadStatus, vm.MediaAssetID, vm.UpdatedAt,
	)
	return err
}

// UpdateUploadStatus sets the upload_status field.
func (s *Store) UpdateUploadStatus(ctx context.Context, postID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE video_metadata SET upload_status = $2, updated_at = NOW() WHERE post_id = $1
	`, postID, status)
	return err
}

// UpdateFinalCategory sets the final_category and updates posts.content_type to match.
func (s *Store) UpdateFinalCategory(ctx context.Context, postID uuid.UUID, category string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE video_metadata SET final_category = $2, updated_at = NOW() WHERE post_id = $1
	`, postID, category); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE posts SET content_type = $2, updated_at = NOW() WHERE id = $1
	`, postID, category); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// UpdateTrim sets trim_start_ms and trim_end_ms.
func (s *Store) UpdateTrim(ctx context.Context, postID uuid.UUID, startMs int, endMs *int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE video_metadata SET trim_start_ms = $2, trim_end_ms = $3, updated_at = NOW() WHERE post_id = $1
	`, postID, startMs, endMs)
	return err
}

// BatchGetVideoMetadata returns video_metadata rows for a set of post IDs.
func (s *Store) BatchGetVideoMetadata(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*VideoMetadata, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `SELECT `+videoMetaCols+` FROM video_metadata WHERE post_id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uuid.UUID]*VideoMetadata, len(postIDs))
	for rows.Next() {
		var vm VideoMetadata
		if err := rows.Scan(
			&vm.PostID, &vm.DurationSeconds, &vm.Width, &vm.Height, &vm.AspectRatio, &vm.Orientation,
			&vm.FileSizeBytes, &vm.MimeType, &vm.CodecVideo, &vm.CodecAudio, &vm.FrameRate,
			&vm.StorageVideoURL, &vm.PlaybackURL, &vm.ThumbnailURL,
			&vm.TrimStartMs, &vm.TrimEndMs, &vm.ComputedCategory, &vm.FinalCategory,
			&vm.UploadStatus, &vm.MediaAssetID, &vm.CreatedAt, &vm.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result[vm.PostID] = &vm
	}
	return result, nil
}

// GetVideoMetadataByMediaAsset looks up video_metadata by media_asset_id.
func (s *Store) GetVideoMetadataByMediaAsset(ctx context.Context, mediaAssetID uuid.UUID) (*VideoMetadata, error) {
	row := s.db.QueryRow(ctx, `SELECT `+videoMetaCols+` FROM video_metadata WHERE media_asset_id = $1`, mediaAssetID)
	return scanVideoMetadata(row)
}

// ResolveMediaDimensions returns width and height from the media_assets table.
func (s *Store) ResolveMediaDimensions(ctx context.Context, mediaID uuid.UUID) (width, height int, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(width, 0), COALESCE(height, 0) FROM media_assets WHERE id = $1
	`, mediaID).Scan(&width, &height)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve media dimensions: %w", err)
	}
	return width, height, nil
}
