package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Story represents an ephemeral story (24h expiry).
type Story struct {
	ID             uuid.UUID  `json:"id"`
	AuthorID       uuid.UUID  `json:"author_id"`
	MediaURL       string     `json:"media_url"`
	MediaType      string     `json:"media_type"`
	Caption        string     `json:"caption,omitempty"`
	Visibility     string     `json:"visibility"`
	ViewCount      int        `json:"view_count"`
	ExpiresAt      time.Time  `json:"expires_at"`
	IsHighlight    bool       `json:"is_highlight"`
	HighlightGroup *string    `json:"highlight_group,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

const storyCols = `id, author_id, media_url, media_type, caption, visibility,
	view_count, expires_at, is_highlight, highlight_group, created_at`

func scanStory(row pgx.Row) (*Story, error) {
	var s Story
	err := row.Scan(
		&s.ID, &s.AuthorID, &s.MediaURL, &s.MediaType, &s.Caption, &s.Visibility,
		&s.ViewCount, &s.ExpiresAt, &s.IsHighlight, &s.HighlightGroup, &s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func scanStoryRows(rows pgx.Rows) ([]Story, error) {
	var stories []Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(
			&s.ID, &s.AuthorID, &s.MediaURL, &s.MediaType, &s.Caption, &s.Visibility,
			&s.ViewCount, &s.ExpiresAt, &s.IsHighlight, &s.HighlightGroup, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		stories = append(stories, s)
	}
	return stories, rows.Err()
}

// CreateStory inserts a new story.
func (s *Store) CreateStory(ctx context.Context, story *Story) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO stories (id, author_id, media_url, media_type, caption, visibility,
			view_count, expires_at, is_highlight, highlight_group, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, story.ID, story.AuthorID, story.MediaURL, story.MediaType, story.Caption,
		story.Visibility, story.ViewCount, story.ExpiresAt, story.IsHighlight,
		story.HighlightGroup, story.CreatedAt)
	return err
}

// GetStory returns a single story by ID.
func (s *Store) GetStory(ctx context.Context, id uuid.UUID) (*Story, error) {
	story, err := scanStory(s.db.QueryRow(ctx, `
		SELECT `+storyCols+` FROM stories WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return story, nil
}

// GetStoriesFeed returns active (non-expired) stories from a list of followed user IDs.
// Stories are ordered by created_at DESC grouped by author.
func (s *Store) GetStoriesFeed(ctx context.Context, followedUserIDs []uuid.UUID) ([]Story, error) {
	if len(followedUserIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT `+storyCols+`
		FROM stories
		WHERE author_id = ANY($1)
			AND (expires_at > NOW() OR is_highlight = TRUE)
		ORDER BY created_at DESC
	`, followedUserIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanStoryRows(rows)
}

// GetStoriesByAuthor returns active stories for a specific author.
func (s *Store) GetStoriesByAuthor(ctx context.Context, authorID uuid.UUID) ([]Story, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+storyCols+`
		FROM stories
		WHERE author_id = $1
			AND (expires_at > NOW() OR is_highlight = TRUE)
		ORDER BY created_at DESC
	`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanStoryRows(rows)
}

// DeleteStory removes a story. Only the author can delete.
func (s *Store) DeleteStory(ctx context.Context, storyID, authorID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM stories WHERE id = $1 AND author_id = $2
	`, storyID, authorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("STORY_NOT_FOUND")
	}
	return nil
}

// IncrementStoryViewCount atomically increments the view count.
// Kept as the Redis-nil fallback for adjustStoryViewCount in the
// service layer; production traffic flows through the sharded counter.
func (s *Store) IncrementStoryViewCount(ctx context.Context, storyID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE stories SET view_count = view_count + 1 WHERE id = $1
	`, storyID)
	return err
}

// SetStoryViewCount overwrites stories.view_count to the absolute sum
// from the sharded Redis counter. Called by the flush worker every
// ~10s per dirty story.
func (s *Store) SetStoryViewCount(ctx context.Context, storyID uuid.UUID, total int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE stories SET view_count = $2 WHERE id = $1
	`, storyID, total)
	return err
}

// CleanupExpiredStories removes non-highlight stories past their expiry.
func (s *Store) CleanupExpiredStories(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM stories WHERE expires_at < NOW() AND is_highlight = FALSE
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
