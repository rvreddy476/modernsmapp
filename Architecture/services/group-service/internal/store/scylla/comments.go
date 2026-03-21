package scylla

import (
	"context"
	"log/slog"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// CommentStore provides high-throughput time-series comment storage in ScyllaDB.
// Primary reads go here; Postgres remains the source of truth for writes.
// The write path: Postgres INSERT → async replication to ScyllaDB via this store.
// The read path: ScyllaDB first (fast) → Postgres fallback (authoritative).
type CommentStore struct {
	session *gocql.Session
}

func NewCommentStore(session *gocql.Session) *CommentStore {
	return &CommentStore{session: session}
}

// Comment is the ScyllaDB representation of a group post comment.
type Comment struct {
	PostID    uuid.UUID  `json:"post_id"`
	Bucket    int        `json:"bucket"`
	TS        gocql.UUID `json:"ts"`
	CommentID uuid.UUID  `json:"comment_id"`
	UserID    string     `json:"user_id"`
	Body      string     `json:"body"`
	ParentID  *uuid.UUID `json:"parent_id,omitempty"`
	IsPinned  bool       `json:"is_pinned"`
	CreatedAt time.Time  `json:"created_at"`
}

// bucket returns YYYYMM int from a time.
func bucket(t time.Time) int {
	return t.Year()*100 + int(t.Month())
}

func prevBucket(b int) int {
	month := b % 100
	year := b / 100
	month--
	if month < 1 {
		month = 12
		year--
	}
	return year*100 + month
}

// EnsureSchema creates the ScyllaDB table if it doesn't exist.
func EnsureSchema(session *gocql.Session) error {
	return session.Query(`
		CREATE TABLE IF NOT EXISTS group_post_comments_by_post (
			post_id UUID,
			bucket INT,
			ts TIMEUUID,
			comment_id UUID,
			user_id TEXT,
			body TEXT,
			parent_id UUID,
			is_pinned BOOLEAN,
			created_at TIMESTAMP,
			PRIMARY KEY ((post_id, bucket), ts)
		) WITH CLUSTERING ORDER BY (ts DESC)
		AND default_time_to_live = 0
		AND gc_grace_seconds = 86400
	`).Exec()
}

// InsertComment writes a comment to ScyllaDB (called async after Postgres write).
func (s *CommentStore) InsertComment(ctx context.Context, c *Comment) error {
	b := bucket(c.CreatedAt)
	ts := gocql.UUIDFromTime(c.CreatedAt)

	var parentID *gocql.UUID
	if c.ParentID != nil {
		g := gocql.UUID(*c.ParentID)
		parentID = &g
	}

	return s.session.Query(`
		INSERT INTO group_post_comments_by_post
			(post_id, bucket, ts, comment_id, user_id, body, parent_id, is_pinned, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, gocql.UUID(c.PostID), b, ts, gocql.UUID(c.CommentID), c.UserID, c.Body,
		parentID, c.IsPinned, c.CreatedAt).WithContext(ctx).Exec()
}

// DeleteComment removes a comment from ScyllaDB.
func (s *CommentStore) DeleteComment(ctx context.Context, postID uuid.UUID, commentBucket int, ts gocql.UUID) error {
	return s.session.Query(`
		DELETE FROM group_post_comments_by_post
		WHERE post_id = ? AND bucket = ? AND ts = ?
	`, gocql.UUID(postID), commentBucket, ts).WithContext(ctx).Exec()
}

// ListComments returns comments for a post, paginating across monthly buckets.
// Returns up to `limit` comments, scanning up to 3 months back.
func (s *CommentStore) ListComments(ctx context.Context, postID uuid.UUID, limit int) ([]Comment, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	now := time.Now().UTC()
	var results []Comment
	remaining := limit

	// Scan current month + 2 previous months
	for i := 0; i < 3 && remaining > 0; i++ {
		t := now.AddDate(0, -i, 0)
		b := bucket(t)

		comments, err := s.queryBucket(ctx, postID, b, remaining)
		if err != nil {
			slog.Warn("scylla comment bucket query failed", "post_id", postID, "bucket", b, "error", err)
			continue
		}
		results = append(results, comments...)
		remaining -= len(comments)
	}

	return results, nil
}

func (s *CommentStore) queryBucket(ctx context.Context, postID uuid.UUID, b, limit int) ([]Comment, error) {
	iter := s.session.Query(`
		SELECT post_id, bucket, ts, comment_id, user_id, body, parent_id, is_pinned, created_at
		FROM group_post_comments_by_post
		WHERE post_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, gocql.UUID(postID), b, limit).WithContext(ctx).Iter()

	var comments []Comment
	var c Comment
	var pid, cid gocql.UUID
	var parentID *gocql.UUID
	for iter.Scan(&pid, &c.Bucket, &c.TS, &cid, &c.UserID, &c.Body, &parentID, &c.IsPinned, &c.CreatedAt) {
		c.PostID = uuid.UUID(pid)
		c.CommentID = uuid.UUID(cid)
		if parentID != nil {
			p := uuid.UUID(*parentID)
			c.ParentID = &p
		} else {
			c.ParentID = nil
		}
		comments = append(comments, c)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return comments, nil
}
