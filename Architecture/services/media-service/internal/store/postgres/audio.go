package postgres

import (
	"context"
	"encoding/json"
	"errors"
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

// ─── Audio Library (audio_library table) ───────────────────────────

// AudioLibraryTrack is a track in the curated audio library used by Flicks.
type AudioLibraryTrack struct {
	ID           uuid.UUID       `json:"id"`
	Title        string          `json:"title"`
	Artist       string          `json:"artist"`
	DurationMs   int             `json:"duration_ms"`
	WaveformData json.RawMessage `json:"waveform_data,omitempty"`
	CoverURL     *string         `json:"cover_url,omitempty"`
	AudioURL     string          `json:"audio_url"`
	Source       string          `json:"source"`
	SourcePostID *uuid.UUID      `json:"source_post_id,omitempty"`
	SourceUserID *uuid.UUID      `json:"source_user_id,omitempty"`
	UsageCount   int64           `json:"usage_count"`
	IsTrending   bool            `json:"is_trending"`
	IsLicensed   bool            `json:"is_licensed"`
	Language     string          `json:"language"`
	Genre        *string         `json:"genre,omitempty"`
	Mood         *string         `json:"mood,omitempty"`
	IsActive     bool            `json:"is_active"`
	CreatedAt    time.Time       `json:"created_at"`
}

const audioLibrarySelectCols = `id, title, artist, duration_ms, waveform_data, cover_url, audio_url,
    source, source_post_id, source_user_id, usage_count, is_trending, is_licensed,
    language, genre, mood, is_active, created_at`

func scanAudioLibraryTrack(row pgx.Row, t *AudioLibraryTrack) error {
	return row.Scan(
		&t.ID, &t.Title, &t.Artist, &t.DurationMs, &t.WaveformData, &t.CoverURL, &t.AudioURL,
		&t.Source, &t.SourcePostID, &t.SourceUserID, &t.UsageCount, &t.IsTrending, &t.IsLicensed,
		&t.Language, &t.Genre, &t.Mood, &t.IsActive, &t.CreatedAt,
	)
}

func scanAudioLibraryRows(rows pgx.Rows) ([]AudioLibraryTrack, error) {
	var tracks []AudioLibraryTrack
	for rows.Next() {
		var t AudioLibraryTrack
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Artist, &t.DurationMs, &t.WaveformData, &t.CoverURL, &t.AudioURL,
			&t.Source, &t.SourcePostID, &t.SourceUserID, &t.UsageCount, &t.IsTrending, &t.IsLicensed,
			&t.Language, &t.Genre, &t.Mood, &t.IsActive, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	return tracks, rows.Err()
}

// GetAudioLibraryTrack returns a single active track from audio_library.
func (s *MediaAssetStore) GetAudioLibraryTrack(ctx context.Context, id uuid.UUID) (*AudioLibraryTrack, error) {
	t := &AudioLibraryTrack{}
	err := scanAudioLibraryTrack(
		s.db.QueryRow(ctx, `SELECT `+audioLibrarySelectCols+` FROM audio_library WHERE id = $1 AND is_active = TRUE`, id),
		t,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// CreateAudioLibraryTrack inserts a new audio_library record.
func (s *MediaAssetStore) CreateAudioLibraryTrack(ctx context.Context, t *AudioLibraryTrack) (*AudioLibraryTrack, error) {
	err := scanAudioLibraryTrack(
		s.db.QueryRow(ctx, `
			INSERT INTO audio_library (title, artist, duration_ms, waveform_data, cover_url, audio_url,
			    source, source_post_id, source_user_id, is_licensed, language, genre, mood)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING `+audioLibrarySelectCols,
			t.Title, t.Artist, t.DurationMs, t.WaveformData, t.CoverURL, t.AudioURL,
			t.Source, t.SourcePostID, t.SourceUserID, t.IsLicensed, t.Language, t.Genre, t.Mood,
		),
		t,
	)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetTrendingAudioLibrary returns tracks ordered by usage_count, optionally filtered by genre.
func (s *MediaAssetStore) GetTrendingAudioLibrary(ctx context.Context, genre *string, limit, offset int) ([]AudioLibraryTrack, error) {
	var rows pgx.Rows
	var err error
	if genre != nil && *genre != "" {
		rows, err = s.db.Query(ctx, `
			SELECT `+audioLibrarySelectCols+`
			FROM audio_library
			WHERE is_active = TRUE AND genre = $3
			ORDER BY usage_count DESC LIMIT $1 OFFSET $2`,
			limit, offset, *genre)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+audioLibrarySelectCols+`
			FROM audio_library
			WHERE is_active = TRUE
			ORDER BY usage_count DESC LIMIT $1 OFFSET $2`,
			limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioLibraryRows(rows)
}

// SearchAudioLibrary searches audio_library by title or artist ILIKE.
func (s *MediaAssetStore) SearchAudioLibrary(ctx context.Context, query string, limit, offset int) ([]AudioLibraryTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+audioLibrarySelectCols+`
		FROM audio_library
		WHERE is_active = TRUE AND (title ILIKE $3 OR artist ILIKE $3)
		ORDER BY usage_count DESC LIMIT $1 OFFSET $2`,
		limit, offset, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioLibraryRows(rows)
}

// IncrementAudioLibraryUsage bumps usage_count by 1 for a library track.
func (s *MediaAssetStore) IncrementAudioLibraryUsage(ctx context.Context, audioID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE audio_library SET usage_count = usage_count + 1 WHERE id = $1`, audioID)
	return err
}

// AddPostAudioRef records that a post uses a library audio track (idempotent).
func (s *MediaAssetStore) AddPostAudioRef(ctx context.Context, audioID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO post_audio_refs (audio_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		audioID, postID)
	return err
}
