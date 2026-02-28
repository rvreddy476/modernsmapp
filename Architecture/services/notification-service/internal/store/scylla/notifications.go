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

// GetNotifications
func (s *NotificationStore) GetNotifications(ctx context.Context, userID uuid.UUID, limit int) ([]Notification, error) {
	// Better approach for MVP: query current date's bucket
	currentBucket := time.Now().Year()*100 + int(time.Now().Month())

	iter := s.session.Query(`
		SELECT user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at
		FROM notifications_by_user
		WHERE user_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, gocql.UUID(userID), currentBucket, limit).Iter()

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
