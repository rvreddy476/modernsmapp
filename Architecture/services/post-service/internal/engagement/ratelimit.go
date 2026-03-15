package engagement

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter implements a Redis sorted-set sliding window rate limiter.
// Each action gets a unique sorted-set key. Members are scored by timestamp.
// The window slides forward on each check, pruning expired entries.
type RateLimiter struct {
	rdb *redis.Client
}

// NewRateLimiter creates a new rate limiter backed by Redis.
func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

// rateLimitScript atomically checks and records a rate limit entry.
//
// KEYS[1] = rate limit key (e.g. "rl:like:{user_id}")
// ARGV[1] = now (milliseconds)
// ARGV[2] = windowStart (milliseconds)
// ARGV[3] = limit (max allowed in window)
// ARGV[4] = window (milliseconds, for EXPIRE TTL)
//
// Returns: 1 if allowed, 0 if rate limited
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

// Allow checks if the action is within the rate limit.
// key should be unique per action+user, e.g. "rl:like:{user_id}".
// limit is the max number of actions allowed in the window.
func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) bool {
	now := time.Now().UnixMilli()
	windowStart := now - window.Milliseconds()

	result, err := rateLimitScript.Run(ctx, rl.rdb, []string{key}, now, windowStart, limit, window.Milliseconds()).Int64()
	if err != nil {
		// On Redis errors, allow the request (fail-open)
		return true
	}

	return result == 1
}

// Predefined rate limit configurations per the spec
var (
	LikeLimitPerHour       = 120
	CommentLimitPerMin     = 10
	ShareLimitPerHour      = 30
	BookmarkLimitPerHour   = 200
	ReplyLimitPerHour      = 20
	CommentLikeLimitPerHour = 120
	CrosspostLimitPerHour   = 5
)
