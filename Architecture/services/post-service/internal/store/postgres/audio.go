package postgres

import (
	"context"
	"fmt"
	"time"

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
	// SourcePostID is the reel the sound was taken from (media-service's
	// source_reel_id). Nil on an uploaded track and on a sound extracted
	// before the column was written.
	SourcePostID *uuid.UUID `json:"source_post_id,omitempty"`
	// Status is media-service's: processing, ready, rejected, deleted.
	Status string `json:"status"`
	// UseCount is the larger of the two counters the table carries
	// (use_count here, usage_count in media-service). Both are written to
	// the same value from now on; rows counted before that may disagree.
	UseCount int `json:"use_count"`
	// M10 audio-track ownership. IsPublic defaults true (existing
	// reuse-by-default UX). CreatorUserID identifies the rights
	// owner — anyone can use a public track in their own post; a
	// private track requires CreatorUserID == actor.
	IsPublic      bool       `json:"is_public"`
	CreatorUserID *uuid.UUID `json:"creator_user_id,omitempty"`
}

// audioTrackCols is the one projection of a sound. COALESCE throughout: a
// row written by media-service has no media_id, use_count or is_public of
// its own, and one written before the status column existed has no status.
const audioTrackCols = `id, COALESCE(title, ''), COALESCE(artist, ''), COALESCE(duration_ms, 0),
		       COALESCE(source_media_id, media_id), source_reel_id, COALESCE(status, ''),
		       GREATEST(COALESCE(use_count, 0), COALESCE(usage_count, 0)),
		       COALESCE(is_public, TRUE), creator_user_id`

func audioTrackDestinations(t *AudioTrack) []any {
	return []any{
		&t.ID, &t.Title, &t.Artist, &t.DurationMs,
		&t.MediaID, &t.SourcePostID, &t.Status,
		&t.UseCount,
		&t.IsPublic, &t.CreatorUserID,
	}
}

// GetAudioTrack reads one sound.
func (s *Store) GetAudioTrack(ctx context.Context, id uuid.UUID) (*AudioTrack, error) {
	var t AudioTrack
	err := s.db.QueryRow(ctx, `
		SELECT `+audioTrackCols+`
		FROM audio_tracks WHERE id = $1
	`, id).Scan(audioTrackDestinations(&t)...)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetAudioTracksByIDs reads the sounds a page of posts plays, in one round
// trip. An id with no row is absent from the map.
func (s *Store) GetAudioTracksByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*AudioTrack, error) {
	out := make(map[uuid.UUID]*AudioTrack, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+audioTrackCols+`
		FROM audio_tracks WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t AudioTrack
		if err := rows.Scan(audioTrackDestinations(&t)...); err != nil {
			return nil, err
		}
		out[t.ID] = &t
	}
	return out, rows.Err()
}

// One counter (2026-09-29). The table carries two, use_count (this
// service's) and usage_count (media-service's, the one its catalogue sorts
// and shows). Every write here sets BOTH to the same value, counted from the
// larger of the two, so a sound reads the same from either service.

// IncrementAudioUseCount atomically adds one use of an audio track.
func (s *Store) IncrementAudioUseCount(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks
		SET use_count   = GREATEST(COALESCE(use_count, 0), COALESCE(usage_count, 0)) + 1,
		    usage_count = GREATEST(COALESCE(use_count, 0), COALESCE(usage_count, 0)) + 1
		WHERE id = $1
	`, id)
	return err
}

// SetAudioUseCount writes an absolute use count. Used by the
// sharded-counter flush worker: every flush interval it materializes
// the sum across Redis shards back into audio_tracks. The
// per-event IncrementAudioUseCount path stays for the Redis-less
// fallback (dev loops + degraded-mode operation).
//
// The write never lowers the count: the shard sum only knows the uses
// counted through this service since the counter began, and the row may
// already hold more (media-service counts uses on the same two columns).
func (s *Store) SetAudioUseCount(ctx context.Context, id uuid.UUID, total int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks
		SET use_count   = GREATEST(COALESCE(use_count, 0), COALESCE(usage_count, 0), $2::int),
		    usage_count = GREATEST(COALESCE(use_count, 0), COALESCE(usage_count, 0), $2::int)
		WHERE id = $1
	`, id, total)
	return err
}

// AttachAudioToPost sets the sound a post plays and where in the sound
// playback starts. It reports whether the sound is new to the post, which
// is what a use is: linking the sound the post already plays (a retried
// create, a draft published twice) moves the start and counts nothing.
func (s *Store) AttachAudioToPost(ctx context.Context, postID, audioTrackID uuid.UUID, startMs int) (bool, error) {
	var newToPost bool
	err := s.db.QueryRow(ctx, `
		WITH prev AS (SELECT audio_track_id FROM posts WHERE id = $2 FOR UPDATE)
		UPDATE posts p
		SET audio_track_id = $1, audio_start_ms = $3, updated_at = NOW()
		FROM prev
		WHERE p.id = $2
		RETURNING prev.audio_track_id IS DISTINCT FROM $1
	`, audioTrackID, postID, startMs).Scan(&newToPost)
	if err != nil {
		return false, err
	}
	return newToPost, nil
}

// SoundSourcePost is a post a sound was taken from, as the consent rule
// reads it: whose it is, and whether its audio may be reused.
type SoundSourcePost struct {
	ID           uuid.UUID
	AuthorID     uuid.UUID
	RemixSetting string
}

// SoundSourcePosts returns the live posts a sound comes from: the one the
// sound names (source_reel_id), and every post that carries the sound's
// media. The second half is what makes the consent rule hold for a sound
// that names no post, which is every sound extracted by media id alone.
// Either argument may be nil.
func (s *Store) SoundSourcePosts(ctx context.Context, sourcePostID, mediaID *uuid.UUID) ([]SoundSourcePost, error) {
	if sourcePostID == nil && mediaID == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.author_id, COALESCE(p.remix_setting, '')
		FROM posts p
		WHERE p.deleted_at IS NULL
		  AND (p.id = $1 OR EXISTS (SELECT 1 FROM post_media pm WHERE pm.post_id = p.id AND pm.media_id = $2))
		ORDER BY p.id
	`, sourcePostID, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SoundSourcePost
	for rows.Next() {
		var sp SoundSourcePost
		if err := rows.Scan(&sp.ID, &sp.AuthorID, &sp.RemixSetting); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// PrimaryVideoMediaID is the first video a post carries, in carousel order;
// nil when it carries none.
func (s *Store) PrimaryVideoMediaID(ctx context.Context, postID uuid.UUID) (*uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT pm.media_id FROM post_media pm WHERE pm.post_id = $1 AND pm.kind = 'video'`+postMediaOrder+` LIMIT 1`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var id uuid.UUID
	if err := rows.Scan(&id); err != nil {
		return nil, err
	}
	return &id, rows.Err()
}

// Posts that play a sound (GET /v1/posts/by-sound/:soundId).

const (
	DefaultSoundPostsLimit = 24
	MaxSoundPostsLimit     = 50
)

// ParseSoundPostsCursor reads the keyset cursor ListPostsBySound issues:
// "<created_at RFC3339Nano>_<post id>", the reel feed's format.
func ParseSoundPostsCursor(cursor string) (time.Time, uuid.UUID, error) {
	return parseReelCursor(cursor)
}

// ListPostsBySound returns the reels that play a sound, newest first: short
// videos that are public, live, approved and unrestricted. exclude, when
// set, is the reel the sound was taken from; the page shows it apart from
// the list, so it is never a row. An unreadable cursor is an error, not the
// first page. limit is the caller's, already resolved
// (service.NormalizeSoundPostsLimit).
func (s *Store) ListPostsBySound(ctx context.Context, soundID uuid.UUID, exclude *uuid.UUID, limit int, cursor string) ([]Post, string, error) {
	if limit < 1 {
		return nil, "", fmt.Errorf("sound posts limit %d: must be at least 1", limit)
	}
	args := []any{soundID, limit + 1}
	query := `SELECT ` + postCols + `
		FROM posts
		WHERE audio_track_id = $1
			AND content_type IN ('flick', 'reel')
			AND visibility = 'public'
			AND deleted_at IS NULL
			AND publish_at IS NULL
			AND ` + viewerApprovedSQL
	if exclude != nil {
		args = append(args, *exclude)
		query += fmt.Sprintf(` AND id <> $%d`, len(args))
	}
	if cursor != "" {
		cursorTime, cursorID, err := parseReelCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("sound posts cursor: %w", err)
		}
		args = append(args, cursorTime, cursorID)
		query += fmt.Sprintf(` AND (created_at, id) < ($%d, $%d)`, len(args)-1, len(args))
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}
	var nextCursor string
	if len(posts) > limit {
		last := posts[limit-1]
		nextCursor = encodeReelCursor(last.CreatedAt, last.ID)
		posts = posts[:limit]
	}
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return nil, "", err
	}
	return posts, nextCursor, nil
}
