package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// StartReconciliationWorker recomputes Redis unread notification counters
// from ScyllaDB weekly. This corrects any drift caused by missed decrements,
// race conditions, or Redis eviction.
func StartReconciliationWorker(ctx context.Context, scyllaSession *gocql.Session, rdb *redis.Client) {
	slog.Info("unread counter reconciliation worker started")
	ticker := time.NewTicker(7 * 24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("reconciliation worker stopped")
			return
		case <-ticker.C:
			reconcileCounters(ctx, scyllaSession, rdb)
		}
	}
}

// reconcileCounters scans ScyllaDB notification buckets for the last 3 months,
// counts unread notifications per user, and sets the corresponding Redis counter.
func reconcileCounters(ctx context.Context, scyllaSession *gocql.Session, rdb *redis.Client) {
	slog.Info("starting weekly unread counter reconciliation")
	start := time.Now()

	// Collect unread counts per user across the last 3 months of buckets.
	userUnread := make(map[uuid.UUID]int64)
	now := time.Now()

	for i := 0; i < 3; i++ {
		t := now.AddDate(0, -i, 0)
		bucket := t.Year()*100 + int(t.Month())

		iter := scyllaSession.Query(`
			SELECT user_id, is_read
			FROM notifications_by_user
			WHERE bucket = ? ALLOW FILTERING
		`, bucket).Iter()

		var uid gocql.UUID
		var isRead bool
		for iter.Scan(&uid, &isRead) {
			if !isRead {
				userUnread[uuid.UUID(uid)]++
			}
		}
		if err := iter.Close(); err != nil {
			slog.Warn("reconcile: failed to scan bucket", "bucket", bucket, "error", err)
		}
	}

	// Write reconciled counts to Redis.
	updated := 0
	drifted := 0
	pipe := rdb.Pipeline()
	for userID, count := range userUnread {
		key := fmt.Sprintf("unread:%s", userID.String())

		// Check current Redis value to detect drift.
		current, err := rdb.Get(ctx, key).Int64()
		if err != nil && err != redis.Nil {
			continue
		}
		if current != count {
			drifted++
			slog.Debug("reconcile: counter drift detected",
				"user_id", userID, "redis", current, "scylla", count)
		}

		pipe.Set(ctx, key, count, 0)
		updated++

		// Flush the pipeline every 500 commands to avoid memory buildup.
		if updated%500 == 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				slog.Warn("reconcile: pipeline flush error", "error", err)
			}
			pipe = rdb.Pipeline()
		}
	}

	// Final flush.
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("reconcile: final pipeline flush error", "error", err)
	}

	slog.Info("unread counter reconciliation complete",
		"users_updated", updated,
		"drifted", drifted,
		"duration", time.Since(start),
	)
}
