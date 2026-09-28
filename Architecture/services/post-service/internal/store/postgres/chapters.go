package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MediaChapter represents a chapter marker within a video post.
//
// There is deliberately no ID field. media_chapters is keyed by
// (post_id, chapter_index) — see 012_posttube_features.sql — and has no
// surrogate `id` column. The struct used to declare one and GetChapters
// used to SELECT it, so every read of a post's chapters failed with
// `column "id" does not exist` while the write path (which never named
// the column) happily returned 200. Chapters could be saved and never
// read back. chapter_index is the stable identifier a client should use.
type MediaChapter struct {
	PostID       uuid.UUID `json:"post_id"`
	ChapterIndex int       `json:"chapter_index"`
	Title        string    `json:"title"`
	StartMs      int       `json:"start_ms"`
	ThumbnailURL *string   `json:"thumbnail_url,omitempty"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

// EndScreen represents an interactive end-screen element overlaid on a video.
// VideoMode (migration 054) is 'specific' | 'latest' | 'popular' and only
// means something for type 'video'. The wire shapes the watch page and the
// editor read are built in service/end_screens.go, not from this row.
type EndScreen struct {
	ID        uuid.UUID       `json:"id"`
	PostID    uuid.UUID       `json:"post_id"`
	Type      string          `json:"type"`
	VideoMode string          `json:"video_mode"`
	TargetID  *uuid.UUID      `json:"target_id,omitempty"`
	TargetURL *string         `json:"target_url,omitempty"`
	Title     *string         `json:"title,omitempty"`
	Position  json.RawMessage `json:"position"`
	StartMs   int             `json:"start_ms"`
	EndMs     int             `json:"end_ms"`
	CreatedAt time.Time       `json:"created_at"`
}

// VideoCard represents an interactive card shown at a specific timestamp in a video.
type VideoCard struct {
	ID         uuid.UUID  `json:"id"`
	PostID     uuid.UUID  `json:"post_id"`
	Type       string     `json:"type"`
	TargetID   *uuid.UUID `json:"target_id,omitempty"`
	TargetURL  *string    `json:"target_url,omitempty"`
	Title      string     `json:"title"`
	TeaserText *string    `json:"teaser_text,omitempty"`
	AppearAtMs int        `json:"appear_at_ms"`
	CreatedAt  time.Time  `json:"created_at"`
}

// SaveChapters replaces all chapters for a post in a single transaction.
func (s *Store) SaveChapters(ctx context.Context, postID uuid.UUID, chapters []MediaChapter) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `DELETE FROM media_chapters WHERE post_id = $1`, postID)
	if err != nil {
		return err
	}

	for _, ch := range chapters {
		src := ch.Source
		if src == "" {
			src = "manual"
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO media_chapters (post_id, chapter_index, title, start_ms, thumbnail_url, source)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			postID, ch.ChapterIndex, ch.Title, ch.StartMs, ch.ThumbnailURL, src)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetChapters retrieves all chapters for a post ordered by chapter_index.
func (s *Store) GetChapters(ctx context.Context, postID uuid.UUID) ([]MediaChapter, error) {
	rows, err := s.db.Query(ctx, `
		SELECT post_id, chapter_index, title, start_ms, thumbnail_url, source, created_at
		FROM media_chapters WHERE post_id = $1
		ORDER BY chapter_index ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MediaChapter
	for rows.Next() {
		var ch MediaChapter
		if err := rows.Scan(&ch.PostID, &ch.ChapterIndex, &ch.Title, &ch.StartMs, &ch.ThumbnailURL, &ch.Source, &ch.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, ch)
	}
	return result, rows.Err()
}

// SaveEndScreens replaces all end screens for a post in a single transaction.
//
// An element carrying the id of one of THIS post's existing elements is
// updated in place and keeps its id, so its end_screen_stats rows survive
// the edit; every other element is inserted with a fresh id, and the post's
// elements missing from the new set are deleted (their stats cascade). The
// written ids are set back on screens.
func (s *Store) SaveEndScreens(ctx context.Context, postID uuid.UUID, screens []EndScreen) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	kept := make([]uuid.UUID, 0, len(screens))
	for _, sc := range screens {
		if sc.ID != uuid.Nil {
			kept = append(kept, sc.ID)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM video_end_screens WHERE post_id = $1 AND NOT (id = ANY($2))`, postID, kept); err != nil {
		return err
	}

	for i := range screens {
		sc := &screens[i]
		mode := sc.VideoMode
		if mode == "" {
			mode = "specific"
		}
		if sc.ID != uuid.Nil {
			tag, err := tx.Exec(ctx, `
				UPDATE video_end_screens
				SET type = $3, video_mode = $4, target_id = $5, target_url = $6, title = $7,
				    position = $8, start_ms = $9, end_ms = $10
				WHERE id = $1 AND post_id = $2`,
				sc.ID, postID, sc.Type, mode, sc.TargetID, sc.TargetURL, sc.Title, sc.Position, sc.StartMs, sc.EndMs)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				continue
			}
		}
		if err = tx.QueryRow(ctx, `
			INSERT INTO video_end_screens (post_id, type, video_mode, target_id, target_url, title, position, start_ms, end_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id`,
			postID, sc.Type, mode, sc.TargetID, sc.TargetURL, sc.Title, sc.Position, sc.StartMs, sc.EndMs).Scan(&sc.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetEndScreens retrieves all end screens for a post, oldest first.
func (s *Store) GetEndScreens(ctx context.Context, postID uuid.UUID) ([]EndScreen, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, type, video_mode, target_id, target_url, title, position, start_ms, end_ms, created_at
		FROM video_end_screens WHERE post_id = $1
		ORDER BY created_at ASC, id ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []EndScreen
	for rows.Next() {
		var sc EndScreen
		if err := rows.Scan(&sc.ID, &sc.PostID, &sc.Type, &sc.VideoMode, &sc.TargetID, &sc.TargetURL, &sc.Title, &sc.Position, &sc.StartMs, &sc.EndMs, &sc.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, sc)
	}
	return result, rows.Err()
}

// SaveVideoCards replaces all video cards for a post in a single transaction.
// Same id rule as SaveEndScreens: an echoed id of this post's card is kept,
// and so are its card_stats rows.
func (s *Store) SaveVideoCards(ctx context.Context, postID uuid.UUID, cards []VideoCard) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	kept := make([]uuid.UUID, 0, len(cards))
	for _, card := range cards {
		if card.ID != uuid.Nil {
			kept = append(kept, card.ID)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM video_cards WHERE post_id = $1 AND NOT (id = ANY($2))`, postID, kept); err != nil {
		return err
	}

	for i := range cards {
		card := &cards[i]
		if card.ID != uuid.Nil {
			tag, err := tx.Exec(ctx, `
				UPDATE video_cards
				SET type = $3, target_id = $4, target_url = $5, title = $6, teaser_text = $7, appear_at_ms = $8
				WHERE id = $1 AND post_id = $2`,
				card.ID, postID, card.Type, card.TargetID, card.TargetURL, card.Title, card.TeaserText, card.AppearAtMs)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				continue
			}
		}
		if err = tx.QueryRow(ctx, `
			INSERT INTO video_cards (post_id, type, target_id, target_url, title, teaser_text, appear_at_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id`,
			postID, card.Type, card.TargetID, card.TargetURL, card.Title, card.TeaserText, card.AppearAtMs).Scan(&card.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetVideoCards retrieves all video cards for a post ordered by appear_at_ms.
func (s *Store) GetVideoCards(ctx context.Context, postID uuid.UUID) ([]VideoCard, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, type, target_id, target_url, title, teaser_text, appear_at_ms, created_at
		FROM video_cards WHERE post_id = $1
		ORDER BY appear_at_ms ASC, id ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []VideoCard
	for rows.Next() {
		var card VideoCard
		if err := rows.Scan(&card.ID, &card.PostID, &card.Type, &card.TargetID, &card.TargetURL, &card.Title, &card.TeaserText, &card.AppearAtMs, &card.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, card)
	}
	return result, rows.Err()
}
