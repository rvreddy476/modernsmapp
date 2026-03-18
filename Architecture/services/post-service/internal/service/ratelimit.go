package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// MaxPostsPerHour is the maximum posts a user can create per rolling hour.
	MaxPostsPerHour int64 = 20
	// MaxPostsPerDay is the maximum posts a user can create per rolling day.
	MaxPostsPerDay int64 = 50
)

// CheckPostRateLimit enforces per-user post creation frequency using Redis
// sliding-window counters (INCR + EXPIRE).
func CheckPostRateLimit(ctx context.Context, rdb *redis.Client, userID uuid.UUID) error {
	if rdb == nil {
		return nil
	}

	hourKey := fmt.Sprintf("post:rate:%s:hour", userID)
	dayKey := fmt.Sprintf("post:rate:%s:day", userID)

	// Hourly window
	if !postAllow(ctx, rdb, hourKey, MaxPostsPerHour, time.Hour) {
		slog.Warn("post rate limit exceeded (hourly)", "user_id", userID)
		return fmt.Errorf("post rate limit exceeded: max %d posts per hour", MaxPostsPerHour)
	}

	// Daily window
	if !postAllow(ctx, rdb, dayKey, MaxPostsPerDay, 24*time.Hour) {
		slog.Warn("post rate limit exceeded (daily)", "user_id", userID)
		return fmt.Errorf("post rate limit exceeded: max %d posts per day", MaxPostsPerDay)
	}

	return nil
}

// postAllow returns true when the counter for key is within limit.
func postAllow(ctx context.Context, rdb *redis.Client, key string, limit int64, window time.Duration) bool {
	pipe := rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("redis pipeline error in post rate limit", "key", key, "error", err)
		return true // fail-open so posts aren't blocked by a Redis outage
	}
	count, _ := incr.Result()
	return count <= limit
}
