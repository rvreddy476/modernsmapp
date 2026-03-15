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
	PostID      uuid.UUID
	AuthorID    uuid.UUID
	CreatedAt   time.Time
	ContentType string // "post", "reel", "video", or "" for legacy rows
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
func (s *TimelineStore) AddToHomeTimeline(ctx context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, contentType string) error {
	b := bucket(createdAt)

	return s.session.Query(`
		INSERT INTO home_timeline_by_user (user_id, bucket, ts, post_id, author_id, created_at, content_type)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, toGocql(userID), b, gocql.UUIDFromTime(createdAt), toGocql(postID), toGocql(authorID), createdAt, contentType).Exec()
}

// AddToAuthorTimeline (Pull source)
func (s *TimelineStore) AddToAuthorTimeline(ctx context.Context, authorID uuid.UUID, postID uuid.UUID, createdAt time.Time, contentType string) error {
	b := bucket(createdAt)

	return s.session.Query(`
		INSERT INTO author_timeline_by_author (author_id, bucket, ts, post_id, created_at, content_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`, toGocql(authorID), b, gocql.UUIDFromTime(createdAt), toGocql(postID), createdAt, contentType).Exec()
}

// GetHomeTimeline returns all timeline items for the current month bucket.
func (s *TimelineStore) GetHomeTimeline(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, author_id, created_at, content_type FROM home_timeline_by_user
		WHERE user_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(userID), b, limit).Iter()

	var items []FeedItem
	var pid, aid gocql.UUID
	var createdAt time.Time
	var contentType *string
	for iter.Scan(&pid, &aid, &createdAt, &contentType) {
		ct := "post"
		if contentType != nil && *contentType != "" {
			ct = *contentType
		}
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    uuid.UUID(aid),
			CreatedAt:   createdAt,
			ContentType: ct,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

// GetHomeTimelineByContentType returns timeline items filtered to a single
// content_type. Over-fetches and filters in Go since content_type is not a
// clustering key. The partition scan is bounded by (user_id, bucket).
func (s *TimelineStore) GetHomeTimelineByContentType(ctx context.Context, userID uuid.UUID, contentType string, limit int) ([]FeedItem, error) {
	fetchLimit := limit * 5
	if fetchLimit > 1000 {
		fetchLimit = 1000
	}

	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, author_id, created_at, content_type FROM home_timeline_by_user
		WHERE user_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(userID), b, fetchLimit).Iter()

	var items []FeedItem
	var pid, aid gocql.UUID
	var createdAt time.Time
	var ct *string
	for iter.Scan(&pid, &aid, &createdAt, &ct) {
		rowType := "post"
		if ct != nil && *ct != "" {
			rowType = *ct
		}
		if rowType != contentType {
			continue
		}
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    uuid.UUID(aid),
			CreatedAt:   createdAt,
			ContentType: rowType,
		})
		if len(items) >= limit {
			break
		}
	}
	_ = iter.Close()
	return items, nil
}

// GetHomeTimelineByContentTypes returns timeline items filtered to a set of
// content_types. Over-fetches and filters in Go since content_type is not a
// clustering key. The partition scan is bounded by (user_id, bucket).
func (s *TimelineStore) GetHomeTimelineByContentTypes(ctx context.Context, userID uuid.UUID, contentTypes []string, limit int) ([]FeedItem, error) {
	fetchLimit := limit * 5
	if fetchLimit > 1000 {
		fetchLimit = 1000
	}

	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, author_id, created_at, content_type FROM home_timeline_by_user
		WHERE user_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(userID), b, fetchLimit).Iter()

	// Build set for O(1) lookup
	typeSet := make(map[string]bool, len(contentTypes))
	for _, ct := range contentTypes {
		typeSet[ct] = true
	}

	var items []FeedItem
	var pid, aid gocql.UUID
	var createdAt time.Time
	var ct *string
	for iter.Scan(&pid, &aid, &createdAt, &ct) {
		rowType := "post"
		if ct != nil && *ct != "" {
			rowType = *ct
		}
		if !typeSet[rowType] {
			continue
		}
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    uuid.UUID(aid),
			CreatedAt:   createdAt,
			ContentType: rowType,
		})
		if len(items) >= limit {
			break
		}
	}
	_ = iter.Close()
	return items, nil
}

// GetAuthorTimeline (for Pull merge)
func (s *TimelineStore) GetAuthorTimeline(ctx context.Context, authorID uuid.UUID, limit int) ([]FeedItem, error) {
	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, created_at, content_type FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(authorID), b, limit).Iter()

	var items []FeedItem
	var pid gocql.UUID
	var createdAt time.Time
	var contentType *string
	for iter.Scan(&pid, &createdAt, &contentType) {
		ct := "post"
		if contentType != nil && *contentType != "" {
			ct = *contentType
		}
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    authorID,
			CreatedAt:   createdAt,
			ContentType: ct,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteTimelineEntriesByAuthor removes all author-timeline entries for the given
// author (GDPR right-to-erasure). It deletes across a rolling window of the
// current and previous two months from author_timeline_by_author.
// Note: home_timeline entries authored by this user will be naturally pruned
// as they expire or as the feed service skips soft-deleted post references.
func (s *TimelineStore) DeleteTimelineEntriesByAuthor(ctx context.Context, authorID uuid.UUID) error {
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		t := now.AddDate(0, -i, 0)
		b := bucket(t)
		if err := s.session.Query(`
			DELETE FROM author_timeline_by_author
			WHERE author_id = ? AND bucket = ?
		`, toGocql(authorID), b).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAuthorTimelineEntry removes a single post entry from the author timeline
// for a given bucket. Used when an upload is deleted to clean up the author's timeline.
func (s *TimelineStore) DeleteAuthorTimelineEntry(ctx context.Context, authorID, postID uuid.UUID, bucket int) error {
	// Since post_id is not a clustering key, we need to find the ts for this post_id first
	// and then delete by (author_id, bucket, ts). For simplicity, we scan the bucket to find it.
	iter := s.session.Query(`
		SELECT ts FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
		ORDER BY ts DESC
	`, toGocql(authorID), bucket).Iter()

	var ts gocql.UUID
	found := false
	// Also scan post_id to match
	var pid gocql.UUID
	iter2 := s.session.Query(`
		SELECT ts, post_id FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
	`, toGocql(authorID), bucket).Iter()

	for iter2.Scan(&ts, &pid) {
		if uuid.UUID(pid) == postID {
			found = true
			break
		}
	}
	_ = iter.Close()
	_ = iter2.Close()

	if !found {
		return nil
	}

	return s.session.Query(`
		DELETE FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ? AND ts = ?
	`, toGocql(authorID), bucket, ts).WithContext(ctx).Exec()
}

// RecordInteraction stores a user-post interaction in ScyllaDB as the
// durable source of truth for the already-interacted ranking penalty.
func (s *TimelineStore) RecordInteraction(ctx context.Context, userID, postID uuid.UUID) error {
	return s.session.Query(`
		INSERT INTO user_post_interactions (user_id, post_id) VALUES (?, ?)`,
		toGocql(userID), toGocql(postID),
	).Exec()
}

// CheckInteractions returns the subset of postIDs that the user has
// previously interacted with. Used as a ScyllaDB fallback when Redis
// data has expired.
func (s *TimelineStore) CheckInteractions(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID) (map[string]bool, error) {
	result := make(map[string]bool, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}

	// Build IN clause with gocql UUIDs
	gocqlIDs := make([]interface{}, len(postIDs))
	for i, id := range postIDs {
		gocqlIDs[i] = toGocql(id)
	}

	iter := s.session.Query(`
		SELECT post_id FROM user_post_interactions
		WHERE user_id = ? AND post_id IN ?`,
		toGocql(userID), gocqlIDs,
	).Iter()

	var pid gocql.UUID
	for iter.Scan(&pid) {
		result[uuid.UUID(pid).String()] = true
	}
	if err := iter.Close(); err != nil {
		return result, err
	}
	return result, nil
}
