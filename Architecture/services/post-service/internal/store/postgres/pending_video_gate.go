package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Pending video posts: the reconciler's reads (reels-1, 2026-11-17) ─────
//
// A video post waits at review_status='pending' until its media is
// transcoded and scanned; the MediaTranscodeCompleted consumer flips it. A
// missed or failed flip used to leave it pending for ever. The reconciler
// (service/pending_video_gate.go) sweeps the ones left behind and applies the
// same rule from media_assets, the shared table media-service writes.

// PendingVideoPost is one post the reconciler judges, with its media's state
// as media_assets has it now ("" when the asset row is gone).
type PendingVideoPost struct {
	PostID           uuid.UUID
	MediaID          uuid.UUID
	CreatedAt        time.Time
	ProcessingStatus string
	ModerationStatus string
}

// PendingVideoCursor is a keyset position in (created_at, id) order, so a
// batch of posts that stay pending (a multi-hour transcode) cannot starve the
// rest: the sweep walks past them and wraps around.
type PendingVideoCursor struct {
	CreatedAt time.Time
	PostID    uuid.UUID
}

// ListPendingVideoPosts returns up to limit live video posts still pending
// that were created before olderThan, after the cursor (nil = from the
// start), oldest first. Index: idx_posts_review_status (review_status <>
// 'approved').
func (s *Store) ListPendingVideoPosts(ctx context.Context, olderThan time.Time, after *PendingVideoCursor, limit int) ([]PendingVideoPost, error) {
	if limit <= 0 {
		limit = 100
	}
	afterAt, afterID := time.Time{}, uuid.Nil
	if after != nil {
		afterAt, afterID = after.CreatedAt, after.PostID
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.id, vm.media_asset_id, p.created_at,
		       COALESCE(ma.processing_status, ''), COALESCE(ma.moderation_status, '')
		FROM posts p
		JOIN video_metadata vm ON vm.post_id = p.id AND vm.media_asset_id IS NOT NULL
		LEFT JOIN media_assets ma ON ma.id = vm.media_asset_id
		WHERE p.review_status = 'pending' AND p.deleted_at IS NULL
		  AND p.created_at < $1
		  AND (p.created_at, p.id) > ($2, $3)
		ORDER BY p.created_at, p.id
		LIMIT $4`, olderThan, afterAt, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending video posts: %w", err)
	}
	defer rows.Close()
	var out []PendingVideoPost
	for rows.Next() {
		var r PendingVideoPost
		if err := rows.Scan(&r.PostID, &r.MediaID, &r.CreatedAt, &r.ProcessingStatus, &r.ModerationStatus); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetPendingVideoPost is ListPendingVideoPosts for one post, with no age
// threshold: the create path's immediate second look. (nil, nil) when the
// post is not a pending video post.
func (s *Store) GetPendingVideoPost(ctx context.Context, postID uuid.UUID) (*PendingVideoPost, error) {
	var r PendingVideoPost
	err := s.db.QueryRow(ctx, `
		SELECT p.id, vm.media_asset_id, p.created_at,
		       COALESCE(ma.processing_status, ''), COALESCE(ma.moderation_status, '')
		FROM posts p
		JOIN video_metadata vm ON vm.post_id = p.id AND vm.media_asset_id IS NOT NULL
		LEFT JOIN media_assets ma ON ma.id = vm.media_asset_id
		WHERE p.id = $1 AND p.review_status = 'pending' AND p.deleted_at IS NULL`, postID).
		Scan(&r.PostID, &r.MediaID, &r.CreatedAt, &r.ProcessingStatus, &r.ModerationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get pending video post: %w", err)
	}
	return &r, nil
}

// MarkVideoMetadataFailed records that the post's media failed, so the
// creator's video reads (GET /v1/videos/:id, the publish route) say
// "failed" instead of "pending" for ever. A ready row is left alone.
func (s *Store) MarkVideoMetadataFailed(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE video_metadata SET upload_status = 'failed', updated_at = NOW()
		WHERE post_id = $1 AND upload_status <> 'ready' AND upload_status <> 'failed'`, postID)
	return err
}
