package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// purgeLookbackMonths bounds the notifications_by_user partition scan on
// purge. Buckets are YYYYMM ints; the inbox is capped well below this by
// the cleanup worker.
const purgeLookbackMonths = 36

// DeleteAllNotificationsForUser deletes every notifications_by_user
// partition for the user over the last purgeLookbackMonths. Each DELETE is
// a partition-level tombstone, so a redelivery is a no-op. Account control
// purge path; DeleteNotificationsForUser (3 buckets) stays for the legacy
// user.deletion_requested handler.
func (s *NotificationStore) DeleteAllNotificationsForUser(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()
	for i := 0; i < purgeLookbackMonths; i++ {
		t := now.AddDate(0, -i, 0)
		b := t.Year()*100 + int(t.Month())
		if err := s.session.Query(`
			DELETE FROM notifications_by_user WHERE user_id = ? AND bucket = ?`,
			gocql.UUID(userID), b).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}
