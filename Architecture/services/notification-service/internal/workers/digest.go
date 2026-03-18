package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StartDigestWorker checks every hour whether daily or weekly email digests
// should be generated. Daily digests fire at 8 AM UTC, weekly on Mondays at 8 AM.
func StartDigestWorker(ctx context.Context, db *pgxpool.Pool, pgStore *postgres.Store, scyllaSession *gocql.Session) {
	slog.Info("email digest worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("digest worker stopped")
			return
		case <-ticker.C:
			processDigests(ctx, db, pgStore, scyllaSession)
		}
	}
}

func processDigests(ctx context.Context, db *pgxpool.Pool, pgStore *postgres.Store, scyllaSession *gocql.Session) {
	now := time.Now().UTC()
	hour := now.Hour()
	weekday := now.Weekday()

	// Daily digests: send at 8 AM UTC.
	if hour == 8 {
		processDailyDigests(ctx, db, pgStore, scyllaSession)
	}

	// Weekly digests: send on Monday at 8 AM UTC.
	if hour == 8 && weekday == time.Monday {
		processWeeklyDigests(ctx, db, pgStore, scyllaSession)
	}
}

func processDailyDigests(ctx context.Context, db *pgxpool.Pool, pgStore *postgres.Store, scyllaSession *gocql.Session) {
	rows, err := db.Query(ctx, `
		SELECT user_id FROM notification_preferences_v2
		WHERE email_enabled = TRUE AND email_digest = 'daily'
		LIMIT 1000
	`)
	if err != nil {
		slog.Error("digest: failed to query daily digest users", "error", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		if err := generateDigest(ctx, pgStore, scyllaSession, userID, "daily"); err != nil {
			slog.Warn("digest: failed for user", "user", userID, "error", err)
		} else {
			count++
		}
	}
	if count > 0 {
		slog.Info("daily digests processed", "count", count)
	}
}

func processWeeklyDigests(ctx context.Context, db *pgxpool.Pool, pgStore *postgres.Store, scyllaSession *gocql.Session) {
	rows, err := db.Query(ctx, `
		SELECT user_id FROM notification_preferences_v2
		WHERE email_enabled = TRUE AND email_digest = 'weekly'
		LIMIT 5000
	`)
	if err != nil {
		slog.Error("digest: failed to query weekly digest users", "error", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		if err := generateDigest(ctx, pgStore, scyllaSession, userID, "weekly"); err != nil {
			slog.Warn("digest: failed for user", "user", userID, "error", err)
		} else {
			count++
		}
	}
	if count > 0 {
		slog.Info("weekly digests processed", "count", count)
	}
}

// generateDigest computes an unread notification summary from ScyllaDB and
// persists a digest record in Postgres. The actual email send is a TODO
// (requires an email-service integration).
func generateDigest(ctx context.Context, pgStore *postgres.Store, scyllaSession *gocql.Session, userID, period string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return err
	}

	var since time.Duration
	if period == "daily" {
		since = 24 * time.Hour
	} else {
		since = 7 * 24 * time.Hour
	}

	cutoff := time.Now().Add(-since)

	// Count unread notifications by type from ScyllaDB.
	// Scan the relevant buckets.
	typeCounts := make(map[string]int)
	totalUnread := 0

	now := time.Now()
	for i := 0; i < 3; i++ {
		t := now.AddDate(0, -i, 0)
		bucket := t.Year()*100 + int(t.Month())

		iter := scyllaSession.Query(`
			SELECT type, is_read, created_at
			FROM notifications_by_user
			WHERE user_id = ? AND bucket = ?
		`, gocql.UUID(uid), bucket).Iter()

		var notifType string
		var isRead bool
		var createdAt time.Time
		for iter.Scan(&notifType, &isRead, &createdAt) {
			if !isRead && createdAt.After(cutoff) {
				typeCounts[notifType]++
				totalUnread++
			}
		}
		if err := iter.Close(); err != nil {
			slog.Warn("digest: failed to scan bucket", "bucket", bucket, "error", err)
		}
	}

	if totalUnread == 0 {
		return nil // nothing to send
	}

	// Build digest content.
	content := map[string]interface{}{
		"total_unread": totalUnread,
		"by_type":      typeCounts,
		"period":       period,
		"generated_at": time.Now().UTC(),
	}
	contentJSON, _ := json.Marshal(content)

	// Compute the period start for deduplication.
	var periodStart time.Time
	if period == "daily" {
		periodStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	} else {
		// Monday of this week.
		daysSinceMonday := (int(now.Weekday()) + 6) % 7
		monday := now.AddDate(0, 0, -daysSinceMonday)
		periodStart = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
	}

	// Persist to notification_digests table.
	digest := &postgres.NotificationDigest{
		ID:          uuid.New(),
		UserID:      uid,
		PeriodType:  period,
		PeriodStart: periodStart,
		Content:     contentJSON,
	}
	if err := pgStore.CreateDigest(ctx, digest); err != nil {
		return err
	}

	// TODO: Send the actual email via an email-service call.
	// For now we persist the digest so it can be queried via the API.
	slog.Info("digest: generated", "user", userID, "period", period, "unread", totalUnread)
	return nil
}
