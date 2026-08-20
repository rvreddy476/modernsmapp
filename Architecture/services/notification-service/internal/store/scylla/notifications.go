package scylla

import (
	"context"
	"crypto/sha256"
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

// CreateNotification inserts an inbox row.
//
// Idempotency (Module 1 fixes-v1, Codex P0-1): the clustering key is
// (user_id, bucket, ts). When n.TS is set, it is used verbatim, which
// makes the INSERT an upsert on that exact key — re-running it after a
// partial failure produces ONE row, not a duplicate. Callers that need
// at-least-once delivery with exactly-once effect must set n.TS via
// DeterministicTS.
//
// When n.TS is zero the legacy behavior applies: gocql.UUIDFromTime
// generates random clock-sequence/node bits, so every call yields a new
// clustering key (fine for one-shot notifications, unsafe for retries).
func (s *NotificationStore) CreateNotification(ctx context.Context, n *Notification) error {
	// bucket rule: YYYYMM
	bucket := n.CreatedAt.Year()*100 + int(n.CreatedAt.Month())

	ts := n.TS
	if ts == (gocql.UUID{}) {
		ts = gocql.UUIDFromTime(n.CreatedAt)
	}

	return s.session.Query(`
		INSERT INTO notifications_by_user (user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, gocql.UUID(n.UserID), bucket, ts, gocql.UUID(n.NotificationID), n.Type, gocql.UUID(n.ActorUserID), n.EntityType, gocql.UUID(n.EntityID), n.DeepLink, n.IsRead, n.CreatedAt).Exec()
}

// CreateNotificationIfNotExists inserts the inbox row ONLY when no row
// exists at that primary key, using a Paxos lightweight transaction.
// Returns applied=false when the row was already there.
//
// Module 1 fixes-v2 / Codex P0-1. A plain upsert with a deterministic key
// gives one ROW, but it rewrites every column on each retry — including
// `is_read`. A retry after the user had already read the notification
// silently marked it unread again. `IF NOT EXISTS` makes the retry a true
// no-op, so mutable read state is never clobbered, and `applied` tells the
// caller whether this attempt is the FIRST one — which is how realtime and
// push are kept from firing twice.
//
// Cost: an LWT is a Paxos round-trip (~4x a normal write). That is
// acceptable here because this path is asynchronous batch fan-out, not a
// user-facing request.
func (s *NotificationStore) CreateNotificationIfNotExists(ctx context.Context, n *Notification) (bool, error) {
	bucket := n.CreatedAt.Year()*100 + int(n.CreatedAt.Month())

	ts := n.TS
	if ts == (gocql.UUID{}) {
		ts = gocql.UUIDFromTime(n.CreatedAt)
	}

	applied, err := s.session.Query(`
		INSERT INTO notifications_by_user (user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		IF NOT EXISTS
	`, gocql.UUID(n.UserID), bucket, ts, gocql.UUID(n.NotificationID), n.Type,
		gocql.UUID(n.ActorUserID), n.EntityType, gocql.UUID(n.EntityID),
		n.DeepLink, n.IsRead, n.CreatedAt).
		WithContext(ctx).ScanCAS()
	if err != nil {
		return false, err
	}
	return applied, nil
}

// DeterministicTS derives a stable time-UUID clustering key from a
// caller-supplied identity string. The time bits still come from `at`
// (so ordering by ts remains chronological), but the clock-sequence and
// node bytes are a hash of the identity instead of random — so the same
// (identity, at) always maps to the same clustering key.
//
// This is what makes subscriber fan-out safe to retry: the identity is
// "<post_id>:<user_id>:<type>", so a replayed event or a resumed batch
// overwrites the same row rather than appending a second notification.
func DeterministicTS(at time.Time, identity string) gocql.UUID {
	u := gocql.UUIDFromTime(at) // correct time_low/mid/hi + version bits
	sum := sha256.Sum256([]byte(identity))
	copy(u[8:16], sum[:8])      // clock_seq (2) + node (6)
	u[8] = (u[8] & 0x3F) | 0x80 // restore the RFC-4122 variant marker
	return u
}

// DeterministicNotificationID derives the notification's own UUID from the
// same identity, so a retried insert reuses the id the client may already
// have seen.
func DeterministicNotificationID(identity string) uuid.UUID {
	return uuid.NewSHA1(notificationIDNamespace, []byte(identity))
}

// Stable namespace for DeterministicNotificationID. Do not change: it
// would re-key every previously delivered notification.
var notificationIDNamespace = uuid.MustParse("6f1a4b90-1f2d-4a3e-9c6b-0d5a6e7f8a90")

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

// GetNotificationsAfter returns rows newer than the (cursorBucket,
// cursorTS) cursor, in chronological order. Drives the SSE
// Last-Event-ID replay path: a client that reconnects after being
// offline sends the last id it saw, and the server replays everything
// the client missed up to `limit`.
//
// The walk goes forward in time: starts at cursorBucket with
// `ts > cursor`, then advances bucket-by-bucket up to the current
// bucket. Capped at `limit` to match the README §13 guidance
// (default 500 events per reconnect).
func (s *NotificationStore) GetNotificationsAfter(ctx context.Context, userID uuid.UUID, cursorBucket int, cursorTS gocql.UUID, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 500
	}
	current := time.Now().Year()*100 + int(time.Now().Month())
	if cursorBucket > current || cursorBucket == 0 {
		// Defensive: treat invalid cursor as "from current bucket
		// onward with no lower-bound" which still hits the live path
		// after replay finishes (just produces 0 replays).
		cursorBucket = current
	}

	var out []Notification
	bucket := cursorBucket
	remaining := limit
	for bucket <= current && remaining > 0 {
		var ts *gocql.UUID
		if bucket == cursorBucket {
			ts = &cursorTS // exclusive lower bound on the first bucket only
		}
		notifs, err := s.queryBucketAfter(ctx, userID, bucket, ts, remaining)
		if err != nil {
			return out, err
		}
		out = append(out, notifs...)
		remaining -= len(notifs)
		bucket = nextBucket(bucket)
	}
	return out, nil
}

// queryBucketAfter is the forward-walking sibling of queryBucket.
// Returns rows ascending by ts so the SSE handler can replay them in
// the order they happened.
func (s *NotificationStore) queryBucketAfter(ctx context.Context, userID uuid.UUID, bucket int, afterTS *gocql.UUID, limit int) ([]Notification, error) {
	var iter *gocql.Iter
	if afterTS != nil {
		iter = s.session.Query(`
			SELECT user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at
			FROM notifications_by_user
			WHERE user_id = ? AND bucket = ? AND ts > ?
			ORDER BY ts ASC
			LIMIT ?
		`, gocql.UUID(userID), bucket, *afterTS, limit).WithContext(ctx).Iter()
	} else {
		iter = s.session.Query(`
			SELECT user_id, bucket, ts, notification_id, type, actor_user_id, entity_type, entity_id, deep_link, is_read, created_at
			FROM notifications_by_user
			WHERE user_id = ? AND bucket = ?
			ORDER BY ts ASC
			LIMIT ?
		`, gocql.UUID(userID), bucket, limit).WithContext(ctx).Iter()
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

// nextBucket returns the YYYYMM bucket for the next month.
func nextBucket(bucket int) int {
	year := bucket / 100
	month := bucket % 100
	month++
	if month > 12 {
		month = 1
		year++
	}
	return year*100 + month
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
