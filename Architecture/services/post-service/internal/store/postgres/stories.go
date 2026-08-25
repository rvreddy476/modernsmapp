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
	ID       uuid.UUID `json:"id"`
	AuthorID uuid.UUID `json:"author_id"`
	// MediaID is the canonical media asset. M4-P0-4 — this replaced a
	// caller-supplied media_url, which meant a story pointed at whatever
	// string the client sent rather than at an asset this platform owns,
	// processed and is allowed to serve.
	MediaID *uuid.UUID `json:"media_id,omitempty"`
	// MediaURL is DERIVED at read time from MediaID by the protected-delivery
	// path. It is never accepted as input and never persisted.
	MediaURL       string     `json:"media_url,omitempty"`
	MediaType      string     `json:"media_type"`
	Caption        string     `json:"caption,omitempty"`
	Visibility     string     `json:"visibility"`
	ViewCount      int        `json:"view_count"`
	ExpiresAt      time.Time  `json:"expires_at"`
	IsHighlight    bool       `json:"is_highlight"`
	HighlightGroup *string    `json:"highlight_group,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"-"`

	// Moderation state. Only ModerationState == "approved" is publishable.
	ModerationState  string     `json:"moderation_state"`
	ContentRevision  int64      `json:"content_revision"`
	ModeratedAt      *time.Time `json:"moderated_at,omitempty"`
	ModerationReason string     `json:"moderation_reason,omitempty"`
}

const storyCols = `id, author_id, media_id, media_type, caption, visibility,
	view_count, expires_at, is_highlight, highlight_group, created_at,
	deleted_at, moderation_state, content_revision, moderated_at,
	COALESCE(moderation_reason, '')`

func scanStoryInto(s *Story, scan func(...any) error) error {
	return scan(
		&s.ID, &s.AuthorID, &s.MediaID, &s.MediaType, &s.Caption, &s.Visibility,
		&s.ViewCount, &s.ExpiresAt, &s.IsHighlight, &s.HighlightGroup, &s.CreatedAt,
		&s.DeletedAt, &s.ModerationState, &s.ContentRevision, &s.ModeratedAt,
		&s.ModerationReason,
	)
}

func scanStory(row pgx.Row) (*Story, error) {
	var s Story
	if err := scanStoryInto(&s, row.Scan); err != nil {
		return nil, err
	}
	return &s, nil
}

func scanStoryRows(rows pgx.Rows) ([]Story, error) {
	var stories []Story
	for rows.Next() {
		var s Story
		if err := scanStoryInto(&s, rows.Scan); err != nil {
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
	// M4-P0-2: this returns the row WITHOUT applying policy, and every caller
	// must pass it through service.EvaluateStoryVisibility before returning
	// anything to a viewer.
	//
	// The row is loaded unfiltered on purpose: the policy needs to distinguish
	// expired from rejected from out-of-audience in order to log and measure
	// denials, and the owner is entitled to see their own pending story. A
	// query that pre-filtered would collapse those cases and force a second
	// query for the owner path — two queries that would then be free to
	// disagree. Filtering lives in exactly one place instead.
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
			AND deleted_at IS NULL
			AND moderation_state = 'approved'
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
			AND deleted_at IS NULL
			AND moderation_state = 'approved'
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

// GetStoriesForOwner returns every one of an author's own live stories in all
// moderation states.
//
// M4-P0-4: the owner is entitled to a truthful pending/rejected view of their
// own content. Soft-deleted rows stay hidden — the author deleted those on
// purpose — but nothing else is filtered, because hiding a rejected story from
// its author is how a creator concludes the app silently ate their upload.
func (s *Store) GetStoriesForOwner(ctx context.Context, ownerID uuid.UUID) ([]Story, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+storyCols+`
		FROM stories
		WHERE author_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStoryRows(rows)
}
