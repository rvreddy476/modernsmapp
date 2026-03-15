package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	bundleThreshold = 5
	bundleTTL       = 1800 // 30 minutes in seconds
)

// BundleNotification groups similar notifications (likes, follows, etc.) to reduce noise.
//
// Flow:
//  1. Increment a Redis counter key: notify_bundle:{userID}:{bundleType}:{refID_or_global}
//  2. Add actorID to a Redis set for the same key with a 30-minute TTL.
//  3. If count >= bundleThreshold, upsert the bundle in Postgres so it can be flushed
//     and a push notification sent.
//  4. Always upsert in Postgres for persistence regardless of count.
func (s *Service) BundleNotification(ctx context.Context, userID, actorID uuid.UUID, bundleType string, refID *uuid.UUID) error {
	refKey := "global"
	if refID != nil {
		refKey = refID.String()
	}

	redisKey := fmt.Sprintf("notify_bundle:%s:%s:%s", userID.String(), bundleType, refKey)
	actorSetKey := redisKey + ":actors"

	// Increment count and set TTL.
	count, err := s.rdb.Incr(ctx, redisKey).Result()
	if err != nil {
		slog.Warn("bundle: redis INCR failed", "key", redisKey, "error", err)
	} else {
		s.rdb.Expire(ctx, redisKey, time.Duration(bundleTTL)*time.Second)
	}

	// Track unique actor IDs in a set.
	s.rdb.SAdd(ctx, actorSetKey, actorID.String())
	s.rdb.Expire(ctx, actorSetKey, time.Duration(bundleTTL)*time.Second)

	// Persist to Postgres for durability.
	if s.pgStore != nil {
		bundle, upsertErr := s.pgStore.UpsertBundle(ctx, userID, bundleType, refID, actorID)
		if upsertErr != nil {
			slog.Error("bundle: upsert failed", "error", upsertErr)
		} else if count >= bundleThreshold {
			// Threshold reached — send push and mark as sent.
			if s.pusher != nil {
				tokens, devErr := s.pgStore.GetUserDevices(ctx, userID)
				if devErr == nil && len(tokens) > 0 {
					title, body := bundleTitleBody(bundleType, int(count))
					for _, t := range tokens {
						if pushErr := s.pusher.Send(ctx, t.PushToken, t.Platform, title, body,
							map[string]string{"type": bundleType}); pushErr != nil {
							slog.Warn("bundle: push send failed", "error", pushErr, "platform", t.Platform)
						}
					}
				}
			}
			if markErr := s.pgStore.MarkBundleSent(ctx, bundle.ID); markErr != nil {
				slog.Warn("bundle: mark sent failed", "error", markErr)
			}
			// Reset Redis counter so the next wave starts fresh.
			s.rdb.Del(ctx, redisKey, actorSetKey)
		}
	}

	return nil
}

// FlushExpiredBundles queries Postgres for bundles that are older than 30 minutes
// or have reached the threshold, sends push notifications for them, then marks them sent.
// This is intended to be called by a background goroutine on a periodic ticker.
func (s *Service) FlushExpiredBundles(ctx context.Context) error {
	if s.pgStore == nil {
		return nil
	}
	bundles, err := s.pgStore.GetPendingBundles(ctx, bundleThreshold)
	if err != nil {
		return fmt.Errorf("flush bundles: query failed: %w", err)
	}

	for _, b := range bundles {
		if s.pusher != nil {
			tokens, devErr := s.pgStore.GetUserDevices(ctx, b.UserID)
			if devErr == nil && len(tokens) > 0 {
				title, body := bundleTitleBody(b.BundleType, b.Count)
				for _, t := range tokens {
					if pushErr := s.pusher.Send(ctx, t.PushToken, t.Platform, title, body,
						map[string]string{"type": b.BundleType}); pushErr != nil {
						slog.Warn("flush bundle: push send failed", "error", pushErr)
					}
				}
			}
		}
		if err := s.pgStore.MarkBundleSent(ctx, b.ID); err != nil {
			slog.Warn("flush bundle: mark sent failed", "id", b.ID, "error", err)
		}
	}
	return nil
}

// CreateDigest creates a weekly or monthly digest for a user.
func (s *Service) CreateDigest(ctx context.Context, userID uuid.UUID, periodType string, periodStart time.Time, content map[string]any) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("digest: marshal content: %w", err)
	}
	d := &postgres.NotificationDigest{
		UserID:      userID,
		PeriodType:  periodType,
		PeriodStart: periodStart,
		Content:     raw,
	}
	return s.pgStore.CreateDigest(ctx, d)
}

// GetDigests returns recent digests for a user.
func (s *Service) GetDigests(ctx context.Context, userID uuid.UUID) ([]postgres.NotificationDigest, error) {
	if s.pgStore == nil {
		return nil, fmt.Errorf("PG store not configured")
	}
	return s.pgStore.GetDigests(ctx, userID, 20)
}

// bundleTitleBody returns a human-readable push title/body for a bundle notification.
func bundleTitleBody(bundleType string, count int) (string, string) {
	switch bundleType {
	case "like", "reaction":
		return "New Reactions", fmt.Sprintf("%d people reacted to your post", count)
	case "follow":
		return "New Followers", fmt.Sprintf("%d people followed you", count)
	case "comment":
		return "New Comments", fmt.Sprintf("%d people commented on your post", count)
	case "friend_request":
		return "Friend Requests", fmt.Sprintf("You have %d new friend requests", count)
	default:
		return "New Activity", fmt.Sprintf("You have %d new notifications", count)
	}
}
