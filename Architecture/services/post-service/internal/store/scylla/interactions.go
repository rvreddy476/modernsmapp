package scylla

import (
	"context"
	"fmt"
	"strconv"
	"sync"
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

// React records the viewer's reaction and keeps post_counters.like_count in
// step with the reactions_by_post rows. It reports whether a NEW reaction
// was recorded (false: the viewer had already reacted; a different type
// rewrites the row and leaves the count alone).
//
// Both the row write and the decision to count are one lightweight
// transaction (INSERT … IF NOT EXISTS). This used to read the row, then
// write it, then increment: two concurrent taps both read "no reaction"
// and both incremented, so the visible like count drifted above the number
// of people who liked the post, and never came back down. Every write to
// reactions_by_post goes through this file and is conditional, as Scylla
// requires for rows LWTs touch.
func (s *InteractionStore) React(ctx context.Context, postID, userID uuid.UUID, reaction string) (bool, error) {
	pid, uid := postID.String(), userID.String()

	inserted, err := s.session.Query(`
		INSERT INTO reactions_by_post (post_id, user_id, reaction, created_at)
		VALUES (?, ?, ?, toTimestamp(now()))
		IF NOT EXISTS
	`, pid, uid, reaction).WithContext(ctx).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	if !inserted {
		// Already reacted: keep the viewer's current type on the row (the
		// feed reads it back as viewer_reaction). The count does not move.
		if _, err := s.session.Query(`
			UPDATE reactions_by_post SET reaction = ?
			WHERE post_id = ? AND user_id = ?
			IF EXISTS
		`, reaction, pid, uid).WithContext(ctx).MapScanCAS(map[string]interface{}{}); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := s.session.Query(`UPDATE post_counters SET like_count = like_count + 1 WHERE post_id = ?`, pid).WithContext(ctx).Exec(); err != nil {
		return true, err
	}
	return true, nil
}

// Unreact removes the viewer's reaction and reports whether there was one.
// The decrement happens only when this call's conditional delete removed
// the row, so two concurrent un-taps cannot both decrement.
func (s *InteractionStore) Unreact(ctx context.Context, postID, userID uuid.UUID) (bool, error) {
	pid, uid := postID.String(), userID.String()

	deleted, err := s.session.Query(`
		DELETE FROM reactions_by_post WHERE post_id = ? AND user_id = ?
		IF EXISTS
	`, pid, uid).WithContext(ctx).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	if !deleted {
		return false, nil
	}
	if err := s.session.Query(`UPDATE post_counters SET like_count = like_count - 1 WHERE post_id = ?`, pid).WithContext(ctx).Exec(); err != nil {
		return true, err
	}
	return true, nil
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

const hydrationReadConcurrency = 16

// BatchGetReactions reads independent post partitions with bounded
// concurrency. Scylla has no efficient cross-partition join for this schema;
// bounding the fan-out avoids both serial N-round-trip latency and an
// unbounded goroutine/query burst.
func (s *InteractionStore) BatchGetReactions(ctx context.Context, postIDs []uuid.UUID, userID uuid.UUID) (map[uuid.UUID]string, error) {
	result := make(map[uuid.UUID]string, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, hydrationReadConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, postID := range postIDs {
		postID := postID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			reaction, err := s.GetReaction(ctx, postID, userID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("post %s: %w", postID, err)
					cancel()
				}
				return
			}
			if reaction != "" {
				result[postID] = reaction
			}
		}()
	}
	wg.Wait()
	return result, firstErr
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
	// Shares and Saves (2026-10-18) are post_engagement_counts.share_count
	// and bookmark_count - the sharded share / bookmark counters as last
	// flushed - overlaid by the service next to the comment figure. Never
	// omitempty: 0 is a count, not a missing field.
	Shares int64 `json:"shares"`
	Saves  int64 `json:"saves"`
}

func (s *InteractionStore) GetCounts(ctx context.Context, postID uuid.UUID) (*Counts, error) {
	var c Counts
	if err := s.session.Query(`SELECT like_count, comment_count FROM post_counters WHERE post_id = ?`, postID.String()).Scan(&c.Likes, &c.Comments); err != nil {
		if err == gocql.ErrNotFound {
			return &Counts{}, nil
		}
		return nil, err
	}
	return &c, nil
}

// BatchGetCounts is the counter companion to BatchGetReactions.
func (s *InteractionStore) BatchGetCounts(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*Counts, error) {
	result := make(map[uuid.UUID]*Counts, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, hydrationReadConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, postID := range postIDs {
		postID := postID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			counts, err := s.GetCounts(ctx, postID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("post %s: %w", postID, err)
					cancel()
				}
				return
			}
			result[postID] = counts
		}()
	}
	wg.Wait()
	return result, firstErr
}
