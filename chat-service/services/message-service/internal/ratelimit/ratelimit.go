// Package ratelimit provides a Redis-backed fixed-window rate limiter for
// chat-service per-user actions (messaging/privacy spec §10.4).
//
// It is a service-local package: it deliberately does not depend on any
// shared package so message-service can tune its own limits independently.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limit describes a fixed-window quota: at most Max actions per Window.
type Limit struct {
	// Action is a short key fragment identifying the limited operation
	// (e.g. "dm", "message_request").
	Action string
	// Max is the maximum number of actions allowed within Window.
	Max int
	// Window is the length of the rate-limit window.
	Window time.Duration
}

// Limiter is a Redis INCR + EXPIRE sliding-window-ish (fixed-window) limiter.
type Limiter struct {
	rdb *redis.Client
}

// New returns a Limiter backed by the given Redis client.
func New(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb}
}

// key derives the Redis counter key for a (action, user, window) tuple. The
// window component buckets the key by Window so a new counter starts each
// window without needing a separate reset.
func key(l Limit, userID string) string {
	windowSecs := int64(l.Window / time.Second)
	if windowSecs <= 0 {
		windowSecs = 1
	}
	bucket := time.Now().Unix() / windowSecs
	return fmt.Sprintf("rate:%s:%s:%d", l.Action, userID, bucket)
}

// Allow records one action for userID under the given Limit and reports
// whether it is within quota. The first call in a window also sets the key's
// TTL so stale counters expire.
//
// If Redis is unavailable the limiter fails OPEN (returns allowed=true) so a
// Redis outage degrades into "no rate limiting" rather than blocking all
// messaging; the underlying error is returned for the caller to log.
func (l *Limiter) Allow(ctx context.Context, limit Limit, userID string) (bool, error) {
	if l == nil || l.rdb == nil {
		return true, nil
	}
	k := key(limit, userID)

	pipe := l.rdb.Pipeline()
	incr := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, limit.Window)
	if _, err := pipe.Exec(ctx); err != nil {
		// Fail open — do not let a Redis blip brick messaging.
		return true, err
	}

	count := incr.Val()
	return count <= int64(limit.Max), nil
}
