package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Module 1 P0-3 — durable subscriber fan-out job store.

// FanoutJob is one pending/running upload-notification fan-out.
type FanoutJob struct {
	PostID        uuid.UUID
	ChannelID     uuid.UUID
	AuthorID      uuid.UUID
	ContentType   string
	DeepLink      string
	NotifType     string
	Visibility    string
	PostCreatedAt time.Time
	Cursor        uuid.UUID
	Delivered     int64
	Attempts      int
}

// EnqueueFanoutJob records a fan-out durably. Idempotent on post_id: a
// duplicate PostCreated delivery does not create a second job.
func (s *Store) EnqueueFanoutJob(ctx context.Context, j *FanoutJob) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO subscriber_fanout_jobs
			(post_id, channel_id, author_id, content_type, deep_link,
			 notif_type, visibility, post_created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (post_id) DO NOTHING`,
		j.PostID, j.ChannelID, j.AuthorID, j.ContentType, j.DeepLink,
		j.NotifType, j.Visibility, j.PostCreatedAt)
	return err
}

// ClaimFanoutJobs claims up to limit jobs for this worker. Uses
// FOR UPDATE SKIP LOCKED so multiple replicas never process the same
// job; jobs stuck in 'running' past staleAfter are reclaimed (dead
// worker) and resume from their stored cursor.
func (s *Store) ClaimFanoutJobs(ctx context.Context, staleAfter time.Duration, limit int) ([]FanoutJob, error) {
	rows, err := s.db.Query(ctx, `
		WITH claimable AS (
			SELECT post_id FROM subscriber_fanout_jobs
			WHERE status = 'pending'
			   OR (status = 'running' AND claimed_at < $1)
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE subscriber_fanout_jobs j
		SET status = 'running', claimed_at = NOW(),
		    attempts = j.attempts + 1, updated_at = NOW()
		FROM claimable WHERE j.post_id = claimable.post_id
		RETURNING j.post_id, j.channel_id, j.author_id, j.content_type,
		          j.deep_link, j.notif_type, j.visibility, j.post_created_at,
		          j.cursor, j.delivered, j.attempts`,
		time.Now().Add(-staleAfter), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []FanoutJob
	for rows.Next() {
		var j FanoutJob
		if err := rows.Scan(&j.PostID, &j.ChannelID, &j.AuthorID, &j.ContentType,
			&j.DeepLink, &j.NotifType, &j.Visibility, &j.PostCreatedAt,
			&j.Cursor, &j.Delivered, &j.Attempts); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// AdvanceFanoutCursor persists progress after each delivered batch so a
// crash resumes rather than restarting or dropping recipients.
func (s *Store) AdvanceFanoutCursor(ctx context.Context, postID, cursor uuid.UUID, deliveredDelta int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET cursor = $2, delivered = delivered + $3,
		    claimed_at = NOW(), updated_at = NOW()
		WHERE post_id = $1`, postID, cursor, deliveredDelta)
	return err
}

// CompleteFanoutJob marks a job finished.
func (s *Store) CompleteFanoutJob(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'completed', updated_at = NOW()
		WHERE post_id = $1`, postID)
	return err
}

// ReleaseFanoutJob returns a job to 'pending' after a transient failure
// so the next tick resumes it from its cursor.
func (s *Store) ReleaseFanoutJob(ctx context.Context, postID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'pending', last_error = $2, updated_at = NOW()
		WHERE post_id = $1`, postID, reason)
	return err
}

// FailFanoutJob parks a job permanently after exhausting retries.
func (s *Store) FailFanoutJob(ctx context.Context, postID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'failed', last_error = $2, updated_at = NOW()
		WHERE post_id = $1`, postID, reason)
	return err
}

// AlreadyDelivered reports whether this recipient already reached a
// terminal state for this post. Used as a cheap skip on retry; it is NOT
// the correctness mechanism (the inbox write itself is idempotent).
func (s *Store) AlreadyDelivered(ctx context.Context, postID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM subscriber_fanout_delivered
			WHERE post_id = $1 AND user_id = $2)`, postID, userID).Scan(&exists)
	return exists, err
}

// MarkDelivered records per-recipient delivery. Returns false when the
// recipient already had a row — duplicate event delivery or a resumed
// batch then creates exactly one notification per (post, user).
func (s *Store) MarkDelivered(ctx context.Context, postID, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO subscriber_fanout_delivered (post_id, user_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, postID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// CleanupFanoutRecords drops completed jobs and their dedup rows older
// than retention.
func (s *Store) CleanupFanoutRecords(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	if _, err := s.db.Exec(ctx,
		`DELETE FROM subscriber_fanout_delivered WHERE delivered_at < $1`, cutoff); err != nil {
		return 0, err
	}
	tag, err := s.db.Exec(ctx,
		`DELETE FROM subscriber_fanout_jobs WHERE status = 'completed' AND updated_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
