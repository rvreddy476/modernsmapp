package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/google/uuid"
)

// HidePost hides a specific post from the user's feed and caches the result in Redis.
func (s *Service) HidePost(ctx context.Context, userID, postID uuid.UUID) error {
	if err := s.pgStore.HidePost(ctx, userID, postID); err != nil {
		return err
	}
	// Cache in Redis for fast feed filtering
	key := fmt.Sprintf("hidden_posts:%s", userID.String())
	s.rdb.SAdd(ctx, key, postID.String())
	s.rdb.Expire(ctx, key, 24*time.Hour)
	return nil
}

// UnhidePost removes a hidden post for the user.
func (s *Service) UnhidePost(ctx context.Context, userID, postID uuid.UUID) error {
	if err := s.pgStore.UnhidePost(ctx, userID, postID); err != nil {
		return err
	}
	key := fmt.Sprintf("hidden_posts:%s", userID.String())
	s.rdb.SRem(ctx, key, postID.String())
	return nil
}

// MuteTarget mutes a user, topic, or hashtag for the given user.
func (s *Service) MuteTarget(ctx context.Context, userID uuid.UUID, targetType, targetID string, expiresAt *time.Time) error {
	validTypes := map[string]bool{"user": true, "topic": true, "hashtag": true}
	if !validTypes[targetType] {
		return fmt.Errorf("invalid target_type: must be user, topic, or hashtag")
	}
	if err := s.pgStore.MuteTarget(ctx, userID, targetType, targetID, expiresAt); err != nil {
		return err
	}
	// Cache muted targets for fast feed filtering
	if targetType == "user" {
		key := fmt.Sprintf("muted_users:%s", userID.String())
		s.rdb.SAdd(ctx, key, targetID)
		s.rdb.Expire(ctx, key, time.Hour)
	} else {
		key := fmt.Sprintf("muted_topics:%s", userID.String())
		s.rdb.SAdd(ctx, key, targetID)
		s.rdb.Expire(ctx, key, time.Hour)
	}
	return nil
}

// UnmuteTarget removes a mute for the given user.
func (s *Service) UnmuteTarget(ctx context.Context, userID uuid.UUID, targetType, targetID string) error {
	if err := s.pgStore.UnmuteTarget(ctx, userID, targetType, targetID); err != nil {
		return err
	}
	if targetType == "user" {
		s.rdb.SRem(ctx, fmt.Sprintf("muted_users:%s", userID.String()), targetID)
	} else {
		s.rdb.SRem(ctx, fmt.Sprintf("muted_topics:%s", userID.String()), targetID)
	}
	return nil
}

// GetMutedTargets returns all active (non-expired) mutes for the given user.
func (s *Service) GetMutedTargets(ctx context.Context, userID uuid.UUID) ([]postgres.FeedMute, error) {
	return s.pgStore.GetMutedTargets(ctx, userID)
}
