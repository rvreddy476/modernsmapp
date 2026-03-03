package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type NotificationStore struct {
	session *gocql.Session
}

func New(session *gocql.Session) *NotificationStore {
	return &NotificationStore{session: session}
}

type Notification struct {
	UserID         uuid.UUID  `json:"user_id"`
	Bucket         int        `json:"bucket"`
	TS             gocql.UUID `json:"ts"`
	NotificationID uuid.UUID  `json:"notification_id"`
	Type           string     `json:"type"`
	ActorUserID    uuid.UUID  `json:"actor_user_id"`
	EntityType     string     `json:"entity_type"`
	EntityID       uuid.UUID  `json:"entity_id"`
	DeepLink       string     `json:"deep_link,omitempty"`
	IsRead         bool       `json:"is_read"`
	CreatedAt      time.Time  `json:"created_at"`
}

// CQL migration (run once):
// ALTER TABLE notifications_by_user ADD deep_link text;

// CreateNotification
func (s *NotificationStore) CreateNotification(ctx context.Context, n *Notification) error {
	// bucket rule: YYYYMM
	bucket := n.CreatedAt.Year()*100 + int(n.CreatedAt.Month())

	return s.session.Query(`
		INSERT INTO notifications_by_user (user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, gocql.UUID(n.UserID), bucket, gocql.UUIDFromTime(n.CreatedAt), gocql.UUID(n.NotificationID), n.Type, gocql.UUID(n.ActorUserID), n.EntityType, gocql.UUID(n.EntityID), n.DeepLink, n.IsRead, n.CreatedAt).Exec()
}

// GetNotifications returns the latest notifications (no cursor).
func (s *NotificationStore) GetNotifications(ctx context.Context, userID uuid.UUID, limit int) ([]Notification, error) {
	currentBucket := time.Now().Year()*100 + int(time.Now().Month())
	return s.queryBucket(userID, currentBucket, nil, limit)
}

// GetNotificationsWithCursor returns notifications using cursor-based pagination.
// If cursorBucket and cursorTS are provided, results start after that position.
// It automatically crosses bucket boundaries when the current bucket is exhausted.
func (s *NotificationStore) GetNotificationsWithCursor(ctx context.Context, userID uuid.UUID, cursorBucket int, cursorTS *gocql.UUID, limit int) ([]Notification, error) {
	startBucket := cursorBucket
	if startBucket == 0 {
		startBucket = time.Now().Year()*100 + int(time.Now().Month())
	}

	var results []Notification
	bucket := startBucket
	remaining := limit

	// Try up to 3 buckets back (current + 2 previous months)
	for i := 0; i < 3 && remaining > 0; i++ {
		var ts *gocql.UUID
		if i == 0 {
			ts = cursorTS // only apply cursor to the first bucket
		}

		notifs, err := s.queryBucket(userID, bucket, ts, remaining)
		if err != nil {
			return results, err
		}
		results = append(results, notifs...)
		remaining -= len(notifs)

		// Move to previous bucket
		bucket = prevBucket(bucket)
	}

	return results, nil
}

// queryBucket queries a single bucket, optionally starting before a given TimeUUID.
func (s *NotificationStore) queryBucket(userID uuid.UUID, bucket int, beforeTS *gocql.UUID, limit int) ([]Notification, error) {
	var iter *gocql.Iter
	if beforeTS != nil {
		iter = s.session.Query(`
			SELECT user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at
			FROM notifications_by_user
			WHERE user_id = ? AND bucket = ? AND ts < ?
			ORDER BY ts DESC
			LIMIT ?
		`, gocql.UUID(userID), bucket, *beforeTS, limit).Iter()
	} else {
		iter = s.session.Query(`
			SELECT user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at
			FROM notifications_by_user
			WHERE user_id = ? AND bucket = ?
			ORDER BY ts DESC
			LIMIT ?
		`, gocql.UUID(userID), bucket, limit).Iter()
	}

	var notifications []Notification
	var n Notification
	var uid, nid, aid, eid gocql.UUID
	for iter.Scan(&uid, &n.Bucket, &n.TS, &nid, &n.Type, &aid, &n.EntityType, &eid, &n.DeepLink, &n.IsRead, &n.CreatedAt) {
		n.UserID = uuid.UUID(uid)
		n.NotificationID = uuid.UUID(nid)
		n.ActorUserID = uuid.UUID(aid)
		n.EntityID = uuid.UUID(eid)
		notifications = append(notifications, n)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return notifications, nil
}

// prevBucket returns the YYYYMM bucket for the previous month.
func prevBucket(bucket int) int {
	year := bucket / 100
	month := bucket % 100
	month--
	if month < 1 {
		month = 12
		year--
	}
	return year*100 + month
}

// DeleteNotificationsForUser removes all notification rows for a given user_id
// across a rolling window of the current and previous two months.
func (s *NotificationStore) DeleteNotificationsForUser(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	for i := 0; i < 3; i++ {
		t := now.AddDate(0, -i, 0)
		b := t.Year()*100 + int(t.Month())
		if err := s.session.Query(`
			DELETE FROM notifications_by_user
			WHERE user_id = ? AND bucket = ?
		`, gocql.UUID(userID), b).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

// MarkRead
func (s *NotificationStore) MarkRead(ctx context.Context, userID uuid.UUID, bucket int, ts gocql.UUID) error {
	return s.session.Query(`
		UPDATE notifications_by_user SET is_read = true
		WHERE user_id = ? AND bucket = ? AND ts = ?
	`, gocql.UUID(userID), bucket, ts).Exec()
}

// DeleteNotification removes a notification row.
func (s *NotificationStore) DeleteNotification(ctx context.Context, userID uuid.UUID, bucket int, ts gocql.UUID) error {
	return s.session.Query(`
		DELETE FROM notifications_by_user
		WHERE user_id = ? AND bucket = ? AND ts = ?
	`, gocql.UUID(userID), bucket, ts).Exec()
}
