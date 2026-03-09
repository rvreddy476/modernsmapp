package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AudioTrack represents an extracted or uploaded audio track for the music/sound system.
type AudioTrack struct {
	ID            uuid.UUID  `json:"id"`
	SourceMediaID *uuid.UUID `json:"source_media_id,omitempty"`
	SourceReelID  *uuid.UUID `json:"source_reel_id,omitempty"`
	Title         string     `json:"title"`
	Artist        string     `json:"artist"`
	Genre         *string    `json:"genre,omitempty"`
	AudioKey      string     `json:"audio_key"`
	WaveformKey   *string    `json:"waveform_key,omitempty"`
	DurationMs    int        `json:"duration_ms"`
	SampleRate    *int       `json:"sample_rate,omitempty"`
	Status        string     `json:"status"`
	IsOriginal    bool       `json:"is_original"`
	LicenseType   string     `json:"license_type"`
	UsageCount    int        `json:"usage_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// CreateAudioTrack inserts a new audio track record.
func (s *MediaAssetStore) CreateAudioTrack(ctx context.Context, a *AudioTrack) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO audio_tracks (id, source_media_id, source_reel_id, title, artist, genre,
		    audio_key, waveform_key, duration_ms, sample_rate, status, is_original, license_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())
	`, a.ID, a.SourceMediaID, a.SourceReelID, a.Title, a.Artist, a.Genre,
		a.AudioKey, a.WaveformKey, a.DurationMs, a.SampleRate, a.Status, a.IsOriginal, a.LicenseType)
	return err
}

// GetAudioTrack returns a single audio track by ID.
func (s *MediaAssetStore) GetAudioTrack(ctx context.Context, id uuid.UUID) (*AudioTrack, error) {
	var a AudioTrack
	err := s.db.QueryRow(ctx, `
		SELECT id, source_media_id, source_reel_id, title, artist, genre,
		       audio_key, waveform_key, duration_ms, sample_rate, status, is_original,
		       license_type, usage_count, created_at, updated_at
		FROM audio_tracks WHERE id = $1
	`, id).Scan(
		&a.ID, &a.SourceMediaID, &a.SourceReelID, &a.Title, &a.Artist, &a.Genre,
		&a.AudioKey, &a.WaveformKey, &a.DurationMs, &a.SampleRate, &a.Status, &a.IsOriginal,
		&a.LicenseType, &a.UsageCount, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetAudioTrackByMedia returns the audio track extracted from a specific media asset.
func (s *MediaAssetStore) GetAudioTrackByMedia(ctx context.Context, mediaID uuid.UUID) (*AudioTrack, error) {
	var a AudioTrack
	err := s.db.QueryRow(ctx, `
		SELECT id, source_media_id, source_reel_id, title, artist, genre,
		       audio_key, waveform_key, duration_ms, sample_rate, status, is_original,
		       license_type, usage_count, created_at, updated_at
		FROM audio_tracks WHERE source_media_id = $1
		LIMIT 1
	`, mediaID).Scan(
		&a.ID, &a.SourceMediaID, &a.SourceReelID, &a.Title, &a.Artist, &a.Genre,
		&a.AudioKey, &a.WaveformKey, &a.DurationMs, &a.SampleRate, &a.Status, &a.IsOriginal,
		&a.LicenseType, &a.UsageCount, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAudioTrackStatus sets the status of an audio track.
func (s *MediaAssetStore) UpdateAudioTrackStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET status = $1, updated_at = NOW() WHERE id = $2
	`, status, id)
	return err
}

// UpdateAudioTrackWaveform sets the waveform_key after waveform generation.
func (s *MediaAssetStore) UpdateAudioTrackWaveform(ctx context.Context, id uuid.UUID, waveformKey string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET waveform_key = $1, updated_at = NOW() WHERE id = $2
	`, waveformKey, id)
	return err
}

// GetTrendingAudioTracks returns audio tracks ordered by usage count (snapshot-based).
func (s *MediaAssetStore) GetTrendingAudioTracks(ctx context.Context, limit, offset int) ([]AudioTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_media_id, source_reel_id, title, artist, genre,
		       audio_key, waveform_key, duration_ms, sample_rate, status, is_original,
		       license_type, usage_count, created_at, updated_at
		FROM audio_tracks
		WHERE status = 'ready'
		ORDER BY usage_count DESC, created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioTracks(rows)
}

// SearchAudioTracks searches audio tracks by title or artist.
func (s *MediaAssetStore) SearchAudioTracks(ctx context.Context, query string, limit, offset int) ([]AudioTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_media_id, source_reel_id, title, artist, genre,
		       audio_key, waveform_key, duration_ms, sample_rate, status, is_original,
		       license_type, usage_count, created_at, updated_at
		FROM audio_tracks
		WHERE status = 'ready'
		  AND (title ILIKE '%' || $1 || '%' OR artist ILIKE '%' || $1 || '%')
		ORDER BY usage_count DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioTracks(rows)
}

// IncrementAudioUsageCount bumps usage_count by 1 (snapshot — not the truth source).
func (s *MediaAssetStore) IncrementAudioUsageCount(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET usage_count = usage_count + 1, updated_at = NOW() WHERE id = $1
	`, id)
	return err
}

func scanAudioTracks(rows pgx.Rows) ([]AudioTrack, error) {
	var tracks []AudioTrack
	for rows.Next() {
		var a AudioTrack
		if err := rows.Scan(
			&a.ID, &a.SourceMediaID, &a.SourceReelID, &a.Title, &a.Artist, &a.Genre,
			&a.AudioKey, &a.WaveformKey, &a.DurationMs, &a.SampleRate, &a.Status, &a.IsOriginal,
			&a.LicenseType, &a.UsageCount, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		tracks = append(tracks, a)
	}
	return tracks, nil
}
