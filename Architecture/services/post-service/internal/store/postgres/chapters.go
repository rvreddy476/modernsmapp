package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MediaChapter represents a chapter marker within a video post.
type MediaChapter struct {
	ID           uuid.UUID `json:"id"`
	PostID       uuid.UUID `json:"post_id"`
	ChapterIndex int       `json:"chapter_index"`
	Title        string    `json:"title"`
	StartMs      int       `json:"start_ms"`
	ThumbnailURL *string   `json:"thumbnail_url,omitempty"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

// EndScreen represents an interactive end-screen element overlaid on a video.
type EndScreen struct {
	ID        uuid.UUID       `json:"id"`
	PostID    uuid.UUID       `json:"post_id"`
	Type      string          `json:"type"`
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
		SELECT id, post_id, chapter_index, title, start_ms, thumbnail_url, source, created_at
		FROM media_chapters WHERE post_id = $1
		ORDER BY chapter_index ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MediaChapter
	for rows.Next() {
		var ch MediaChapter
		if err := rows.Scan(&ch.ID, &ch.PostID, &ch.ChapterIndex, &ch.Title, &ch.StartMs, &ch.ThumbnailURL, &ch.Source, &ch.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, ch)
	}
	return result, rows.Err()
}

// SaveEndScreens replaces all end screens for a post in a single transaction.
func (s *Store) SaveEndScreens(ctx context.Context, postID uuid.UUID, screens []EndScreen) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `DELETE FROM video_end_screens WHERE post_id = $1`, postID)
	if err != nil {
		return err
	}

	for _, sc := range screens {
		_, err = tx.Exec(ctx, `
			INSERT INTO video_end_screens (post_id, type, target_id, target_url, title, position, start_ms, end_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			postID, sc.Type, sc.TargetID, sc.TargetURL, sc.Title, sc.Position, sc.StartMs, sc.EndMs)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetEndScreens retrieves all end screens for a post.
func (s *Store) GetEndScreens(ctx context.Context, postID uuid.UUID) ([]EndScreen, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, type, target_id, target_url, title, position, start_ms, end_ms, created_at
		FROM video_end_screens WHERE post_id = $1`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []EndScreen
	for rows.Next() {
		var sc EndScreen
		if err := rows.Scan(&sc.ID, &sc.PostID, &sc.Type, &sc.TargetID, &sc.TargetURL, &sc.Title, &sc.Position, &sc.StartMs, &sc.EndMs, &sc.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, sc)
	}
	return result, rows.Err()
}

// SaveVideoCards replaces all video cards for a post in a single transaction.
func (s *Store) SaveVideoCards(ctx context.Context, postID uuid.UUID, cards []VideoCard) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `DELETE FROM video_cards WHERE post_id = $1`, postID)
	if err != nil {
		return err
	}

	for _, card := range cards {
		_, err = tx.Exec(ctx, `
			INSERT INTO video_cards (post_id, type, target_id, target_url, title, teaser_text, appear_at_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			postID, card.Type, card.TargetID, card.TargetURL, card.Title, card.TeaserText, card.AppearAtMs)
		if err != nil {
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
		ORDER BY appear_at_ms ASC`, postID)
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
