package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MTube captions list for creators (2026-09-27, migration 020).

// SubtitleStatusFilter is the `status` query of GET /v1/subtitles/mine.
type SubtitleStatusFilter string

const (
	SubtitleStatusAll       SubtitleStatusFilter = "all"
	SubtitleStatusDraft     SubtitleStatusFilter = "draft"
	SubtitleStatusPublished SubtitleStatusFilter = "published"
)

// SubtitleLanguageRow is one caption track as the creator list shows it —
// state only, never the transcript.
type SubtitleLanguageRow struct {
	Language  string    `json:"language"`
	Source    string    `json:"source"`
	Published bool      `json:"published"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SubtitleMediaGroup is one media asset's tracks, keyed for pagination by
// the newest track's updated_at.
type SubtitleMediaGroup struct {
	MediaID    uuid.UUID             `json:"media_id"`
	ModifiedAt time.Time             `json:"modified_at"`
	Languages  []SubtitleLanguageRow `json:"languages"`
}

// SubtitleListCursor is the keyset position: the last group's
// (modified_at, media_id). Zero value means "from the top".
type SubtitleListCursor struct {
	ModifiedAt time.Time
	MediaID    uuid.UUID
}

func (c SubtitleListCursor) empty() bool { return c.ModifiedAt.IsZero() }

// ListSubtitlesByUploader returns up to limit+1 groups (the caller trims and
// derives the next cursor) of caption tracks on media the uploader owns,
// newest modified first. The status filter applies to the tracks, so a
// media whose only tracks are drafts disappears from status=published.
func (s *MediaAssetStore) ListSubtitlesByUploader(ctx context.Context, uploaderID uuid.UUID, status SubtitleStatusFilter, cursor SubtitleListCursor, limit int) ([]SubtitleMediaGroup, error) {
	if limit <= 0 {
		limit = 20
	}
	var cursorAt *time.Time
	var cursorID *uuid.UUID
	if !cursor.empty() {
		at, id := cursor.ModifiedAt, cursor.MediaID
		cursorAt, cursorID = &at, &id
	}
	rows, err := s.db.Query(ctx, `
		SELECT s.media_asset_id, MAX(s.updated_at) AS modified_at
		  FROM media_subtitles s
		  JOIN media_assets m ON m.id = s.media_asset_id
		 WHERE m.uploader_id = $1
		   AND ($2 = 'all' OR ($2 = 'draft' AND NOT s.published) OR ($2 = 'published' AND s.published))
		 GROUP BY s.media_asset_id
		HAVING $3::timestamptz IS NULL
		    OR MAX(s.updated_at) < $3
		    OR (MAX(s.updated_at) = $3 AND s.media_asset_id < $4::uuid)
		 ORDER BY modified_at DESC, s.media_asset_id DESC
		 LIMIT $5`,
		uploaderID, string(status), cursorAt, cursorID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list subtitle media: %w", err)
	}
	defer rows.Close()
	var groups []SubtitleMediaGroup
	var ids []uuid.UUID
	for rows.Next() {
		var g SubtitleMediaGroup
		if err := rows.Scan(&g.MediaID, &g.ModifiedAt); err != nil {
			return nil, err
		}
		g.Languages = []SubtitleLanguageRow{}
		groups = append(groups, g)
		ids = append(ids, g.MediaID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return groups, nil
	}
	langRows, err := s.db.Query(ctx, `
		SELECT media_asset_id, language, source, published, updated_at
		  FROM media_subtitles
		 WHERE media_asset_id = ANY($1::uuid[])
		   AND ($2 = 'all' OR ($2 = 'draft' AND NOT published) OR ($2 = 'published' AND published))
		 ORDER BY media_asset_id, language ASC`, ids, string(status))
	if err != nil {
		return nil, fmt.Errorf("list subtitle languages: %w", err)
	}
	defer langRows.Close()
	index := make(map[uuid.UUID]int, len(groups))
	for i, g := range groups {
		index[g.MediaID] = i
	}
	for langRows.Next() {
		var mediaID uuid.UUID
		var row SubtitleLanguageRow
		if err := langRows.Scan(&mediaID, &row.Language, &row.Source, &row.Published, &row.UpdatedAt); err != nil {
			return nil, err
		}
		if i, ok := index[mediaID]; ok {
			groups[i].Languages = append(groups[i].Languages, row)
		}
	}
	return groups, langRows.Err()
}

// SetSubtitlePublished flips one track's review state and returns the row.
// pgx.ErrNoRows when there is no such track. Ownership is the service's
// check (it has the asset row); this only writes. The flip and its
// MediaSubtitlesChanged snapshot are one commit (subtitle_events.go).
func (s *MediaAssetStore) SetSubtitlePublished(ctx context.Context, mediaID uuid.UUID, language string, published bool) (*MediaSubtitle, error) {
	var sub MediaSubtitle
	err := s.withSubtitleStateEvent(ctx, mediaID, func(tx pgx.Tx) (bool, error) {
		err := tx.QueryRow(ctx, `
			UPDATE media_subtitles
			   SET published = $3, updated_at = NOW()
			 WHERE media_asset_id = $1 AND language = $2
			RETURNING id, media_asset_id, language, source, format, content_url,
			          COALESCE(content,''), word_level_json, confidence, edited_by_owner, created_at,
			          published, updated_at`,
			mediaID, language, published).
			Scan(&sub.ID, &sub.MediaAssetID, &sub.Language, &sub.Source, &sub.Format,
				&sub.ContentURL, &sub.Content, &sub.WordLevelJSON, &sub.Confidence,
				&sub.EditedByOwner, &sub.CreatedAt, &sub.Published, &sub.UpdatedAt)
		return err == nil, err
	})
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// MarkGeneratedSubtitleDraft is the caption job's completion write: the
// track it just generated becomes a draft for the creator to review. An
// owner-edited track is left alone — the job's upsert was suppressed for it
// (CreateSubtitle), so its text is the owner's and so is its state. The
// write and its MediaSubtitlesChanged snapshot are one commit.
func (s *MediaAssetStore) MarkGeneratedSubtitleDraft(ctx context.Context, mediaID uuid.UUID, language string) error {
	return s.withSubtitleStateEvent(ctx, mediaID, func(tx pgx.Tx) (bool, error) {
		_, err := tx.Exec(ctx, `
			UPDATE media_subtitles
			   SET published = FALSE
			 WHERE media_asset_id = $1 AND language = $2
			   AND source = 'auto_generated' AND edited_by_owner = FALSE`,
			mediaID, language)
		return err == nil, err
	})
}
