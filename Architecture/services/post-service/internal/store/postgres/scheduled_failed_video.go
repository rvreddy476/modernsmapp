package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── A scheduled video whose media failed (REG-6, 2026-11-18) ──────────────
//
// The schedule worker only publishes a scheduled video once its transcode is
// ready (scheduledVideoReadySQL). When the media fails for good, the video
// gate (internal/videogate) marks video_metadata.upload_status='failed' and
// rejects the post, but the post kept its publish_at: it sat in the
// creator's Scheduled list for ever, skipped on every tick, and nobody was
// told. The worker now takes such a post off the schedule into the state the
// pending-video sweep leaves an unscheduled failed video in (review_status
// 'rejected', upload_status 'failed', author-only) and writes the creator's
// notice, in one transaction. It does not wait for publish_at: the media
// cannot recover, so the creator hears now rather than at the slot.

// failedScheduledVideoSQL: a scheduled, live (not deleted), non-note post
// whose video media failed. alias names the posts row.
func failedScheduledVideoSQL(alias string) string {
	return alias + `.publish_at IS NOT NULL AND ` + alias + `.deleted_at IS NULL AND ` + NotNoteSQL(alias) + `
		AND EXISTS (SELECT 1 FROM video_metadata vm WHERE vm.post_id = ` + alias + `.id AND vm.upload_status = 'failed')`
}

// ListFailedScheduledVideos returns up to limit scheduled video posts whose
// media failed, earliest slot first.
func (s *Store) ListFailedScheduledVideos(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.id FROM posts p
		WHERE `+failedScheduledVideoSQL("p")+`
		ORDER BY p.publish_at ASC, p.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list failed scheduled videos: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ResolveFailedScheduledVideo takes one such post off the schedule: publish_at
// cleared, review_status 'rejected' unless a moderator already holds it in
// another non-live state (flagged, needs_changes stay as they are; an
// 'approved' one is rejected too, since there is nothing to play), and the
// scheduled_video_failed notice written, with the search projection, in one
// transaction. The guarded UPDATE re-checks the rule, so two replicas (or a
// "publish now" racing it) resolve a post once; false = it no longer
// qualified.
func (s *Store) ResolveFailedScheduledVideo(ctx context.Context, postID uuid.UUID) (bool, error) {
	return s.WithSearchEligibilityTx(ctx, postID, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var (
			authorID    uuid.UUID
			contentType string
			title       string
		)
		err := tx.QueryRow(ctx, `
			UPDATE posts SET publish_at = NULL,
			       review_status = CASE WHEN review_status IN ('pending','approved') THEN 'rejected' ELSE review_status END,
			       updated_at = NOW()
			WHERE id = $1 AND `+failedScheduledVideoSQL("posts")+`
			RETURNING author_id, content_type, COALESCE(title, '')`, postID).
			Scan(&authorID, &contentType, &title)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("resolve failed scheduled video: %w", err)
		}
		if err := InsertOutboxEventTx(ctx, tx, EventPostAuthorNotice, "post", postID,
			scheduledVideoFailedNotice(postID, authorID, contentType, title, time.Now().UTC())); err != nil {
			return false, err
		}
		return true, nil
	})
}

// scheduledVideoFailedNotice is the creator's notice for REG-6. notice_id is
// the post id: a post leaves the schedule this way once.
func scheduledVideoFailedNotice(postID, authorID uuid.UUID, contentType, title string, at time.Time) AuthorNoticePayload {
	return AuthorNoticePayload{
		NoticeID: postID.String(), Kind: AuthorNoticeScheduledVideoFailed,
		PostID: postID.String(), AuthorID: authorID.String(),
		ContentType: contentType, Title: title,
		Reason: ScheduledVideoFailedReason, OccurredAt: at,
	}
}
