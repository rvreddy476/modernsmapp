package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MetaStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *MetaStore {
	return &MetaStore{db: db}
}

// IsCeleb checks if an author is a celebrity
func (s *MetaStore) IsCeleb(ctx context.Context, authorID uuid.UUID) (bool, error) {
	var isCeleb bool
	err := s.db.QueryRow(ctx, `SELECT is_celeb FROM celeb_authors WHERE author_id = $1`, authorID).Scan(&isCeleb)
	if err != nil {
		// If not found, assume false (safe default)
		return false, nil
	}
	return isCeleb, nil
}

// SetCelebStatus updates celeb status
func (s *MetaStore) SetCelebStatus(ctx context.Context, authorID uuid.UUID, isCeleb bool) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO celeb_authors (author_id, is_celeb, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (author_id) DO UPDATE
		SET is_celeb = EXCLUDED.is_celeb, updated_at = NOW()
	`, authorID, isCeleb)
	return err
}

// GetFeedMode returns the user's preferred feed mode (e.g. "ranked" or "chronological").
// Defaults to "chronological" if no preference is stored.
func (s *MetaStore) GetFeedMode(ctx context.Context, userID uuid.UUID) (string, error) {
	var mode string
	err := s.db.QueryRow(ctx, `SELECT feed_mode FROM user_preferences WHERE user_id = $1`, userID).Scan(&mode)
	if err != nil {
		// If not found, return safe default
		return "chronological", nil
	}
	return mode, nil
}

// SetFeedMode upserts the user's preferred feed mode into user_preferences.
func (s *MetaStore) SetFeedMode(ctx context.Context, userID uuid.UUID, mode string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_preferences (user_id, feed_mode, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE
		SET feed_mode = EXCLUDED.feed_mode, updated_at = NOW()
	`, userID, mode)
	return err
}

// RecordSignal stores a user interaction signal (e.g. "see_less", "see_more") against a post.
// The row is inserted into post_impressions with the given action. The downstream affinity
// pipeline will later process these signals to adjust author boosts and penalties.
func (s *MetaStore) RecordSignal(ctx context.Context, userID, postID uuid.UUID, signal string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO post_impressions (user_id, post_id, action, created_at)
		VALUES ($1, $2, $3, NOW())
	`, userID, postID, signal)
	return err
}
