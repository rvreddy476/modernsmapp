package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AudioTrack represents an audio/music track used in posts and reels.
type AudioTrack struct {
	ID             uuid.UUID  `json:"id"`
	Title          string     `json:"title"`
	Artist         string     `json:"artist"`
	DurationMs     int        `json:"duration_ms"`
	MediaID        uuid.UUID  `json:"media_id"`
	OriginalPostID *uuid.UUID `json:"original_post_id,omitempty"`
	Genre          string     `json:"genre"`
	IsOriginal     bool       `json:"is_original"`
	UseCount       int        `json:"use_count"`
	IsTrending     bool       `json:"is_trending"`
	CreatedAt      time.Time  `json:"created_at"`
	// M10 audio-track ownership. IsPublic defaults true (existing
	// reuse-by-default UX). CreatorUserID identifies the rights
	// owner — anyone can use a public track in their own post; a
	// private track requires CreatorUserID == actor.
	IsPublic       bool       `json:"is_public"`
	CreatorUserID  *uuid.UUID `json:"creator_user_id,omitempty"`
}

// CreateAudioTrack inserts a new audio track record.
func (s *Store) CreateAudioTrack(ctx context.Context, t *AudioTrack) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO audio_tracks (id, title, artist, duration_ms, media_id, original_post_id, genre, is_original, use_count, is_trending, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, t.ID, t.Title, t.Artist, t.DurationMs, t.MediaID, t.OriginalPostID, t.Genre, t.IsOriginal, t.UseCount, t.IsTrending, t.CreatedAt)
	return err
}

// GetAudioTrack retrieves an audio track by ID. Includes the M10
// ownership columns; COALESCE on is_public so pre-migration rows
// (which may have NULL) are treated as public.
func (s *Store) GetAudioTrack(ctx context.Context, id uuid.UUID) (*AudioTrack, error) {
	var t AudioTrack
	err := s.db.QueryRow(ctx, `
		SELECT id, title, artist, duration_ms, media_id, original_post_id, genre, is_original, use_count, is_trending, created_at,
		       COALESCE(is_public, TRUE), creator_user_id
		FROM audio_tracks WHERE id = $1
	`, id).Scan(
		&t.ID, &t.Title, &t.Artist, &t.DurationMs, &t.MediaID, &t.OriginalPostID,
		&t.Genre, &t.IsOriginal, &t.UseCount, &t.IsTrending, &t.CreatedAt,
		&t.IsPublic, &t.CreatorUserID,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTrendingAudio returns trending audio tracks ordered by use count.
func (s *Store) GetTrendingAudio(ctx context.Context, limit int) ([]AudioTrack, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, title, artist, duration_ms, media_id, original_post_id, genre, is_original, use_count, is_trending, created_at
		FROM audio_tracks
		WHERE is_trending = true
		ORDER BY use_count DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioTracks(rows)
}

// GetAudioByPost returns the audio track associated with a post.
func (s *Store) GetAudioByPost(ctx context.Context, postID uuid.UUID) (*AudioTrack, error) {
	var t AudioTrack
	err := s.db.QueryRow(ctx, `
		SELECT id, title, artist, duration_ms, media_id, original_post_id, genre, is_original, use_count, is_trending, created_at
		FROM audio_tracks WHERE original_post_id = $1
	`, postID).Scan(
		&t.ID, &t.Title, &t.Artist, &t.DurationMs, &t.MediaID, &t.OriginalPostID,
		&t.Genre, &t.IsOriginal, &t.UseCount, &t.IsTrending, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// IncrementAudioUseCount atomically increments the use_count of an audio track.
func (s *Store) IncrementAudioUseCount(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET use_count = use_count + 1 WHERE id = $1
	`, id)
	return err
}

// SearchAudio searches audio tracks by title or artist using ILIKE.
func (s *Store) SearchAudio(ctx context.Context, query string, limit int) ([]AudioTrack, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(ctx, `
		SELECT id, title, artist, duration_ms, media_id, original_post_id, genre, is_original, use_count, is_trending, created_at
		FROM audio_tracks
		WHERE title ILIKE $1 OR artist ILIKE $1
		ORDER BY use_count DESC
		LIMIT $2
	`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioTracks(rows)
}

// AttachAudioToPost sets the audio_track_id on a post.
func (s *Store) AttachAudioToPost(ctx context.Context, postID, audioTrackID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE posts SET audio_track_id = $1, updated_at = NOW() WHERE id = $2
	`, audioTrackID, postID)
	return err
}

func scanAudioTracks(rows pgx.Rows) ([]AudioTrack, error) {
	var tracks []AudioTrack
	for rows.Next() {
		var t AudioTrack
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Artist, &t.DurationMs, &t.MediaID, &t.OriginalPostID,
			&t.Genre, &t.IsOriginal, &t.UseCount, &t.IsTrending, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	return tracks, rows.Err()
}
