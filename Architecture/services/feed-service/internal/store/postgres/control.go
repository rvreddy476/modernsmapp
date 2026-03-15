package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type FeedMute struct {
	UserID     uuid.UUID  `json:"user_id"`
	TargetType string     `json:"target_type"`
	TargetID   string     `json:"target_id"`
	MutedAt    time.Time  `json:"muted_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// HidePost hides a post from the user's feed.
func (s *MetaStore) HidePost(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO feed_hides (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, postID)
	return err
}

// UnhidePost removes a hidden post.
func (s *MetaStore) UnhidePost(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM feed_hides WHERE user_id = $1 AND post_id = $2`,
		userID, postID)
	return err
}

// GetHiddenPostIDs returns all post IDs the user has hidden.
func (s *MetaStore) GetHiddenPostIDs(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT post_id::text FROM feed_hides WHERE user_id = $1`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MuteTarget mutes a user, topic, or hashtag.
func (s *MetaStore) MuteTarget(ctx context.Context, userID uuid.UUID, targetType, targetID string, expiresAt *time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO feed_mutes (user_id, target_type, target_id, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, target_type, target_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		userID, targetType, targetID, expiresAt)
	return err
}

// UnmuteTarget removes a mute.
func (s *MetaStore) UnmuteTarget(ctx context.Context, userID uuid.UUID, targetType, targetID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM feed_mutes WHERE user_id = $1 AND target_type = $2 AND target_id = $3`,
		userID, targetType, targetID)
	return err
}

// GetMutedTargets returns all active mutes for a user (non-expired).
func (s *MetaStore) GetMutedTargets(ctx context.Context, userID uuid.UUID) ([]FeedMute, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, target_type, target_id, muted_at, expires_at
		FROM feed_mutes
		WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY muted_at DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mutes []FeedMute
	for rows.Next() {
		var m FeedMute
		if err := rows.Scan(&m.UserID, &m.TargetType, &m.TargetID, &m.MutedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		mutes = append(mutes, m)
	}
	return mutes, rows.Err()
}

// GetMutedUserIDs returns user IDs muted by the given user (for feed filtering).
func (s *MetaStore) GetMutedUserIDs(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT target_id FROM feed_mutes
		WHERE user_id = $1 AND target_type = 'user' AND (expires_at IS NULL OR expires_at > NOW())`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
