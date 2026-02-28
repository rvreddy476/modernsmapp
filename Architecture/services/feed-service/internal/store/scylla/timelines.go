package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type TimelineStore struct {
	session *gocql.Session
}

func New(session *gocql.Session) *TimelineStore {
	return &TimelineStore{session: session}
}

// FeedItem represents a post in a timeline
type FeedItem struct {
	PostID    uuid.UUID
	AuthorID  uuid.UUID
	CreatedAt time.Time
}

// bucket returns YYYYMM int from a time
func bucket(t time.Time) int {
	return t.Year()*100 + int(t.Month())
}

// currentBucket returns the current month bucket
func currentBucket() int {
	return bucket(time.Now().UTC())
}

// toGocql converts google/uuid to gocql UUID
func toGocql(id uuid.UUID) gocql.UUID {
	return gocql.UUID(id)
}

// AddToHomeTimeline (Push)
func (s *TimelineStore) AddToHomeTimeline(ctx context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time) error {
	b := bucket(createdAt)

	return s.session.Query(`
		INSERT INTO home_timeline_by_user (user_id, bucket, ts, post_id, author_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, toGocql(userID), b, gocql.UUIDFromTime(createdAt), toGocql(postID), toGocql(authorID), createdAt).Exec()
}

// AddToAuthorTimeline (Pull source)
func (s *TimelineStore) AddToAuthorTimeline(ctx context.Context, authorID uuid.UUID, postID uuid.UUID, createdAt time.Time) error {
	b := bucket(createdAt)

	return s.session.Query(`
		INSERT INTO author_timeline_by_author (author_id, bucket, ts, post_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, toGocql(authorID), b, gocql.UUIDFromTime(createdAt), toGocql(postID), createdAt).Exec()
}

// GetHomeTimeline
func (s *TimelineStore) GetHomeTimeline(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, author_id, created_at FROM home_timeline_by_user
		WHERE user_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(userID), b, limit).Iter()

	var items []FeedItem
	var pid, aid gocql.UUID
	var createdAt time.Time
	for iter.Scan(&pid, &aid, &createdAt) {
		items = append(items, FeedItem{
			PostID:    uuid.UUID(pid),
			AuthorID:  uuid.UUID(aid),
			CreatedAt: createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

// GetAuthorTimeline (for Pull merge)
func (s *TimelineStore) GetAuthorTimeline(ctx context.Context, authorID uuid.UUID, limit int) ([]FeedItem, error) {
	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, created_at FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(authorID), b, limit).Iter()

	var items []FeedItem
	var pid gocql.UUID
	var createdAt time.Time
	for iter.Scan(&pid, &createdAt) {
		items = append(items, FeedItem{
			PostID:    uuid.UUID(pid),
			AuthorID:  authorID,
			CreatedAt: createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}
