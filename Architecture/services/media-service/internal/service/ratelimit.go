package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// CheckUploadRateLimit enforces per-user upload frequency using Redis
// sliding-window counters (INCR + EXPIRE).
//
// Limits:
//   - MaxUploadsPerHour  (10) per rolling hour
//   - MaxUploadsPerDay   (30) per rolling day
//
// If rdb is nil rate limiting is silently skipped (useful in tests / local dev).
func (s *Service) CheckUploadRateLimit(ctx context.Context, userID uuid.UUID) error {
	if s.rdb == nil {
		return nil
	}

	hourKey := fmt.Sprintf("upload:rate:%s:hour", userID)
	dayKey := fmt.Sprintf("upload:rate:%s:day", userID)

	// Hourly window
	if !uploadAllow(ctx, s.rdb, hourKey, int64(MaxUploadsPerHour), time.Hour) {
		slog.Warn("upload rate limit exceeded (hourly)", "user_id", userID)
		return fmt.Errorf("upload rate limit exceeded: max %d uploads per hour", MaxUploadsPerHour)
	}

	// Daily window
	if !uploadAllow(ctx, s.rdb, dayKey, int64(MaxUploadsPerDay), 24*time.Hour) {
		slog.Warn("upload rate limit exceeded (daily)", "user_id", userID)
		return fmt.Errorf("upload rate limit exceeded: max %d uploads per day", MaxUploadsPerDay)
	}

	return nil
}

// uploadAllow returns true when the counter for key is within limit.
func uploadAllow(ctx context.Context, rdb *redis.Client, key string, limit int64, window time.Duration) bool {
	pipe := rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("redis pipeline error in upload rate limit", "key", key, "error", err)
		return true // fail-open so uploads aren't blocked by a Redis outage
	}
	count, _ := incr.Result()
	return count <= limit
}
