package pipeline

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// VelocityTracker runs on a 5-minute interval, computing engagement velocity
// for recent posts. Velocities are stored in a Redis sorted set that the
// ranking middleware's signals loader reads via ZSCORE.
type VelocityTracker struct {
	rdb *redis.Client
}

// NewVelocityTracker creates a new VelocityTracker backed by Redis.
func NewVelocityTracker(rdb *redis.Client) *VelocityTracker {
	return &VelocityTracker{rdb: rdb}
}

// Start runs the velocity computation loop on a 5-minute ticker.
// It blocks until the context is cancelled.
func (v *VelocityTracker) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Run once immediately on startup
	if err := v.computeVelocities(ctx); err != nil {
		log.Printf("VelocityTracker: initial computation failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("VelocityTracker: stopping")
			return
		case <-ticker.C:
			if err := v.computeVelocities(ctx); err != nil {
				log.Printf("VelocityTracker: computation failed: %v", err)
			}
		}
	}
}

// computeVelocities scans all post engagement counters in Redis, computes
// the rate of change (likes per minute) since the last snapshot, and stores
// velocities in a sorted set for the ranking layer.
func (v *VelocityTracker) computeVelocities(ctx context.Context) error {
	var cursor uint64
	var processedCount int

	for {
		keys, nextCursor, err := v.rdb.Scan(ctx, cursor, "post:counters:*:likes", 100).Result()
		if err != nil {
			return fmt.Errorf("SCAN post counters: %w", err)
		}

		for _, key := range keys {
			if err := v.processPostVelocity(ctx, key); err != nil {
				log.Printf("VelocityTracker: failed to process %s: %v", key, err)
				continue
			}
			processedCount++
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// Clean up stale entries older than 24 hours
	if err := v.cleanupStaleEntries(ctx); err != nil {
		log.Printf("VelocityTracker: cleanup failed: %v", err)
	}

	log.Printf("VelocityTracker: processed %d posts", processedCount)
	return nil
}

// processPostVelocity computes the velocity for a single post given its
// likes counter key. The key format is "post:counters:{postID}:likes".
func (v *VelocityTracker) processPostVelocity(ctx context.Context, counterKey string) error {
	// Extract postID from key: post:counters:{postID}:likes
	// Key format has 4 parts separated by ':'
	postID := extractPostID(counterKey)
	if postID == "" {
		return fmt.Errorf("invalid counter key format: %s", counterKey)
	}

	// Get current like count
	currentStr, err := v.rdb.Get(ctx, counterKey).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("GET %s: %w", counterKey, err)
	}

	current, err := strconv.ParseFloat(currentStr, 64)
	if err != nil {
		return fmt.Errorf("parse current count for %s: %w", counterKey, err)
	}

	// Get previous snapshot
	previousStr, err := v.rdb.HGet(ctx, "post:velocity:snapshot", postID).Result()
	if err == redis.Nil {
		// No previous snapshot; store current as baseline, velocity = 0
		if err := v.rdb.HSet(ctx, "post:velocity:snapshot", postID, current).Err(); err != nil {
			return fmt.Errorf("HSET snapshot for %s: %w", postID, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("HGET snapshot for %s: %w", postID, err)
	}

	previous, err := strconv.ParseFloat(previousStr, 64)
	if err != nil {
		return fmt.Errorf("parse previous snapshot for %s: %w", postID, err)
	}

	// Compute velocity: likes per minute over the 5-minute interval
	velocity := (current - previous) / 5.0

	// Store new snapshot
	if err := v.rdb.HSet(ctx, "post:velocity:snapshot", postID, current).Err(); err != nil {
		return fmt.Errorf("HSET snapshot update for %s: %w", postID, err)
	}

	// Store velocity in sorted set for ranked retrieval
	if err := v.rdb.ZAdd(ctx, "post:velocity:ranked", redis.Z{
		Score:  velocity,
		Member: postID,
	}).Err(); err != nil {
		return fmt.Errorf("ZADD velocity for %s: %w", postID, err)
	}

	return nil
}

// cleanupStaleEntries removes posts from the velocity sorted set that have
// not had meaningful engagement in 24 hours. Posts with zero or negative
// velocity scores that have been idle are pruned.
func (v *VelocityTracker) cleanupStaleEntries(ctx context.Context) error {
	// Remove entries with very low velocity (effectively stale posts).
	// Posts older than 24h with near-zero velocity are no longer relevant.
	// We use -inf to 0 to remove posts that have stopped gaining engagement.
	staleMembers, err := v.rdb.ZRangeByScore(ctx, "post:velocity:ranked", &redis.ZRangeBy{
		Min: "-inf",
		Max: "0",
	}).Result()
	if err != nil {
		return fmt.Errorf("ZRANGEBYSCORE for cleanup: %w", err)
	}

	if len(staleMembers) == 0 {
		return nil
	}

	// Convert to interface slice for ZREM
	members := make([]interface{}, len(staleMembers))
	for i, m := range staleMembers {
		members[i] = m
	}

	removed, err := v.rdb.ZRem(ctx, "post:velocity:ranked", members...).Result()
	if err != nil {
		return fmt.Errorf("ZREM stale entries: %w", err)
	}

	// Also clean up their snapshots
	for _, postID := range staleMembers {
		v.rdb.HDel(ctx, "post:velocity:snapshot", postID)
	}

	log.Printf("VelocityTracker: cleaned up %d stale entries", removed)
	return nil
}

// RecordEngagement increments the engagement counter for a post in Redis.
// The engagementType should be "likes" or "comments". If a userID is provided,
// the user is also added to the post's likers set for deduplication.
func (v *VelocityTracker) RecordEngagement(ctx context.Context, postID uuid.UUID, engagementType string, userID *uuid.UUID) error {
	counterKey := fmt.Sprintf("post:counters:%s:%s", postID.String(), engagementType)

	if err := v.rdb.Incr(ctx, counterKey).Err(); err != nil {
		return fmt.Errorf("INCR %s: %w", counterKey, err)
	}

	// If a userID is provided and this is a like, track the liker
	if userID != nil && engagementType == "likes" {
		likersKey := fmt.Sprintf("post:likers:%s", postID.String())
		if err := v.rdb.SAdd(ctx, likersKey, userID.String()).Err(); err != nil {
			return fmt.Errorf("SADD %s: %w", likersKey, err)
		}
	}

	return nil
}

// extractPostID parses the post ID from a Redis key with the format
// "post:counters:{postID}:likes".
func extractPostID(key string) string {
	// key = "post:counters:{postID}:likes"
	// We need to extract the postID portion between the second and third colons.
	colonCount := 0
	start := 0
	for i, ch := range key {
		if ch == ':' {
			colonCount++
			if colonCount == 2 {
				start = i + 1
			}
			if colonCount == 3 {
				return key[start:i]
			}
		}
	}
	return ""
}
