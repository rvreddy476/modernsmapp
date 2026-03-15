package service

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter implements a Redis sorted-set sliding window rate limiter.
type RateLimiter struct {
	rdb *redis.Client
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

var rateLimitScript = redis.NewScript(`
	local key = KEYS[1]
	redis.call('ZREMRANGEBYSCORE', key, '-inf', ARGV[2])
	local count = redis.call('ZCARD', key)
	if count < tonumber(ARGV[3]) then
		redis.call('ZADD', key, ARGV[1], ARGV[1] .. ':' .. math.random(100000))
		redis.call('EXPIRE', key, math.ceil(tonumber(ARGV[4]) / 1000))
		return 1
	end
	return 0
`)

// Allow checks if the action is within the rate limit. Fail-open on Redis errors.
func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) bool {
	now := time.Now().UnixMilli()
	windowStart := now - window.Milliseconds()

	result, err := rateLimitScript.Run(ctx, rl.rdb, []string{key}, now, windowStart, limit, window.Milliseconds()).Int64()
	if err != nil {
		return true
	}

	return result == 1
}
