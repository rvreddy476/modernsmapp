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
	// The feed reads feed_mutes on every request (feedmutes.go); the Redis
	// sets this used to write were never read, so they are gone.
	if err := validateMuteTarget(targetType, targetID); err != nil {
		return err
	}
	if err := muteExpiry(expiresAt, time.Now()); err != nil {
		return err
	}
	if targetType != "user" {
		targetID = normalizeMuteWord(targetID)
	}
	return s.pgStore.MuteTarget(ctx, userID, targetType, targetID, expiresAt)
}

// UnmuteTarget removes a mute for the given user.
func (s *Service) UnmuteTarget(ctx context.Context, userID uuid.UUID, targetType, targetID string) error {
	if verr := validateMuteTarget(targetType, targetID); verr != nil {
		return verr
	}
	if targetType != "user" {
		targetID = normalizeMuteWord(targetID)
	}
	return s.pgStore.UnmuteTarget(ctx, userID, targetType, targetID)
}

// GetMutedTargets returns all active (non-expired) mutes for the given user.
func (s *Service) GetMutedTargets(ctx context.Context, userID uuid.UUID) ([]postgres.FeedMute, error) {
	return s.pgStore.GetMutedTargets(ctx, userID)
}
