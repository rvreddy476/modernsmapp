package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ErrUploadLimiterUnavailable: the per-user upload count could not be read
// (no Redis, or a Redis error), so upload init is refused rather than let
// through unlimited (lv-3: it used to fail open). The HTTP layer answers 503
// UPLOAD_LIMITER_UNAVAILABLE with Retry-After; the client may retry.
var ErrUploadLimiterUnavailable = errors.New("upload limiter unavailable: try again shortly")

// CheckUploadRateLimit enforces per-user upload frequency using Redis
// sliding-window counters (INCR + EXPIRE).
//
// Limits:
//   - MaxUploadsPerHour  (10) per rolling hour
//   - MaxUploadsPerDay   (30) per rolling day
//
// It fails CLOSED: with no Redis, or on a Redis error, it returns
// ErrUploadLimiterUnavailable. Only MEDIA_UPLOAD_RATE_LIMIT_OPTIONAL=true
// (local runs with no Redis) skips it when rdb is nil.
func (s *Service) CheckUploadRateLimit(ctx context.Context, userID uuid.UUID) error {
	if s.rdb == nil {
		if uploadLimits.RateLimitOptional {
			return nil
		}
		slog.Error("upload refused: no Redis for the upload rate limit", "user_id", userID)
		return fmt.Errorf("%w: no rate-limit store", ErrUploadLimiterUnavailable)
	}

	hourKey := fmt.Sprintf("upload:rate:%s:hour", userID)
	dayKey := fmt.Sprintf("upload:rate:%s:day", userID)

	// Hourly window
	ok, err := uploadAllowClosed(ctx, s.rdb, hourKey, int64(MaxUploadsPerHour), time.Hour)
	if err != nil {
		return err
	}
	if !ok {
		slog.Warn("upload rate limit exceeded (hourly)", "user_id", userID)
		return fmt.Errorf("upload rate limit exceeded: max %d uploads per hour", MaxUploadsPerHour)
	}

	// Daily window
	ok, err = uploadAllowClosed(ctx, s.rdb, dayKey, int64(MaxUploadsPerDay), 24*time.Hour)
	if err != nil {
		return err
	}
	if !ok {
		slog.Warn("upload rate limit exceeded (daily)", "user_id", userID)
		return fmt.Errorf("upload rate limit exceeded: max %d uploads per day", MaxUploadsPerDay)
	}

	return nil
}

// uploadAllowClosed is uploadAllow that reports a Redis error instead of
// allowing: the caller refuses (ErrUploadLimiterUnavailable).
func uploadAllowClosed(ctx context.Context, rdb *redis.Client, key string, limit int64, window time.Duration) (bool, error) {
	pipe := rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis pipeline error in upload rate limit; refusing", "key", key, "error", err)
		return false, fmt.Errorf("%w: %v", ErrUploadLimiterUnavailable, err)
	}
	count, err := incr.Result()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUploadLimiterUnavailable, err)
	}
	return count <= limit, nil
}

// uploadAllow returns true when the counter for key is within limit. It
// fails OPEN on a Redis error; only the document budget (document.go, not
// this lane) still uses it. Upload init uses uploadAllowClosed.
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
