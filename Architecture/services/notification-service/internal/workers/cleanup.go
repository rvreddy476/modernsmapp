package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// StartCleanupWorker runs nightly notification cleanup against ScyllaDB.
// It removes old notifications to keep the inbox manageable and storage bounded.
func StartCleanupWorker(ctx context.Context, session *gocql.Session) {
	slog.Info("notification cleanup worker started")
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run once on startup
	runCleanup(ctx, session)

	for {
		select {
		case <-ctx.Done():
			slog.Info("cleanup worker stopped")
			return
		case <-ticker.C:
			runCleanup(ctx, session)
		}
	}
}

func runCleanup(ctx context.Context, session *gocql.Session) {
	slog.Info("running notification cleanup")
	start := time.Now()

	// 1. Delete all notifications older than 90 days.
	// Notifications are bucketed by YYYYMM so we delete entire old buckets.
	deleted := deleteOldBuckets(session, 90)
	slog.Info("cleanup: deleted old notification buckets", "buckets_processed", deleted)

	// 2. Delete read notifications older than 30 days (within recent buckets).
	// We scan the last 3 months of buckets and remove read notifications older than 30 days.
	pruned := pruneReadNotifications(session, 30)
	slog.Info("cleanup: pruned old read notifications", "count", pruned)

	slog.Info("notification cleanup complete", "duration", time.Since(start))
}

// deleteOldBuckets removes entire month-buckets older than the given number of days.
// Returns the number of buckets processed.
func deleteOldBuckets(session *gocql.Session, maxAgeDays int) int {
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	cutoffBucket := cutoff.Year()*100 + int(cutoff.Month())

	// We go back up to 12 months beyond the cutoff to catch any stale data.
	processed := 0
	for i := 1; i <= 12; i++ {
		bucket := decrementBucket(cutoffBucket, i)

		// Find all distinct user_ids in this bucket and delete their partition.
		iter := session.Query(`
			SELECT DISTINCT user_id FROM notifications_by_user
			WHERE bucket = ? ALLOW FILTERING
		`, bucket).Iter()

		var uid gocql.UUID
		users := 0
		for iter.Scan(&uid) {
			err := session.Query(`
				DELETE FROM notifications_by_user
				WHERE user_id = ? AND bucket = ?
			`, uid, bucket).Exec()
			if err != nil {
				slog.Warn("cleanup: failed to delete bucket partition",
					"bucket", bucket, "user_id", uid, "error", err)
			}
			users++
		}
		if err := iter.Close(); err != nil {
			slog.Warn("cleanup: failed to scan bucket", "bucket", bucket, "error", err)
		}
		if users > 0 {
			slog.Info("cleanup: cleared old bucket", "bucket", bucket, "users", users)
			processed++
		}
	}

	return processed
}

// pruneReadNotifications scans recent buckets (current + 2 previous months) and
// deletes read notifications older than the given number of days.
func pruneReadNotifications(session *gocql.Session, maxAgeDays int) int {
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	now := time.Now()
	count := 0

	// Scan the last 3 months of buckets.
	for i := 0; i < 3; i++ {
		t := now.AddDate(0, -i, 0)
		bucket := t.Year()*100 + int(t.Month())

		iter := session.Query(`
			SELECT user_id, bucket, ts, is_read, created_at
			FROM notifications_by_user
			WHERE bucket = ? ALLOW FILTERING
		`, bucket).Iter()

		var uid gocql.UUID
		var b int
		var ts gocql.UUID
		var isRead bool
		var createdAt time.Time

		for iter.Scan(&uid, &b, &ts, &isRead, &createdAt) {
			if isRead && createdAt.Before(cutoff) {
				err := session.Query(`
					DELETE FROM notifications_by_user
					WHERE user_id = ? AND bucket = ? AND ts = ?
				`, uid, b, ts).Exec()
				if err != nil {
					slog.Warn("cleanup: failed to delete read notification",
						"user_id", uuid.UUID(uid), "error", err)
				} else {
					count++
				}
			}
		}
		if err := iter.Close(); err != nil {
			slog.Warn("cleanup: failed to scan bucket for read pruning",
				"bucket", bucket, "error", err)
		}
	}

	return count
}

// decrementBucket returns the YYYYMM bucket that is `n` months before the given bucket.
func decrementBucket(bucket, n int) int {
	year := bucket / 100
	month := bucket % 100
	for i := 0; i < n; i++ {
		month--
		if month < 1 {
			month = 12
			year--
		}
	}
	return year*100 + month
}
