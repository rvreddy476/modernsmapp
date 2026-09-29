package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Scheduled publish (founder, 2026-09-05; migration 042) ─────────────────
//
// posts.publish_at IS NOT NULL ⇔ the post is scheduled. While scheduled it
// is stored with everything a live post has (media, poll, review verdict),
// but it is author-only, in no feed/search/hashtag surface, and no
// PostCreated has been emitted. This file is the lifecycle:
//
//   - ListDueScheduledPosts is the worker's scan (internal/postschedule).
//   - PublishScheduledPost flips ONE post live inside a transaction and
//     writes the PostCreated outbox row in the same commit. The guarded
//     UPDATE is the exactly-once gate: two workers (or a worker and a
//     "publish now" request) racing on the same row serialise on the row
//     lock and the loser matches zero rows.
//   - ReschedulePost moves publish_at, author-only, only while scheduled.
//   - ListScheduledPostsByAuthor is the author's "Scheduled" list.

// ErrPostNotScheduled: the post is live, deleted, or not the caller's — the
// three are deliberately indistinguishable to a non-author.
var ErrPostNotScheduled = errors.New("post is not scheduled")

// ScheduledCandidate is one due post the schedule worker should publish.
type ScheduledCandidate struct {
	PostID    uuid.UUID
	AuthorID  uuid.UUID
	PublishAt time.Time
}

// ListDueScheduledPosts returns live (not deleted) scheduled posts whose
// publish_at is at or before `now`, earliest first. Index: idx_posts_scheduled.
func (s *Store) ListDueScheduledPosts(ctx context.Context, now time.Time, limit int) ([]ScheduledCandidate, error) {
	if limit <= 0 {
		limit = 100
	}
	// A parked post (post_publish_blocks, migration 055) is not due: the
	// worker would only refuse it again. Rescheduling or "publish now"
	// clears the block.
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.author_id, p.publish_at FROM posts p
		WHERE p.publish_at IS NOT NULL AND p.publish_at <= $1 AND p.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM post_publish_blocks b WHERE b.post_id = p.id)
		ORDER BY p.publish_at ASC
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledCandidate
	for rows.Next() {
		var c ScheduledCandidate
		if err := rows.Scan(&c.PostID, &c.AuthorID, &c.PublishAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PublishedPost is what PublishScheduledPost reports after the commit.
type PublishedPost struct {
	// SearchRev is the revision stamped on the row in the same statement
	// that made it live; the PostCreated carries it so search cannot apply
	// an older transition on top.
	SearchRev int64
	// PublishedAt is the moment written to created_at, published_at and
	// updated_at — the post sorts as new from here.
	PublishedAt time.Time
}

// PublishScheduledPost makes one scheduled post live.
//
// In ONE transaction: the guarded UPDATE clears publish_at, stamps
// published_at, moves created_at/updated_at to `now` and bumps search_rev;
// then `event` (given the new revision) builds the outbox payload that is
// inserted before commit. Returns (nil, nil) when the row was not scheduled
// any more — already published by a concurrent run, deleted, or never
// scheduled — which is what makes a second worker tick a no-op rather than
// a second PostCreated.
//
// dueOnly=true (the worker) refuses a post whose publish_at is still in the
// future; false ("publish now") takes it regardless. authorID, when non-nil,
// restricts the flip to that author's post.
func (s *Store) PublishScheduledPost(
	ctx context.Context,
	postID uuid.UUID,
	authorID *uuid.UUID,
	now time.Time,
	dueOnly bool,
	event func(rev int64) (eventType string, payload interface{}, err error),
) (*PublishedPost, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("publish scheduled: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := `
		UPDATE posts
		SET publish_at = NULL,
		    published_at = $2,
		    created_at = $2,
		    updated_at = $2,
		    search_rev = search_rev + 1
		WHERE id = $1 AND publish_at IS NOT NULL AND deleted_at IS NULL`
	args := []interface{}{postID, now}
	if dueOnly {
		query += ` AND publish_at <= $2`
	}
	if authorID != nil {
		args = append(args, *authorID)
		query += fmt.Sprintf(` AND author_id = $%d`, len(args))
	}
	query += ` RETURNING search_rev`

	var rev int64
	err = tx.QueryRow(ctx, query, args...).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("publish scheduled: flip: %w", err)
	}

	if event != nil {
		eventType, payload, err := event(rev)
		if err != nil {
			return nil, err
		}
		if eventType != "" {
			if err := InsertOutboxEventTx(ctx, tx, eventType, "post", postID, payload); err != nil {
				return nil, fmt.Errorf("publish scheduled: outbox: %w", err)
			}
		}
	}
	// A live post carries no publication block (migration 055).
	if _, err := tx.Exec(ctx, `DELETE FROM post_publish_blocks WHERE post_id = $1`, postID); err != nil {
		return nil, fmt.Errorf("publish scheduled: clear block: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("publish scheduled: commit: %w", err)
	}
	return &PublishedPost{SearchRev: rev, PublishedAt: now}, nil
}

// ReschedulePost moves a scheduled post's publish_at. Author-only and only
// while the post is still scheduled: a post the worker has already
// published (or that was deleted) returns ErrPostNotScheduled. The window
// check (≥5 min, ≤30 days) is the service's; this is the durable half.
func (s *Store) ReschedulePost(ctx context.Context, postID, authorID uuid.UUID, publishAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reschedule: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE posts SET publish_at = $3, updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND publish_at IS NOT NULL AND deleted_at IS NULL`,
		postID, authorID, publishAt)
	if err != nil {
		return fmt.Errorf("reschedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPostNotScheduled
	}
	// The author acted on the post: a parked post is re-armed (migration
	// 055). If standing still refuses, the worker parks it again.
	if _, err := tx.Exec(ctx, `DELETE FROM post_publish_blocks WHERE post_id = $1`, postID); err != nil {
		return fmt.Errorf("reschedule: clear block: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("reschedule: commit: %w", err)
	}
	return nil
}

// BlockScheduledPost parks a scheduled post (migration 055) with the
// reason the author's Scheduled list shows. Only a post that is still
// scheduled can be parked; a live or deleted one is left alone (false).
// Idempotent: a second park overwrites the reason.
func (s *Store) BlockScheduledPost(ctx context.Context, postID uuid.UUID, reason string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO post_publish_blocks (post_id, reason, blocked_at)
		SELECT id, $2, NOW() FROM posts
		WHERE id = $1 AND publish_at IS NOT NULL AND deleted_at IS NULL
		ON CONFLICT (post_id) DO UPDATE SET reason = EXCLUDED.reason, blocked_at = EXCLUDED.blocked_at`,
		postID, reason)
	if err != nil {
		return false, fmt.Errorf("block scheduled: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// PublishBlockReason reads a parked post's reason ("" when not parked).
func (s *Store) PublishBlockReason(ctx context.Context, postID uuid.UUID) (string, error) {
	var reason string
	err := s.db.QueryRow(ctx, `SELECT reason FROM post_publish_blocks WHERE post_id = $1`, postID).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return reason, err
}

// attachPublishBlocks sets PublishBlockedReason on every parked post in
// the slice (the author's Scheduled list).
func (s *Store) attachPublishBlocks(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
	}
	rows, err := s.db.Query(ctx, `SELECT post_id, reason FROM post_publish_blocks WHERE post_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	reasons := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var reason string
		if err := rows.Scan(&id, &reason); err != nil {
			return err
		}
		reasons[id] = reason
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range posts {
		if r, ok := reasons[posts[i].ID]; ok {
			reason := r
			posts[i].PublishBlockedReason = &reason
		}
	}
	return nil
}

// ListScheduledPostsByAuthor is the author's "Scheduled" list, newest
// publish_at first. The cursor is the previous page's last publish_at
// (RFC3339Nano). Media is attached so the row can render a thumbnail.
// Index: idx_posts_author_scheduled.
func (s *Store) ListScheduledPostsByAuthor(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	args := []interface{}{authorID, limit + 1}
	query := `SELECT ` + postCols + `
		FROM posts
		WHERE author_id = $1 AND publish_at IS NOT NULL AND deleted_at IS NULL`
	if cursor != "" {
		if cursorTime, err := time.Parse(time.RFC3339Nano, cursor); err == nil {
			query += ` AND publish_at < $3`
			args = append(args, cursorTime)
		}
	}
	query += ` ORDER BY publish_at DESC, id DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}
	var next string
	if len(posts) > limit {
		next = posts[limit-1].PublishAt.Format(time.RFC3339Nano)
		posts = posts[:limit]
	}
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return nil, "", err
	}
	if err := s.attachPublishBlocks(ctx, posts); err != nil {
		return nil, "", err
	}
	return posts, next, nil
}
