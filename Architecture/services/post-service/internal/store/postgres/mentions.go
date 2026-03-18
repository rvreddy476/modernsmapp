package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PostMention represents a @username mention within a post.
type PostMention struct {
	ID              uuid.UUID `json:"id"`
	PostID          uuid.UUID `json:"post_id"`
	PostType        string    `json:"post_type"`
	MentionedUserID string    `json:"mentioned_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// InsertMention inserts a single mention record, ignoring duplicates.
func (s *Store) InsertMention(ctx context.Context, postID uuid.UUID, postType, mentionedUserID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO post_mentions (post_id, post_type, mentioned_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (post_id, mentioned_user_id) DO NOTHING
	`, postID, postType, mentionedUserID)
	return err
}

// GetMentionsByPost returns all mentions for a given post.
func (s *Store) GetMentionsByPost(ctx context.Context, postID uuid.UUID) ([]PostMention, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, post_type, mentioned_user_id, created_at
		FROM post_mentions WHERE post_id = $1
		ORDER BY created_at
	`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mentions []PostMention
	for rows.Next() {
		var m PostMention
		if err := rows.Scan(&m.ID, &m.PostID, &m.PostType, &m.MentionedUserID, &m.CreatedAt); err != nil {
			return nil, err
		}
		mentions = append(mentions, m)
	}
	return mentions, rows.Err()
}

// GetMentionsForUser returns recent posts mentioning a user.
func (s *Store) GetMentionsForUser(ctx context.Context, userID string, limit int) ([]PostMention, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, post_type, mentioned_user_id, created_at
		FROM post_mentions WHERE mentioned_user_id = $1
		ORDER BY created_at DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mentions []PostMention
	for rows.Next() {
		var m PostMention
		if err := rows.Scan(&m.ID, &m.PostID, &m.PostType, &m.MentionedUserID, &m.CreatedAt); err != nil {
			return nil, err
		}
		mentions = append(mentions, m)
	}
	return mentions, rows.Err()
}
