// Package ratelimit provides per-user, per-action Redis sliding-window
// rate limiting for graph-service write actions (spec §10.4).
//
// The limiter keys a counter per (action, user, 24h window) and uses an
// atomic INCR + EXPIRE. On any Redis error it fails OPEN — a Redis blip
// must never block a user from following or sending connection requests.
package ratelimit

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Action identifiers (spec §10.4).
const (
	ActionConnectionRequest = "connection_request"
	ActionFollow            = "follow"
	ActionCloseFriendAdd    = "close_friend_add"
)

// Per-action limits over a rolling 24h window (spec §10.4).
const (
	// ConnectionRequestLimit caps connection requests at 30 per 24h.
	ConnectionRequestLimit = 30
	// FollowLimit caps follows at 200 per 24h.
	FollowLimit = 200
	// CloseFriendAddLimit caps Trusted Circle adds at 30 per 24h.
	CloseFriendAddLimit = 30
	// window is the bucket duration for all actions.
	window = 24 * time.Hour
)

// limits maps an action to its allowed count per window.
var limits = map[string]int{
	ActionConnectionRequest: ConnectionRequestLimit,
	ActionFollow:            FollowLimit,
	ActionCloseFriendAdd:    CloseFriendAddLimit,
}

// Limiter enforces per-user action quotas backed by Redis.
type Limiter struct {
	rdb *redis.Client
}

// New constructs a Limiter over the given Redis client.
func New(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb}
}

// Allow reports whether userID may perform action right now. It increments
// the current 24h bucket and returns false once the count exceeds the
// action's limit. Unknown actions are always allowed.
//
// On a Redis error it fails OPEN: returns (true, nil) and logs, so a Redis
// outage degrades to "no limiting" rather than blocking every write.
func (l *Limiter) Allow(ctx context.Context, action string, userID uuid.UUID) (bool, error) {
	limit, ok := limits[action]
	if !ok {
		return true, nil
	}
	if l == nil || l.rdb == nil {
		// No Redis configured — fail open.
		return true, nil
	}

	// Bucket the window so the key naturally rolls over and expires.
	bucket := time.Now().UTC().Unix() / int64(window/time.Second)
	key := fmt.Sprintf("rate:%s:%s:%d", action, userID, bucket)

	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("[graph] ratelimit Incr failed for %s (fail-open): %v", key, err)
		return true, nil
	}
	// Set the TTL once, on the first increment of a fresh bucket.
	if count == 1 {
		if err := l.rdb.Expire(ctx, key, window).Err(); err != nil {
			log.Printf("[graph] ratelimit Expire failed for %s: %v", key, err)
		}
	}

	return count <= int64(limit), nil
}
