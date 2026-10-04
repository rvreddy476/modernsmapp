// Package slotcache is the Redis cache of slot answers (≤30 s). It is never
// the source of truth: every error is a miss, and the hold insert with the
// calendar exclusion constraint decides any booking.
package slotcache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// opTimeout keeps a slow Redis from slowing slot answers.
const opTimeout = 150 * time.Millisecond

// Redis is the cache over a go-redis client.
type Redis struct{ rdb *redis.Client }

// New wraps a client (nil gives a nil cache, which callers skip).
func New(rdb *redis.Client) *Redis {
	if rdb == nil {
		return nil
	}
	return &Redis{rdb: rdb}
}

// Get reads a cached answer.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	v, err := r.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	return v, true
}

// Set stores an answer for ttl.
func (r *Redis) Set(ctx context.Context, key string, v []byte, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_ = r.rdb.Set(ctx, key, v, ttl).Err()
}
