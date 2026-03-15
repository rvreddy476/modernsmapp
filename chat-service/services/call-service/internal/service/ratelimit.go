package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrCallRateLimitExceeded   = errors.New("call rate limit exceeded: max 30 calls per hour")
	ErrInviteRateLimitExceeded = errors.New("invite rate limit exceeded: max 100 invites per day")
	ErrJoinRateLimitExceeded   = errors.New("join rate limit exceeded: max 5 joins per minute")
	ErrRingAntiSpam            = errors.New("anti-spam: too many unanswered calls to this user, cooldown active")
)

// RateLimiter uses Redis sliding-window counters for call rate limiting.
type RateLimiter struct {
	rdb *redis.Client
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

// CheckCallRate enforces 30 calls/hour per user.
func (r *RateLimiter) CheckCallRate(ctx context.Context, userID uuid.UUID) error {
	key := fmt.Sprintf("rl:call:create:%s", userID)
	return r.checkLimit(ctx, key, 30, time.Hour)
}

// CheckInviteRate enforces 100 invites/day per user.
func (r *RateLimiter) CheckInviteRate(ctx context.Context, userID uuid.UUID) error {
	key := fmt.Sprintf("rl:call:invite:%s", userID)
	return r.checkLimit(ctx, key, 100, 24*time.Hour)
}

// CheckJoinRate enforces 5 joins/minute per user per call.
func (r *RateLimiter) CheckJoinRate(ctx context.Context, userID uuid.UUID, callID uuid.UUID) error {
	key := fmt.Sprintf("rl:call:join:%s:%s", userID, callID)
	return r.checkLimit(ctx, key, 5, time.Minute)
}

// CheckRingAntiSpam enforces 3 unanswered calls in 5 minutes → 30 minute cooldown.
func (r *RateLimiter) CheckRingAntiSpam(ctx context.Context, callerID, targetID uuid.UUID) error {
	cooldownKey := fmt.Sprintf("rl:call:cooldown:%s:%s", callerID, targetID)
	exists, err := r.rdb.Exists(ctx, cooldownKey).Result()
	if err != nil {
		return nil // fail open
	}
	if exists > 0 {
		return ErrRingAntiSpam
	}

	ringKey := fmt.Sprintf("rl:call:ring:%s:%s", callerID, targetID)
	count, err := r.rdb.Incr(ctx, ringKey).Result()
	if err != nil {
		return nil // fail open
	}
	if count == 1 {
		r.rdb.Expire(ctx, ringKey, 5*time.Minute)
	}
	if count >= 3 {
		r.rdb.Set(ctx, cooldownKey, "1", 30*time.Minute)
		r.rdb.Del(ctx, ringKey)
		return ErrRingAntiSpam
	}
	return nil
}

// ClearRingCounter resets the ring counter when a call is answered.
func (r *RateLimiter) ClearRingCounter(ctx context.Context, callerID, targetID uuid.UUID) {
	ringKey := fmt.Sprintf("rl:call:ring:%s:%s", callerID, targetID)
	r.rdb.Del(ctx, ringKey)
}

func (r *RateLimiter) checkLimit(ctx context.Context, key string, maxCount int64, window time.Duration) error {
	pipe := r.rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil // fail open on Redis errors
	}
	if incrCmd.Val() > maxCount {
		switch {
		case maxCount == 30:
			return ErrCallRateLimitExceeded
		case maxCount == 100:
			return ErrInviteRateLimitExceeded
		case maxCount == 5:
			return ErrJoinRateLimitExceeded
		default:
			return fmt.Errorf("rate limit exceeded")
		}
	}
	return nil
}
