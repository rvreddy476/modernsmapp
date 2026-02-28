package reconcile

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"
)

// ViewReconciler periodically reconciles Redis view counters with ScyllaDB
// watch session counts. It pops entries from the hot:videos set and compares
// display view counts between the two stores, correcting Redis when the drift
// exceeds 5%.
type ViewReconciler struct {
	rdb    *redis.Client
	scylla *gocql.Session
}

// NewViewReconciler creates a new ViewReconciler.
func NewViewReconciler(rdb *redis.Client, scylla *gocql.Session) *ViewReconciler {
	return &ViewReconciler{rdb: rdb, scylla: scylla}
}

// Start blocks on a 5-minute ticker, running reconcile on each tick until the
// context is cancelled.
func (vr *ViewReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	log.Println("[ViewReconciler] started, reconciling every 5 minutes")

	// Run once immediately on start.
	vr.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[ViewReconciler] stopping: context cancelled")
			return
		case <-ticker.C:
			vr.reconcile(ctx)
		}
	}
}

// reconcile pops members from the hot:videos Redis SET and, for each content
// ID, compares the display view count in ScyllaDB against the Redis counter.
// If drift exceeds 5%, it overwrites the Redis value with the ScyllaDB count.
func (vr *ViewReconciler) reconcile(ctx context.Context) {
	reconciled := 0
	corrections := 0

	for {
		// Pop up to 100 members at a time from hot:videos.
		members, err := vr.rdb.SPopN(ctx, "hot:videos", 100).Result()
		if err != nil && err != redis.Nil {
			log.Printf("[ViewReconciler] error popping from hot:videos: %v", err)
			break
		}
		if len(members) == 0 {
			break
		}

		for _, contentID := range members {
			corrected, err := vr.reconcileOne(ctx, contentID)
			if err != nil {
				log.Printf("[ViewReconciler] error reconciling content %s: %v", contentID, err)
				continue
			}
			reconciled++
			if corrected {
				corrections++
			}
		}
	}

	log.Printf("[ViewReconciler] reconciled %d items, corrected %d drifts", reconciled, corrections)
}

// reconcileOne compares a single content ID's display view count between
// ScyllaDB and Redis. Returns true if a correction was applied.
func (vr *ViewReconciler) reconcileOne(ctx context.Context, contentID string) (bool, error) {
	// 1. Count display views in ScyllaDB watch_sessions.
	var scyllaCount int64
	if err := vr.scylla.Query(
		`SELECT COUNT(*) FROM watch_sessions WHERE content_id = ? AND is_display_view = true ALLOW FILTERING`,
		contentID,
	).WithContext(ctx).Scan(&scyllaCount); err != nil {
		return false, fmt.Errorf("scylla count query: %w", err)
	}

	// 2. Read the current display count from Redis.
	redisVal, err := vr.rdb.HGet(ctx, fmt.Sprintf("post:views:%s", contentID), "display").Result()
	if err != nil && err != redis.Nil {
		return false, fmt.Errorf("redis hget: %w", err)
	}

	var redisCount int64
	if redisVal != "" {
		redisCount, err = strconv.ParseInt(redisVal, 10, 64)
		if err != nil {
			return false, fmt.Errorf("parse redis display count: %w", err)
		}
	}

	// 3. Compare and correct if drift exceeds 5%.
	if scyllaCount == 0 && redisCount == 0 {
		return false, nil
	}

	var drift float64
	if scyllaCount == 0 {
		drift = 1.0 // Redis has a count but ScyllaDB does not -- full drift.
	} else {
		drift = float64(abs64(scyllaCount-redisCount)) / float64(scyllaCount)
	}

	if drift > 0.05 {
		if err := vr.rdb.HSet(
			ctx,
			fmt.Sprintf("post:views:%s", contentID),
			"display",
			strconv.FormatInt(scyllaCount, 10),
		).Err(); err != nil {
			return false, fmt.Errorf("redis hset correction: %w", err)
		}
		log.Printf("[ViewReconciler] corrected content %s: redis=%d scylla=%d drift=%.2f%%",
			contentID, redisCount, scyllaCount, drift*100)
		return true, nil
	}

	return false, nil
}

// abs64 returns the absolute value of an int64.
func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
