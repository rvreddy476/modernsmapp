package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Post-frequency caps.
//
// Values are the production defaults and are overridable per environment so a
// durability proof can issue 50 concurrent same-key writes — C-CLB-PROOF-1.
// Without that, the limiter answers most of them 429 before the durable
// idempotency gate is ever reached, which proves the limiter works and nothing
// about idempotency.
//
// Variables rather than constants purely so the environment can set them at
// boot. Nothing mutates them afterwards.
var (
	// MaxPostsPerHour is the maximum posts a user can create per rolling hour.
	MaxPostsPerHour int64 = envInt64("MAX_POSTS_PER_HOUR", 20)
	// MaxPostsPerDay is the maximum posts a user can create per rolling day.
	MaxPostsPerDay int64 = envInt64("MAX_POSTS_PER_DAY", 50)
)

// envInt64 reads a positive integer environment variable, falling back on
// anything unset, unparseable or non-positive.
//
// Non-positive falls back deliberately: a cap of zero would reject every post,
// and a typo in a deployment variable must not be able to stop the product
// working.
func envInt64(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		slog.Warn("ignoring invalid integer env var", "key", key, "value", raw)
		return fallback
	}
	return parsed
}

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
