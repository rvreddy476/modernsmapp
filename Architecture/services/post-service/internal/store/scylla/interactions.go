package scylla

import (
	"context"
	"strconv"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Comment represents a comment returned from ScyllaDB.
type Comment struct {
	ID        uuid.UUID `json:"id"`
	AuthorID  uuid.UUID `json:"author_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type InteractionStore struct {
	session *gocql.Session
}

func New(session *gocql.Session) *InteractionStore {
	return &InteractionStore{session: session}
}

// React (idempotent upsert)
func (s *InteractionStore) React(ctx context.Context, postID, userID uuid.UUID, reaction string) error {
	pid, uid := postID.String(), userID.String()

	// Read-before-write for counter correctness
	var existing string
	if err := s.session.Query(`SELECT reaction FROM reactions_by_post WHERE post_id = ? AND user_id = ?`, pid, uid).Scan(&existing); err != nil && err != gocql.ErrNotFound {
		return err
	}

	if existing != "" {
		return nil // Already reacted
	}

	if err := s.session.Query(`
		INSERT INTO reactions_by_post (post_id, user_id, reaction, created_at)
		VALUES (?, ?, ?, toTimestamp(now()))
	`, pid, uid, reaction).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE post_counters SET like_count = like_count + 1 WHERE post_id = ?`, pid).Exec()
}

// Unreact removes a reaction.
func (s *InteractionStore) Unreact(ctx context.Context, postID, userID uuid.UUID) error {
	pid, uid := postID.String(), userID.String()

	var existing string
	if err := s.session.Query(`SELECT reaction FROM reactions_by_post WHERE post_id = ? AND user_id = ?`, pid, uid).Scan(&existing); err != nil {
		if err == gocql.ErrNotFound {
			return nil
		}
		return err
	}

	if err := s.session.Query(`DELETE FROM reactions_by_post WHERE post_id = ? AND user_id = ?`, pid, uid).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE post_counters SET like_count = like_count - 1 WHERE post_id = ?`, pid).Exec()
}

// GetReaction returns the viewer's reaction type for a post, or empty string if none.
func (s *InteractionStore) GetReaction(ctx context.Context, postID, userID uuid.UUID) (string, error) {
	var reaction string
	if err := s.session.Query(`SELECT reaction FROM reactions_by_post WHERE post_id = ? AND user_id = ?`, postID.String(), userID.String()).Scan(&reaction); err != nil {
		if err == gocql.ErrNotFound {
			return "", nil
		}
		return "", err
	}
	return reaction, nil
}

// currentBucket returns the current YYYYMM bucket as an int.
func currentBucket() int {
	b, _ := strconv.Atoi(time.Now().UTC().Format("200601"))
	return b
}

// AddComment inserts a comment and increments the counter.
func (s *InteractionStore) AddComment(ctx context.Context, postID uuid.UUID, userID uuid.UUID, text string) (uuid.UUID, error) {
	pid := postID.String()
	bucket := currentBucket()
	commentID := uuid.New()

	if err := s.session.Query(`
		INSERT INTO comments_by_post (post_id, bucket, ts, comment_id, author_id, text, is_deleted)
		VALUES (?, ?, now(), ?, ?, ?, false)
	`, pid, bucket, commentID.String(), userID.String(), text).Exec(); err != nil {
		return uuid.Nil, err
	}

	if err := s.session.Query(`UPDATE post_counters SET comment_count = comment_count + 1 WHERE post_id = ?`, pid).Exec(); err != nil {
		return uuid.Nil, err
	}

	return commentID, nil
}

// ListComments returns the latest comments for a post from the current bucket.
func (s *InteractionStore) ListComments(ctx context.Context, postID uuid.UUID, limit int) ([]Comment, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	bucket := currentBucket()
	iter := s.session.Query(`
		SELECT comment_id, author_id, text, toTimestamp(ts) FROM comments_by_post
		WHERE post_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, postID.String(), bucket, limit).Iter()

	var comments []Comment
	var idStr, authorStr string
	var text string
	var createdAt time.Time
	for iter.Scan(&idStr, &authorStr, &text, &createdAt) {
		cID, _ := uuid.Parse(idStr)
		aID, _ := uuid.Parse(authorStr)
		comments = append(comments, Comment{ID: cID, AuthorID: aID, Text: text, CreatedAt: createdAt})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	return comments, nil
}

type Counts struct {
	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
}

func (s *InteractionStore) GetCounts(ctx context.Context, postID uuid.UUID) (*Counts, error) {
	var c Counts
	if err := s.session.Query(`SELECT like_count, comment_count FROM post_counters WHERE post_id = ?`, postID.String()).Scan(&c.Likes, &c.Comments); err != nil {
		if err == gocql.ErrNotFound {
			return &Counts{0, 0}, nil
		}
		return nil, err
	}
	return &c, nil
}
