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
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, publish_at FROM posts
		WHERE publish_at IS NOT NULL AND publish_at <= $1 AND deleted_at IS NULL
		ORDER BY publish_at ASC
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
	tag, err := s.db.Exec(ctx, `
		UPDATE posts SET publish_at = $3, updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND publish_at IS NOT NULL AND deleted_at IS NULL`,
		postID, authorID, publishAt)
	if err != nil {
		return fmt.Errorf("reschedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPostNotScheduled
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
	return posts, next, nil
}
