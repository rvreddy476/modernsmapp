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
	// Title and ChannelName (migration 006) render the push text at
	// delivery without a per-recipient lookup. Either may be empty for
	// jobs enqueued from events that predate the fields.
	Title       string
	ChannelName string
	// Phase and ReminderCursor (migration 011) exist for "creator is live"
	// jobs, which page three recipient groups: the stream's reminder holders
	// first, then the channel's subscribers, then the creator's followers.
	// Upload jobs are always FanoutPhaseSubscribers and never touch
	// ReminderCursor. For a live job PostID holds the STREAM id and
	// ChannelID may be uuid.Nil (a creator with no channel has reminder
	// holders and followers only).
	Phase          string
	ReminderCursor string
	// FollowerCursor (migration 012) is graph-service's opaque next_cursor
	// token for the third and last phase of a live job, the creator's
	// followers. Upload jobs never touch it.
	FollowerCursor string
	// Waits (migration 021) counts consecutive deferrals of an upload job
	// whose post was not visible yet; it bounds the wait.
	Waits int
}

// Fan-out phases (subscriber_fanout_jobs.phase). A live job walks them in
// this order; an upload job is only ever FanoutPhaseSubscribers.
const (
	FanoutPhaseReminders   = "reminders"
	FanoutPhaseSubscribers = "subscribers"
	FanoutPhaseFollowers   = "followers"
)

// EnqueueFanoutJob records a fan-out durably. Idempotent on post_id: a
// duplicate PostCreated (or live.stream.started) delivery does not create a
// second job. An empty Phase is the upload default, 'subscribers'.
//
// An UPLOAD job is also woken or reopened by a repeated trigger (migration
// 021): PostCreated and the later "post became visible" signal
// (PostSearchEligibilityChanged) share this one row per post, so
//   - a job waiting for its post becomes due now;
//   - a FAILED job is reopened from its cursor; the per-(post, user)
//     markers keep anyone already told from hearing it twice;
//   - a RUNNING job is only marked wake_requested (migration 022, REG-5):
//     the pass in flight may already have read the post as not visible,
//     so however it ends short of completion (defer, release, fail,
//     expire) it leaves the job pending and due now, not on the backoff
//     curve. The running pass itself is not disturbed;
//   - a completed job is left alone: one announcement per post.
//
// A "creator is live" job keeps plain DO NOTHING semantics.
func (s *Store) EnqueueFanoutJob(ctx context.Context, j *FanoutJob) error {
	phase := j.Phase
	if phase == "" {
		phase = FanoutPhaseSubscribers
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO subscriber_fanout_jobs AS j
			(post_id, channel_id, author_id, content_type, deep_link,
			 notif_type, visibility, post_created_at, title, channel_name, phase)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (post_id) DO UPDATE
		SET status = CASE WHEN j.status = 'running' THEN 'running' ELSE 'pending' END,
		    wake_requested = (j.status = 'running'),
		    available_at = CASE WHEN j.status = 'running' THEN j.available_at ELSE NOW() END,
		    waits = CASE WHEN j.status = 'running' THEN j.waits ELSE 0 END,
		    attempts = CASE WHEN j.status = 'running' THEN j.attempts ELSE 0 END,
		    wait_reason = CASE WHEN j.status = 'running' THEN j.wait_reason ELSE '' END,
		    visibility = EXCLUDED.visibility,
		    title = CASE WHEN j.title = '' THEN EXCLUDED.title ELSE j.title END,
		    updated_at = NOW()
		WHERE EXCLUDED.notif_type <> $12
		  AND j.notif_type <> $12
		  AND (j.status IN ('failed', 'running')
		       OR (j.status = 'pending' AND j.available_at > NOW()))`,
		j.PostID, j.ChannelID, j.AuthorID, j.ContentType, j.DeepLink,
		j.NotifType, j.Visibility, j.PostCreatedAt, j.Title, j.ChannelName, phase,
		liveNotifType)
	return err
}

// liveNotifType mirrors service.LiveNotifType (the store cannot import the
// service package): live jobs are never woken or replayed, because a late
// "is live" push is worse than none.
const liveNotifType = "creator_went_live"

// AdvanceFanoutReminders persists progress through a live job's reminder
// holders. `cursor` is live-service-v2's opaque next_after token. When
// `done` is set the reminder pages are exhausted and the job moves on to
// the subscriber phase, so a retry never re-walks the reminders.
func (s *Store) AdvanceFanoutReminders(ctx context.Context, postID uuid.UUID, cursor string, deliveredDelta int64, done bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET reminder_cursor = $2, delivered = delivered + $3,
		    phase = CASE WHEN $4 THEN 'subscribers' ELSE phase END,
		    claimed_at = NOW(), updated_at = NOW()
		WHERE post_id = $1`, postID, cursor, deliveredDelta, done)
	return err
}

// BeginFanoutFollowers moves a live job into its last phase, the creator's
// followers, once the subscriber pages are exhausted (or there is no
// channel to page). After this a retry re-walks neither the reminder
// holders nor the subscribers. It touches no cursor, so a duplicate call
// cannot disturb a follower walk in progress.
func (s *Store) BeginFanoutFollowers(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET phase = 'followers', claimed_at = NOW(), updated_at = NOW()
		WHERE post_id = $1`, postID)
	return err
}

// AdvanceFanoutFollowers persists progress through a live job's followers.
// `cursor` is graph-service's opaque next_cursor token for the page to
// fetch next; it is written back unchanged when a recipient in the page
// failed, which pins the walk there for the retry. The reminder and
// subscriber cursors are untouched.
func (s *Store) AdvanceFanoutFollowers(ctx context.Context, postID uuid.UUID, cursor string, deliveredDelta int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET follower_cursor = $2, delivered = delivered + $3,
		    claimed_at = NOW(), updated_at = NOW()
		WHERE post_id = $1`, postID, cursor, deliveredDelta)
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
			WHERE (status = 'pending' AND available_at <= NOW())
			   OR (status = 'running' AND claimed_at < $1)
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE subscriber_fanout_jobs j
		SET status = 'running', claimed_at = NOW(),
		    attempts = j.attempts + 1, wake_requested = false, updated_at = NOW()
		FROM claimable WHERE j.post_id = claimable.post_id
		RETURNING j.post_id, j.channel_id, j.author_id, j.content_type,
		          j.deep_link, j.notif_type, j.visibility, j.post_created_at,
		          j.cursor, j.delivered, j.attempts, j.title, j.channel_name,
		          j.phase, j.reminder_cursor, j.follower_cursor, j.waits`,
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
			&j.Cursor, &j.Delivered, &j.Attempts, &j.Title, &j.ChannelName,
			&j.Phase, &j.ReminderCursor, &j.FollowerCursor, &j.Waits); err != nil {
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

// CompleteFanoutJob marks a job finished. A wake that arrived during the
// pass goes with it: the pass just decided every recipient.
func (s *Store) CompleteFanoutJob(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'completed', wake_requested = false, updated_at = NOW()
		WHERE post_id = $1`, postID)
	return err
}

// ReleaseFanoutJob returns a job to 'pending' after a transient failure,
// due again at retryAt (backoff, migration 021), resuming from its cursor.
// A trigger that arrived during the pass (wake_requested) makes it due now
// with a fresh attempt budget, as the trigger does to a pending job.
func (s *Store) ReleaseFanoutJob(ctx context.Context, postID uuid.UUID, reason string, retryAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'pending', last_error = $2,
		    available_at = CASE WHEN wake_requested THEN NOW() ELSE $3 END,
		    attempts = CASE WHEN wake_requested THEN 0 ELSE attempts END,
		    wait_reason = '', waits = 0, wake_requested = false, updated_at = NOW()
		WHERE post_id = $1`, postID, reason, retryAt)
	return err
}

// DeferFanoutJob parks an upload job whose post is not visible yet until
// `until`. The claim that found it so was not a failed attempt, so the
// attempt it took is given back; waits bounds the wait instead.
//
// REG-5: when the "post became visible" trigger arrived while this pass was
// running (wake_requested), the read that said "wait" may be stale, so the
// job is left due NOW with the counters the trigger gives a pending job,
// instead of parked for up to 30 minutes.
func (s *Store) DeferFanoutJob(ctx context.Context, postID uuid.UUID, reason string, until time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'pending', last_error = $2,
		    wait_reason = CASE WHEN wake_requested THEN '' ELSE $2 END,
		    available_at = CASE WHEN wake_requested THEN NOW() ELSE $3 END,
		    waits = CASE WHEN wake_requested THEN 0 ELSE waits + 1 END,
		    attempts = CASE WHEN wake_requested THEN 0 ELSE GREATEST(attempts - 1, 0) END,
		    wake_requested = false, updated_at = NOW()
		WHERE post_id = $1`, postID, reason, until)
	return err
}

// FailFanoutJob parks a job after exhausting its retries. An upload job
// stays replayable (RequeueFailedFanoutJobs) and a new trigger reopens it;
// one that arrived during this pass (wake_requested) reopens it here.
func (s *Store) FailFanoutJob(ctx context.Context, postID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = CASE WHEN wake_requested THEN 'pending' ELSE 'failed' END,
		    available_at = CASE WHEN wake_requested THEN NOW() ELSE available_at END,
		    attempts = CASE WHEN wake_requested THEN 0 ELSE attempts END,
		    waits = CASE WHEN wake_requested THEN 0 ELSE waits END,
		    last_error = $2, wait_reason = '', wake_requested = false, updated_at = NOW()
		WHERE post_id = $1`, postID, reason)
	return err
}

// ExpireFanoutJob ends an upload job whose post never became visible within
// the wait ceiling. Keeping wait_reason keeps it out of the automatic
// replay; a later trigger for the post still reopens it, and one that
// arrived during this pass (wake_requested) reopens it here.
func (s *Store) ExpireFanoutJob(ctx context.Context, postID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = CASE WHEN wake_requested THEN 'pending' ELSE 'failed' END,
		    available_at = CASE WHEN wake_requested THEN NOW() ELSE available_at END,
		    attempts = CASE WHEN wake_requested THEN 0 ELSE attempts END,
		    waits = CASE WHEN wake_requested THEN 0 ELSE waits END,
		    wait_reason = CASE WHEN wake_requested THEN '' ELSE $2 END,
		    last_error = 'expired waiting: ' || $2, wake_requested = false, updated_at = NOW()
		WHERE post_id = $1`, postID, reason)
	return err
}

// RequeueFailedFanoutJobs is the replay path for upload jobs that failed on
// errors (post-service or graph down for longer than the retry budget):
// each one quiet for at least `cooldown` and younger than `window` goes back
// to pending, at most maxReplays times per job. Live jobs and jobs that
// expired waiting for their post are never replayed.
func (s *Store) RequeueFailedFanoutJobs(ctx context.Context, maxReplays int, cooldown, window time.Duration) (int64, error) {
	now := time.Now()
	tag, err := s.db.Exec(ctx, `
		UPDATE subscriber_fanout_jobs
		SET status = 'pending', attempts = 0, available_at = NOW(),
		    replays = replays + 1, updated_at = NOW()
		WHERE status = 'failed' AND wait_reason = ''
		  AND notif_type <> $1 AND replays < $2
		  AND updated_at < $3 AND created_at > $4`,
		liveNotifType, maxReplays, now.Add(-cooldown), now.Add(-window))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
