package postgres

import (
	"context"

	"github.com/google/uuid"
)

// AudioTrack is a sound as post-service needs it: enough to decide whether
// a post may play it. The catalogue is media-service's (it creates the rows
// and lists them); both services read the one audio_tracks table, which
// carries the columns of both shapes, so every column read here may be NULL
// on a row the other service wrote.
type AudioTrack struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Artist     string    `json:"artist"`
	DurationMs int       `json:"duration_ms"`
	// MediaID is the asset that answers for the sound's audience: the video
	// it was extracted from (source_media_id), or the uploaded track
	// (media_id). Nil when the row names neither.
	MediaID *uuid.UUID `json:"media_id,omitempty"`
	// Status is media-service's: processing, ready, rejected, deleted.
	Status   string `json:"status"`
	UseCount int    `json:"use_count"`
	// M10 audio-track ownership. IsPublic defaults true (existing
	// reuse-by-default UX). CreatorUserID identifies the rights
	// owner — anyone can use a public track in their own post; a
	// private track requires CreatorUserID == actor.
	IsPublic      bool       `json:"is_public"`
	CreatorUserID *uuid.UUID `json:"creator_user_id,omitempty"`
}

// GetAudioTrack reads one sound. COALESCE throughout: a row written by
// media-service has no media_id, use_count or is_public of its own, and one
// written before the status column existed has no status.
func (s *Store) GetAudioTrack(ctx context.Context, id uuid.UUID) (*AudioTrack, error) {
	var t AudioTrack
	err := s.db.QueryRow(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(artist, ''), COALESCE(duration_ms, 0),
		       COALESCE(source_media_id, media_id), COALESCE(status, ''), COALESCE(use_count, 0),
		       COALESCE(is_public, TRUE), creator_user_id
		FROM audio_tracks WHERE id = $1
	`, id).Scan(
		&t.ID, &t.Title, &t.Artist, &t.DurationMs,
		&t.MediaID, &t.Status, &t.UseCount,
		&t.IsPublic, &t.CreatorUserID,
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

// SetAudioUseCount writes an absolute use_count. Used by the
// sharded-counter flush worker: every flush interval it materializes
// the sum across Redis shards back into audio_tracks.use_count. The
// per-event IncrementAudioUseCount path stays for the Redis-less
// fallback (dev loops + degraded-mode operation).
func (s *Store) SetAudioUseCount(ctx context.Context, id uuid.UUID, total int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET use_count = $2 WHERE id = $1
	`, id, total)
	return err
}

// AttachAudioToPost sets the audio_track_id on a post.
func (s *Store) AttachAudioToPost(ctx context.Context, postID, audioTrackID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE posts SET audio_track_id = $1, updated_at = NOW() WHERE id = $2
	`, audioTrackID, postID)
	return err
}
