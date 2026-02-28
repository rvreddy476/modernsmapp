package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type UserProfile struct {
	UserID        uuid.UUID  `json:"user_id"`
	DisplayName   string     `json:"display_name"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// UpsertUserProfile inserts or updates a cached user profile.
// Uses an updated_at guard to prevent stale overwrites.
func (s *ConversationStore) UpsertUserProfile(ctx context.Context, userID uuid.UUID, displayName string, avatarMediaID *uuid.UUID) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.user_profiles (user_id, display_name, avatar_media_id, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE
		SET display_name = EXCLUDED.display_name,
		    avatar_media_id = EXCLUDED.avatar_media_id,
		    updated_at = EXCLUDED.updated_at
		WHERE chat.user_profiles.updated_at <= EXCLUDED.updated_at
	`, userID, displayName, avatarMediaID, now)
	return err
}

// GetUserProfiles batch-fetches user profiles by IDs.
func (s *ConversationStore) GetUserProfiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]UserProfile, error) {
	if len(userIDs) == 0 {
		return map[uuid.UUID]UserProfile{}, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT user_id, display_name, avatar_media_id, updated_at
		FROM chat.user_profiles
		WHERE user_id = ANY($1)
	`, userIDs)
	if err != nil {
		if err == pgx.ErrNoRows {
			return map[uuid.UUID]UserProfile{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	profiles := make(map[uuid.UUID]UserProfile)
	for rows.Next() {
		var p UserProfile
		if err := rows.Scan(&p.UserID, &p.DisplayName, &p.AvatarMediaID, &p.UpdatedAt); err != nil {
			return nil, err
		}
		profiles[p.UserID] = p
	}
	return profiles, rows.Err()
}
