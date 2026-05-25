// Package counters implements sharded Redis counters with periodic
// PostgreSQL flush — the production realtime architecture pattern for
// hot rows like community member_count, channel subscriber_count,
// hashtag use_count, audio use_count, post view_count.
//
// Why shard:
//
//	UPDATE communities SET member_count = member_count + 1 WHERE id = $1;
//
// works fine until a single community has millions of members and
// thousands of joins per second. Every UPDATE then contends on the same
// row — lock contention, retries, DB CPU, slow writes. Sharding splits
// the counter across N Redis keys so increments distribute, and a
// flush worker batches writes back to PG every few seconds.
//
// Usage:
//
//	c := counters.New(rdb, counters.Config{
//	    EntityKind: "community_member_count",
//	    Shards: 32,
//	})
//	c.Inc(ctx, communityID.String(), +1)  // join
//	c.Inc(ctx, communityID.String(), -1)  // leave
//	total, _ := c.Read(ctx, communityID.String())  // sum of all shards
//
// A separate Worker drains the dirty-set and writes totals to PG.
// Redis is buffer, PG is truth — see (*Worker).Flush.
package counters

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Config tunes the sharding behaviour.
type Config struct {
	// EntityKind is a short label used in Redis key namespacing, e.g.
	// "community_member_count", "channel_subscriber_count". Must be
	// stable across deploys — renaming silently leaves the old keys
	// stranded.
	EntityKind string

	// Shards is the fan-out — increments are scattered across this many
	// keys per entity. Default 32. Higher = more write parallelism, more
	// memory + slower reads (Read must touch every shard).
	Shards int
}

func (c Config) withDefaults() Config {
	if c.Shards <= 0 {
		c.Shards = 32
	}
	if c.EntityKind == "" {
		// Force a panic during init rather than silently producing
		// keys like counter::id:0 that collide across kinds.
		panic("counters: Config.EntityKind is required")
	}
	return c
}

// Counter is a sharded counter for one entity kind.
type Counter struct {
	rdb *redis.Client
	cfg Config
}

func New(rdb *redis.Client, cfg Config) *Counter {
	return &Counter{rdb: rdb, cfg: cfg.withDefaults()}
}

func (c *Counter) shardKey(entityID string, shard int) string {
	return fmt.Sprintf("counter:%s:%s:%d", c.cfg.EntityKind, entityID, shard)
}

// DirtySetKey returns the set Redis key the Worker reads to find
// entities whose counters changed since the last flush. Exported so
// callers can inspect / clean it during recovery.
func (c *Counter) DirtySetKey() string {
	return "dirty_counters:" + c.cfg.EntityKind
}

func randShard(n int) int {
	if n <= 1 {
		return 0
	}
	// crypto/rand to keep the distribution uniform across many
	// concurrent callers — math/rand without seeding is process-shared
	// and was the original "hot shard 0" surprise on a busy node.
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// Inc applies delta (positive or negative) to a randomly chosen shard
// and marks the entity dirty so the flush worker writes the new total
// back to PG. delta=0 is a no-op.
func (c *Counter) Inc(ctx context.Context, entityID string, delta int64) error {
	if delta == 0 {
		return nil
	}
	shard := randShard(c.cfg.Shards)
	pipe := c.rdb.Pipeline()
	pipe.IncrBy(ctx, c.shardKey(entityID, shard), delta)
	pipe.SAdd(ctx, c.DirtySetKey(), entityID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("counter inc: %w", err)
	}
	return nil
}

// Read returns the current sum across all shards. Pipeline'd so it's
// one round-trip even with 32+ shards. Returns 0 + nil if no shards
// have been written yet.
func (c *Counter) Read(ctx context.Context, entityID string) (int64, error) {
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, c.cfg.Shards)
	for i := 0; i < c.cfg.Shards; i++ {
		cmds[i] = pipe.Get(ctx, c.shardKey(entityID, i))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, fmt.Errorf("counter read: %w", err)
	}
	var sum int64
	for _, cmd := range cmds {
		val, err := cmd.Result()
		if err == redis.Nil || val == "" {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("counter read shard: %w", err)
		}
		n, parseErr := strconv.ParseInt(val, 10, 64)
		if parseErr != nil {
			// Skip malformed shards rather than wedge the whole read —
			// a single bad value shouldn't blank a counter for everyone.
			continue
		}
		sum += n
	}
	return sum, nil
}

// Seed initializes the counter from a known PG total by writing it
// into shard 0 and clearing the rest. Use this when bootstrapping a
// new shard topology or recovering from a Redis flush.
func (c *Counter) Seed(ctx context.Context, entityID string, total int64) error {
	pipe := c.rdb.Pipeline()
	for i := 1; i < c.cfg.Shards; i++ {
		pipe.Del(ctx, c.shardKey(entityID, i))
	}
	pipe.Set(ctx, c.shardKey(entityID, 0), strconv.FormatInt(total, 10), 0)
	pipe.SAdd(ctx, c.DirtySetKey(), entityID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("counter seed: %w", err)
	}
	return nil
}

// PopDirty atomically removes up to limit entity IDs from the dirty
// set and returns them. The flush worker uses this to claim a batch
// of entities to write to PG without locking.
func (c *Counter) PopDirty(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	// SPOP n is atomic and removes immediately — if the flush fails
	// we re-mark these dirty.
	vals, err := c.rdb.SPopN(ctx, c.DirtySetKey(), int64(limit)).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("counter pop dirty: %w", err)
	}
	return vals, nil
}

// MarkDirty re-marks entityIDs as needing a flush. Used by the flush
// worker after a failed PG write so the entries don't get dropped
// permanently.
func (c *Counter) MarkDirty(ctx context.Context, entityIDs ...string) error {
	if len(entityIDs) == 0 {
		return nil
	}
	args := make([]interface{}, len(entityIDs))
	for i, id := range entityIDs {
		args[i] = id
	}
	return c.rdb.SAdd(ctx, c.DirtySetKey(), args...).Err()
}
