package consumers

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// BaseConsumer provides Redis SETNX-based dedup for analytics event consumers.
type BaseConsumer struct {
	rdb  *redis.Client
	name string
}

func NewBaseConsumer(rdb *redis.Client, name string) *BaseConsumer {
	return &BaseConsumer{rdb: rdb, name: name}
}

// IsDuplicate returns true if the event was already processed.
// Uses SETNX with 24h TTL for idempotent consumption.
func (c *BaseConsumer) IsDuplicate(ctx context.Context, eventID string) bool {
	key := fmt.Sprintf("consumed:%s:%s", c.name, eventID)
	set, err := c.rdb.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil {
		return false // On Redis error, allow processing (at-least-once)
	}
	return !set // SetNX returns true if key was SET (new), false if already exists
}
