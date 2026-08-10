package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MediaClip is one ordered segment in a multi-clip Flick.
type MediaClip struct {
	ID           uuid.UUID `json:"id"`
	PostID       uuid.UUID `json:"post_id"`
	MediaAssetID uuid.UUID `json:"media_asset_id"`
	ClipOrder    int       `json:"clip_order"`
	TrimStartMs  int       `json:"trim_start_ms"`
	TrimEndMs    *int      `json:"trim_end_ms,omitempty"`
	DurationMs   int       `json:"duration_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

// MediaSubtitle is an AI-generated or manually provided subtitle track.
type MediaSubtitle struct {
	ID            uuid.UUID       `json:"id"`
	MediaAssetID  uuid.UUID       `json:"media_asset_id"`
	Language      string          `json:"language"`
	Source        string          `json:"source"`
	Format        string          `json:"format"`
	ContentURL    string          `json:"content_url"`
	// Content is the inline transcript text. media_subtitles is the ONE
	// canonical caption store, so the text lives here rather than in a
	// parallel table (Module 1 fixes-v1 / Codex P0-2).
	Content       string          `json:"content,omitempty"`
	WordLevelJSON json.RawMessage `json:"word_level_json,omitempty"`
	Confidence    *float32        `json:"confidence,omitempty"`
	EditedByOwner bool            `json:"edited_by_owner"`
	CreatedAt     time.Time       `json:"created_at"`
}

// SaveMediaClips replaces all clips for a post within a single transaction.
func (s *MediaAssetStore) SaveMediaClips(ctx context.Context, postID uuid.UUID, clips []MediaClip) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `DELETE FROM media_clips WHERE post_id = $1`, postID); err != nil {
		return err
	}
	for _, c := range clips {
		if _, err = tx.Exec(ctx, `
			INSERT INTO media_clips (post_id, media_asset_id, clip_order, trim_start_ms, trim_end_ms, duration_ms)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			postID, c.MediaAssetID, c.ClipOrder, c.TrimStartMs, c.TrimEndMs, c.DurationMs,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetMediaClips returns the ordered clip list for a post.
func (s *MediaAssetStore) GetMediaClips(ctx context.Context, postID uuid.UUID) ([]MediaClip, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, media_asset_id, clip_order, trim_start_ms, trim_end_ms, duration_ms, created_at
		FROM media_clips WHERE post_id = $1 ORDER BY clip_order ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var clips []MediaClip
	for rows.Next() {
		var c MediaClip
		if err := rows.Scan(&c.ID, &c.PostID, &c.MediaAssetID, &c.ClipOrder,
			&c.TrimStartMs, &c.TrimEndMs, &c.DurationMs, &c.CreatedAt); err != nil {
			return nil, err
		}
		clips = append(clips, c)
	}
	return clips, rows.Err()
}

// CreateSubtitle upserts a subtitle track for a media asset.
func (s *MediaAssetStore) CreateSubtitle(ctx context.Context, sub *MediaSubtitle) (*MediaSubtitle, error) {
	// An owner-corrected transcript is never clobbered by a later
	// auto-generated run (Codex P0-2: owner correction must stick).
	err := s.db.QueryRow(ctx, `
		INSERT INTO media_subtitles (media_asset_id, language, source, format, content_url, content, word_level_json, confidence, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (media_asset_id, language) DO UPDATE
		    SET content_url     = EXCLUDED.content_url,
		        content         = EXCLUDED.content,
		        source          = EXCLUDED.source,
		        word_level_json = EXCLUDED.word_level_json,
		        confidence      = EXCLUDED.confidence,
		        updated_at      = NOW()
		    WHERE media_subtitles.edited_by_owner = FALSE
		RETURNING id, media_asset_id, language, source, format, content_url,
		          COALESCE(content,''), word_level_json, confidence, edited_by_owner, created_at`,
		sub.MediaAssetID, sub.Language, sub.Source, sub.Format, sub.ContentURL,
		sub.Content, sub.WordLevelJSON, sub.Confidence,
	).Scan(&sub.ID, &sub.MediaAssetID, &sub.Language, &sub.Source, &sub.Format,
		&sub.ContentURL, &sub.Content, &sub.WordLevelJSON, &sub.Confidence,
		&sub.EditedByOwner, &sub.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The WHERE guard suppressed the update because the owner has
		// edited this track. Return the existing row unchanged.
		return s.getSubtitle(ctx, sub.MediaAssetID, sub.Language)
	}
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// GetSubtitles returns all subtitle tracks for a media asset, ordered by language.
func (s *MediaAssetStore) GetSubtitles(ctx context.Context, mediaAssetID uuid.UUID) ([]MediaSubtitle, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_asset_id, language, source, format, content_url,
		       COALESCE(content,''), word_level_json, confidence, edited_by_owner, created_at
		FROM media_subtitles WHERE media_asset_id = $1 ORDER BY language ASC`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []MediaSubtitle
	for rows.Next() {
		var sub MediaSubtitle
		if err := rows.Scan(&sub.ID, &sub.MediaAssetID, &sub.Language, &sub.Source, &sub.Format,
			&sub.ContentURL, &sub.Content, &sub.WordLevelJSON, &sub.Confidence,
			&sub.EditedByOwner, &sub.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// getSubtitle reads one track (used when an upsert is suppressed because
// the owner has edited it).
func (s *MediaAssetStore) getSubtitle(ctx context.Context, mediaAssetID uuid.UUID, language string) (*MediaSubtitle, error) {
	var sub MediaSubtitle
	err := s.db.QueryRow(ctx, `
		SELECT id, media_asset_id, language, source, format, content_url,
		       COALESCE(content,''), word_level_json, confidence, edited_by_owner, created_at
		FROM media_subtitles WHERE media_asset_id = $1 AND language = $2`,
		mediaAssetID, language).
		Scan(&sub.ID, &sub.MediaAssetID, &sub.Language, &sub.Source, &sub.Format,
			&sub.ContentURL, &sub.Content, &sub.WordLevelJSON, &sub.Confidence,
			&sub.EditedByOwner, &sub.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}
