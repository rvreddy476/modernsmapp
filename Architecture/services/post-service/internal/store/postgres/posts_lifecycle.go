package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Post lifecycle: soft delete → recently deleted → restore | purge ────────
//
// Product decision (founder, 2026-09-04): post deletion is SOFT first, HARD
// later. DeleteUploadCascade (my_uploads.go) sets posts.deleted_at and keeps
// every row and object; this file is the rest of the lifecycle:
//
//   - RestorePost clears deleted_at inside the restore window (author only)
//     and re-emits the canonical search transition plus PostRestored.
//   - ListDeletedPostsByAuthor is the "Recently deleted" listing.
//   - ListPurgeablePosts / PurgePost are the purge worker's two halves: find
//     posts whose deleted_at is older than the window, then erase one post's
//     rows in ONE transaction and hand its now-unreferenced media to the
//     post_purge_media queue for media-service (internal/postpurge).

// ErrPostNotDeleted: restore asked for a post that is live (or not the
// caller's — the two are deliberately indistinguishable).
var ErrPostNotDeleted = errors.New("post is not deleted")

// ErrRestoreWindowExpired: the post is past its purge_at. The purge worker
// may not have run yet, but the contract is the window, not the worker.
var ErrRestoreWindowExpired = errors.New("restore window expired")

// RestorePost undoes a soft delete. Only the author may restore, only while
// deleted_at + window is still in the future. Crosspost embeds that were
// cascaded in the same delete (they share the exact deleted_at) come back
// with it. Returns the restored post's search revision.
func (s *Store) RestorePost(ctx context.Context, postID, authorID uuid.UUID, window time.Duration) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var (
		deletedAt   *time.Time
		contentType string
		visibility  string
		createdAt   time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT deleted_at, content_type, visibility, created_at
		FROM posts WHERE id = $1 AND author_id = $2
		FOR UPDATE`, postID, authorID).Scan(&deletedAt, &contentType, &visibility, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && deletedAt == nil) {
		return 0, ErrPostNotDeleted
	}
	if err != nil {
		return 0, fmt.Errorf("restore: lock post: %w", err)
	}
	if time.Since(*deletedAt) > window {
		return 0, ErrRestoreWindowExpired
	}

	if _, err := tx.Exec(ctx, `
		UPDATE posts SET deleted_at = NULL, updated_at = NOW()
		WHERE id = $1`, postID); err != nil {
		return 0, fmt.Errorf("restore: clear deleted_at: %w", err)
	}
	// Search: the canonical transition re-reads the row (deleted=false,
	// visibility, review_status) and carries the body when eligible, so
	// search-service re-indexes without a read-back.
	rev, err := BumpSearchRevAndEmitTxRev(ctx, tx, postID)
	if err != nil {
		return 0, fmt.Errorf("restore: emit search eligibility: %w", err)
	}
	if err := InsertOutboxEventTx(ctx, tx, events.PostRestored, "post", postID, events.PostRestoredPayload{
		PostID: postID.String(), AuthorID: authorID.String(), ContentType: contentType,
		Visibility: visibility, CreatedAt: createdAt, RestoredAt: time.Now().UTC(), SearchRev: rev,
	}); err != nil {
		return 0, fmt.Errorf("restore: emit PostRestored: %w", err)
	}

	// Crosspost embeds cascaded by the same delete share its deleted_at.
	// Savepoint: the table is optional, exactly as in DeleteUploadCascade.
	_, _ = tx.Exec(ctx, "SAVEPOINT crosspost_restore")
	rows, qerr := tx.Query(ctx, `
		UPDATE posts p SET deleted_at = NULL, updated_at = NOW()
		FROM crosspost_links l
		WHERE l.source_post_id = $1 AND l.target_post_id = p.id
		  AND l.deleted_at = $2 AND p.deleted_at = $2
		RETURNING p.id, p.author_id, p.content_type, p.visibility, p.created_at`, postID, *deletedAt)
	if qerr != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT crosspost_restore")
	} else {
		type target struct {
			id, author              uuid.UUID
			contentType, visibility string
			createdAt               time.Time
		}
		var targets []target
		for rows.Next() {
			var t target
			if err := rows.Scan(&t.id, &t.author, &t.contentType, &t.visibility, &t.createdAt); err == nil {
				targets = append(targets, t)
			}
		}
		rows.Close()
		if len(targets) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE crosspost_links SET deleted_at = NULL
				WHERE source_post_id = $1 AND deleted_at = $2`, postID, *deletedAt); err != nil {
				return 0, fmt.Errorf("restore: crosspost links: %w", err)
			}
		}
		for _, t := range targets {
			trev, err := BumpSearchRevAndEmitTxRev(ctx, tx, t.id)
			if err != nil {
				return 0, fmt.Errorf("restore: emit search eligibility (cascade): %w", err)
			}
			if err := InsertOutboxEventTx(ctx, tx, events.PostRestored, "post", t.id, events.PostRestoredPayload{
				PostID: t.id.String(), AuthorID: t.author.String(), ContentType: t.contentType,
				Visibility: t.visibility, CreatedAt: t.createdAt, RestoredAt: time.Now().UTC(), SearchRev: trev,
			}); err != nil {
				return 0, fmt.Errorf("restore: emit PostRestored (cascade): %w", err)
			}
		}
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT crosspost_restore")
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return rev, nil
}

// ListDeletedPostsByAuthor is the author's "Recently deleted" list, newest
// deletion first. The cursor is the previous page's last deleted_at
// (RFC3339Nano). Media is attached so the row can render a thumbnail.
func (s *Store) ListDeletedPostsByAuthor(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	args := []interface{}{authorID, limit + 1}
	query := `SELECT ` + postCols + `
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NOT NULL`
	if cursor != "" {
		if cursorTime, err := time.Parse(time.RFC3339Nano, cursor); err == nil {
			query += ` AND deleted_at < $3`
			args = append(args, cursorTime)
		}
	}
	query += ` ORDER BY deleted_at DESC LIMIT $2`

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
		next = posts[limit-1].DeletedAt.Format(time.RFC3339Nano)
		posts = posts[:limit]
	}
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return nil, "", err
	}
	return posts, next, nil
}

// PurgeCandidate is one soft-deleted post the purge worker should erase.
type PurgeCandidate struct {
	PostID    uuid.UUID
	AuthorID  uuid.UUID
	DeletedAt time.Time
}

// ListPurgeablePosts returns posts whose deleted_at is at or before
// `before`, oldest first, bounded by limit. Index: idx_posts_deleted_at
// (migration 039).
func (s *Store) ListPurgeablePosts(ctx context.Context, before time.Time, limit int) ([]PurgeCandidate, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, deleted_at FROM posts
		WHERE deleted_at IS NOT NULL AND deleted_at <= $1
		ORDER BY deleted_at ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PurgeCandidate
	for rows.Next() {
		var c PurgeCandidate
		if err := rows.Scan(&c.PostID, &c.AuthorID, &c.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PurgeMediaItem is one media asset queued for deletion at media-service
// because the purged post was its last referrer.
type PurgeMediaItem struct {
	MediaID  uuid.UUID
	PostID   uuid.UUID
	OwnerID  uuid.UUID
	Attempts int
}

// PurgePost HARD-deletes one soft-deleted post: every post-service row keyed
// by it goes in ONE transaction, and each media asset the post referenced
// that NO OTHER POST still references is written to post_purge_media for
// the worker to hand to media-service after commit. PostPurged goes on the
// outbox in the same transaction.
//
// The queue exists because the post_media rows — the thing that proves the
// reference — are gone once this commits; a media-service call that fails
// afterwards must still be retryable, so the intent is durable first.
//
// Idempotent: a post that is already gone (or is live — never purge a
// restored post) returns (nil, nil).
//
// Ordering follows the live FK shape (verified with pg_constraint on
// 2026-09-04): polls, comments, post_engagement_counts carry plain FKs and
// post_moderation_decisions is ON DELETE RESTRICT, so they are emptied
// first; video_metadata, crosspost_links, tunes, article_tags, events,
// media_rights_checks, flick_series_items, video_series_episodes,
// playlist_items, media_chapters, video_end_screens, video_cards and
// watch_progress cascade; video_series.trailer_post_id has no action and is
// nulled; posts.ref_post_id / remix_source_id are ON DELETE SET NULL.
// post_media, reactions, saved_items, reel_hashtags, reel_crosspost,
// slug_history, moderation_reviews, post_product_tags, post_reposts and
// content_reports have no FK to posts at all and are cleaned explicitly.
func (s *Store) PurgePost(ctx context.Context, postID uuid.UUID) ([]PurgeMediaItem, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("purge post: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var authorID uuid.UUID
	var deletedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT author_id, deleted_at FROM posts WHERE id = $1 FOR UPDATE`, postID).
		Scan(&authorID, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // already purged
	}
	if err != nil {
		return nil, fmt.Errorf("purge post: lock: %w", err)
	}
	if deletedAt == nil {
		return nil, nil // restored between the scan and the lock — leave it alone
	}

	// Media the post referenced that no OTHER post (live or deleted) still
	// references. A deleted sibling still counts: it may be restored, and
	// its purge will run this same check for itself.
	mrows, err := tx.Query(ctx, `
		WITH mine AS (
			SELECT media_id FROM post_media WHERE post_id = $1
			UNION
			SELECT cover_media_id FROM posts WHERE id = $1 AND cover_media_id IS NOT NULL
		)
		SELECT m.media_id FROM mine m
		WHERE NOT EXISTS (SELECT 1 FROM post_media pm WHERE pm.media_id = m.media_id AND pm.post_id <> $1)
		  AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.cover_media_id = m.media_id AND p.id <> $1)`, postID)
	if err != nil {
		return nil, fmt.Errorf("purge post: unreferenced media: %w", err)
	}
	var queued []PurgeMediaItem
	for mrows.Next() {
		var id uuid.UUID
		if err := mrows.Scan(&id); err != nil {
			mrows.Close()
			return nil, err
		}
		queued = append(queued, PurgeMediaItem{MediaID: id, PostID: postID, OwnerID: authorID})
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, err
	}
	for _, q := range queued {
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_purge_media (media_id, post_id, owner_id)
			VALUES ($1, $2, $3) ON CONFLICT (media_id, post_id) DO NOTHING`, q.MediaID, q.PostID, q.OwnerID); err != nil {
			return nil, fmt.Errorf("purge post: queue media: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE _purge_comments ON COMMIT DROP AS
			SELECT id FROM comments WHERE post_id = $1`, postID); err != nil {
		return nil, fmt.Errorf("purge post: collect comments: %w", err)
	}

	stmts := []string{
		`UPDATE comments SET parent_id = NULL
			WHERE parent_id IN (SELECT id FROM _purge_comments)
			  AND id NOT IN (SELECT id FROM _purge_comments)`,
		`DELETE FROM comment_idempotency WHERE post_id = $1`,
		`DELETE FROM reactions
			WHERE (target_type = 'post' AND target_id = $1)
			   OR (target_type = 'comment' AND target_id IN (SELECT id FROM _purge_comments))`,
		`DELETE FROM content_reports
			WHERE (target_type = 'post' AND target_id = $1)
			   OR (target_type = 'comment' AND target_id IN (SELECT id FROM _purge_comments))`,
		`DELETE FROM engagement_event_log
			WHERE target_id = $1 OR target_id IN (SELECT id FROM _purge_comments)`,
		`DELETE FROM comments WHERE id IN (SELECT id FROM _purge_comments)`,
		`DELETE FROM post_product_tags WHERE post_id = $1`,
		`DELETE FROM reel_hashtags WHERE reel_id = $1`,
		`DELETE FROM reel_crosspost WHERE source_reel_id = $1`,
		`DELETE FROM slug_history WHERE reel_id = $1`,
		`DELETE FROM moderation_reviews WHERE reel_id = $1`,
		`DELETE FROM post_media WHERE post_id = $1`,
		`DELETE FROM poll_votes WHERE post_id = $1`,
		`DELETE FROM poll_options WHERE post_id = $1`,
		`DELETE FROM polls WHERE post_id = $1`,
		`DELETE FROM watch_progress WHERE post_id = $1`,
		`DELETE FROM saved_items WHERE target_type = 'post' AND target_id = $1`,
		`DELETE FROM post_engagement_counts WHERE post_id = $1`,
		`DELETE FROM post_reposts WHERE original_post_id = $1`,
		`DELETE FROM post_moderation_decisions WHERE post_id = $1`,
		`UPDATE video_series SET trailer_post_id = NULL WHERE trailer_post_id = $1`,
		`DELETE FROM posts WHERE id = $1`,
	}
	for _, stmt := range stmts {
		var execErr error
		if strings.Contains(stmt, "$1") {
			_, execErr = tx.Exec(ctx, stmt, postID)
		} else {
			_, execErr = tx.Exec(ctx, stmt)
		}
		if execErr != nil {
			return nil, fmt.Errorf("purge post: %s: %w", firstWords(stmt), execErr)
		}
	}

	mediaIDs := make([]string, 0, len(queued))
	for _, q := range queued {
		mediaIDs = append(mediaIDs, q.MediaID.String())
	}
	if err := InsertOutboxEventTx(ctx, tx, events.PostPurged, "post", postID, events.PostPurgedPayload{
		PostID: postID.String(), AuthorID: authorID.String(), MediaIDs: mediaIDs, PurgedAt: time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("purge post: emit PostPurged: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("purge post: commit: %w", err)
	}
	return queued, nil
}

// ── post_purge_media queue ──────────────────────────────────────────────────

// PendingPurgeMedia returns queued media whose next attempt is due.
func (s *Store) PendingPurgeMedia(ctx context.Context, limit int) ([]PurgeMediaItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT media_id, post_id, owner_id, attempts FROM post_purge_media
		WHERE next_attempt_at <= NOW()
		ORDER BY next_attempt_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PurgeMediaItem
	for rows.Next() {
		var it PurgeMediaItem
		if err := rows.Scan(&it.MediaID, &it.PostID, &it.OwnerID, &it.Attempts); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ResolvePurgeMedia removes a queue row: the asset is gone, or media-service
// refused because something else still references it (nothing to retry).
func (s *Store) ResolvePurgeMedia(ctx context.Context, mediaID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM post_purge_media WHERE media_id = $1 AND post_id = $2`, mediaID, postID)
	return err
}

// DeferPurgeMedia records a failed attempt and backs the row off
// (attempts² minutes, capped at an hour) so a media-service outage does
// not turn the worker into a tight loop.
func (s *Store) DeferPurgeMedia(ctx context.Context, mediaID, postID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE post_purge_media
		SET attempts = attempts + 1,
		    last_error = LEFT($3, 500),
		    next_attempt_at = NOW() + LEAST(make_interval(mins => (attempts + 1) * (attempts + 1)), interval '1 hour')
		WHERE media_id = $1 AND post_id = $2`, mediaID, postID, reason)
	return err
}
