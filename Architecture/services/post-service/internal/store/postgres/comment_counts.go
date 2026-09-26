package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

/*
	comment_count — ONE definition, ONE store, ONE writer.

	  comment_count(post) = COUNT(*) FROM comments
	                        WHERE post_id = post AND is_deleted = FALSE
	                          AND moderation_status = 'visible'

	Replies are included (the thread renders them inline). Held, hidden and
	removed comments are not (ListComments does not show them).

	Stored in post_engagement_counts.comment_count, and only there. The
	request path moves it by ±1 on each transition into or out of the
	predicate; the recount below is the hourly (and boot-time) safety net.
	Before this the count lived in three places that disagreed: Scylla
	post_counters.comment_count (what every read used — never written by any
	live route, so it read 0), Redis post:eng (never seeded), and this column,
	which two writers each bumped for one comment while replies were never
	counted at all.
*/

const visibleCommentPredicate = `is_deleted = FALSE AND moderation_status = 'visible'`

// GetCommentCounts reads the authoritative count for a page of posts. A
// post with no row has zero comments.
func (s *Store) GetCommentCounts(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT post_id, comment_count FROM post_engagement_counts WHERE post_id = ANY($1)`, postIDs)
	if err != nil {
		return nil, fmt.Errorf("read comment counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// GetCommentCount reads one post's authoritative count.
func (s *Store) GetCommentCount(ctx context.Context, postID uuid.UUID) (int64, error) {
	m, err := s.GetCommentCounts(ctx, []uuid.UUID{postID})
	if err != nil {
		return 0, err
	}
	return m[postID], nil
}

// AdjustCommentCount moves the count by delta, never below zero. Callers
// call it exactly once per transition into (+1) or out of (-1) the visible
// set: create, reply, delete, and moderation status changes.
func (s *Store) AdjustCommentCount(ctx context.Context, postID uuid.UUID, delta int64) error {
	return s.IncrementEngagementCount(ctx, postID, "comment_count", delta)
}

// CountVisibleComments computes the definition directly; tests compare the
// stored count against it, and the recount uses the same predicate.
func (s *Store) CountVisibleComments(ctx context.Context, postID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM comments WHERE post_id = $1 AND `+visibleCommentPredicate, postID).Scan(&n)
	return n, err
}

// RecountCommentCounts rewrites every stored count that disagrees with the
// definition, including posts whose count must return to zero, and returns
// how many rows it corrected.
func (s *Store) RecountCommentCounts(ctx context.Context) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	up, err := tx.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id, comment_count)
		SELECT post_id, COUNT(*) FROM comments
		WHERE `+visibleCommentPredicate+`
		GROUP BY post_id
		ON CONFLICT (post_id) DO UPDATE
		SET comment_count = EXCLUDED.comment_count, updated_at = NOW()
		WHERE post_engagement_counts.comment_count <> EXCLUDED.comment_count`)
	if err != nil {
		return 0, fmt.Errorf("recount comment counts: %w", err)
	}
	down, err := tx.Exec(ctx, `
		UPDATE post_engagement_counts ec
		SET comment_count = 0, updated_at = NOW()
		WHERE ec.comment_count <> 0
		  AND NOT EXISTS (
		      SELECT 1 FROM comments c
		      WHERE c.post_id = ec.post_id AND `+visibleCommentPredicate+`)`)
	if err != nil {
		return 0, fmt.Errorf("zero stale comment counts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return up.RowsAffected() + down.RowsAffected(), nil
}
