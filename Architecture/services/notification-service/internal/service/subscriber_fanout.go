package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/subscribers"
	"github.com/google/uuid"
)

// Module 1 P0-3 — subscriber upload notifications.
//
// Corrections this implements (Codex approval §P0-3):
//   - Recipients are CHANNEL SUBSCRIBERS, never followers. A channel with
//     no subscribers notifies nobody; there is no follower fallback.
//   - The work is durable: the consumer persists a job row before the
//     Kafka offset commits, and a resumable worker drains it. A crash
//     resumes from the stored cursor instead of losing recipients.
//   - No 5,000-recipient cap: keyset pagination runs to exhaustion.
//   - Respects creator notify_subscribers (checked before enqueue),
//     subscriber notify_on (filtered in SQL by the subscriber source),
//     viewer push preferences, self-exclusion, and per-(post,user) dedup.
//
// Tube launch (2026-09-12): every subscriber is notified on a new upload
// by default. The per-channel bell (notify_on all|none) is the opt-out
// and is applied by the subscriber source; the creator's per-upload
// notify_subscribers switch is applied at enqueue. The push reads
// "{channel} uploaded: {title}", rendered from inputs carried on the job
// so delivery needs no per-recipient lookup.

const (
	fanoutPageSize     = 500
	fanoutWorkers      = 16
	fanoutStaleAfter   = 10 * time.Minute
	fanoutJobBatchSize = 10

	// Transient failures (post-service, graph, inbox) retry with backoff:
	// 30 s doubling to 30 min, 12 attempts, about 3.5 h in all (lv-19).
	// Before: every 5 s, 5 attempts, lost after ~30 s.
	fanoutMaxAttempts = 12
	fanoutRetryBase   = 30 * time.Second
	fanoutRetryCap    = 30 * time.Minute

	// A post not visible yet (processing, pending review, scheduled,
	// private for now) is waited for on the same curve, up to about 72 h:
	// long transcodes take hours (lv-1, lv-17). The "post became visible"
	// trigger wakes a waiting job at once, so the curve is only the backstop.
	fanoutMaxWaits = 150

	// Replay of jobs that failed on errors: hourly sweep, after an hour of
	// quiet, at most twice per job, only within three days of the upload.
	fanoutReplayEvery    = time.Hour
	fanoutReplayCooldown = time.Hour
	fanoutReplayWindow   = 72 * time.Hour
	fanoutMaxReplays     = 2
)

// fanoutBackoff is the delay before the n-th retry or wait (n from 1).
func fanoutBackoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	d := fanoutRetryBase
	for i := 1; i < n && d < fanoutRetryCap; i++ {
		d *= 2
	}
	if d > fanoutRetryCap {
		d = fanoutRetryCap
	}
	return d
}

// subscriberSource pages notify-eligible subscriber ids and resolves an
// author to their channel. Served by post-service since the Tube channel
// model moved there; the wire contract is unchanged from user-service.
type subscriberSource interface {
	SubscriberIDs(ctx context.Context, channelID, after uuid.UUID, limit int) (*subscribers.Page, error)
	ChannelByOwner(ctx context.Context, ownerID uuid.UUID) (uuid.UUID, error)
}

// fanoutStore is the slice of the Postgres store the pipeline touches:
// the job table and the per-recipient dedup markers.
type fanoutStore interface {
	EnqueueFanoutJob(ctx context.Context, j *postgres.FanoutJob) error
	ClaimFanoutJobs(ctx context.Context, staleAfter time.Duration, limit int) ([]postgres.FanoutJob, error)
	AdvanceFanoutCursor(ctx context.Context, postID, cursor uuid.UUID, deliveredDelta int64) error
	AdvanceFanoutReminders(ctx context.Context, postID uuid.UUID, cursor string, deliveredDelta int64, done bool) error
	BeginFanoutFollowers(ctx context.Context, postID uuid.UUID) error
	AdvanceFanoutFollowers(ctx context.Context, postID uuid.UUID, cursor string, deliveredDelta int64) error
	CompleteFanoutJob(ctx context.Context, postID uuid.UUID) error
	ReleaseFanoutJob(ctx context.Context, postID uuid.UUID, reason string, retryAt time.Time) error
	DeferFanoutJob(ctx context.Context, postID uuid.UUID, reason string, until time.Time) error
	FailFanoutJob(ctx context.Context, postID uuid.UUID, reason string) error
	ExpireFanoutJob(ctx context.Context, postID uuid.UUID, reason string) error
	RequeueFailedFanoutJobs(ctx context.Context, maxReplays int, cooldown, window time.Duration) (int64, error)
	AlreadyDelivered(ctx context.Context, postID, userID uuid.UUID) (bool, error)
	MarkDelivered(ctx context.Context, postID, userID uuid.UUID) (bool, error)
	CleanupFanoutRecords(ctx context.Context, retention time.Duration) (int64, error)
}

// uploadNotifier is the one delivery call the pipeline makes per
// recipient. The Service implements it; tests substitute a recorder.
type uploadNotifier interface {
	CreateUploadNotification(ctx context.Context, n UploadNotification) error
}

// SubscriberFanout owns the durable upload-notification pipeline.
type SubscriberFanout struct {
	svc  uploadNotifier
	pg   fanoutStore
	subs subscriberSource
	// reminders and names serve "creator is live" jobs only
	// (live_fanout.go): the stream's reminder holders, and the creator's
	// profile name when they have no channel name. Both optional.
	reminders reminderSource
	names     creatorNamer
	// followers pages the creator's followers for the last phase of a live
	// job (live_fanout.go). Optional; upload jobs never use it.
	followers followerSource

	// Per-recipient eligibility (Codex P1-8) and its short-lived
	// per-job post-state cache.
	elig           *eligibilityDeps
	stateMu        sync.Mutex
	stateCache     *postState
	stateCachePost uuid.UUID
	stateCacheAt   time.Time

	// Channel display name for jobs enqueued from events that predate
	// channel_name on the payload. Resolved once per job, cached beside
	// the post state so a retried job does not ask again.
	nameCacheAuthor uuid.UUID
	nameCache       string
	nameCacheAt     time.Time

	// Upload policy flags (members-only, 18+); DefaultUploadPolicy until
	// SetUploadPolicy is called.
	policy    UploadPolicy
	policySet bool

	// now is the clock for backoff deadlines; tests pin it.
	now func() time.Time
}

func (f *SubscriberFanout) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// NewSubscriberFanout wires the production dependencies. A nil pointer
// must not become a non-nil interface holding nil, or every nil guard
// below would pass and the first call would panic; hence the per-field
// assignment.
func NewSubscriberFanout(svc *Service, pg *postgres.Store, subs *subscribers.Client) *SubscriberFanout {
	f := &SubscriberFanout{}
	if svc != nil {
		f.svc = svc
		f.names = svc
	}
	if pg != nil {
		f.pg = pg
	}
	if subs != nil {
		f.subs = subs
	}
	return f
}

// newSubscriberFanoutWith is the test seam: fakes for every dependency
// drive the pipeline without Postgres, Scylla or a subscriber service.
func newSubscriberFanoutWith(svc uploadNotifier, pg fanoutStore, subs subscriberSource) *SubscriberFanout {
	return &SubscriberFanout{svc: svc, pg: pg, subs: subs}
}

// EnqueueParams describes one upload to notify subscribers about.
type EnqueueParams struct {
	PostID      uuid.UUID
	AuthorID    uuid.UUID
	ChannelID   uuid.UUID
	ContentType string
	Visibility  string
	DeepLink    string
	NotifType   string
	CreatedAt   time.Time
	// Title and ChannelName render the push text. Either may be empty on
	// events from older producers; delivery then resolves the channel
	// name once per job and falls back to neutral copy for the title.
	Title       string
	ChannelName string
	// Phase is empty for uploads (subscribers only). A "creator is live"
	// job sets postgres.FanoutPhaseReminders so the stream's reminder
	// holders are paged before the channel's subscribers; PostID is then
	// the stream id and ChannelID may be uuid.Nil.
	Phase string
}

// Enqueue records the fan-out durably. Called synchronously from the
// Kafka consumer so a committed event always has persisted work.
func (f *SubscriberFanout) Enqueue(ctx context.Context, p EnqueueParams) error {
	if f == nil || f.pg == nil {
		return nil
	}
	err := f.pg.EnqueueFanoutJob(ctx, &postgres.FanoutJob{
		PostID:        p.PostID,
		ChannelID:     p.ChannelID,
		AuthorID:      p.AuthorID,
		ContentType:   p.ContentType,
		DeepLink:      p.DeepLink,
		NotifType:     p.NotifType,
		Visibility:    p.Visibility,
		PostCreatedAt: p.CreatedAt,
		Title:         p.Title,
		ChannelName:   p.ChannelName,
		Phase:         p.Phase,
	})
	result := "ok"
	if err != nil {
		result = "error"
	}
	fanoutJobsEnqueued.WithLabelValues(fanoutKind(p.NotifType), result).Inc()
	return err
}

// ResolveChannel returns the author's canonical channel id, or uuid.Nil
// when they have none (⇒ no subscriber fan-out, no fallback).
func (f *SubscriberFanout) ResolveChannel(ctx context.Context, authorID uuid.UUID) uuid.UUID {
	channelID, err := f.LookupChannel(ctx, authorID)
	if err != nil {
		slog.Warn("fanout: channel lookup failed", "author_id", authorID, "error", err)
		return uuid.Nil
	}
	return channelID
}

// LookupChannel is ResolveChannel that reports a failed lookup instead of
// folding it into "no channel".
func (f *SubscriberFanout) LookupChannel(ctx context.Context, authorID uuid.UUID) (uuid.UUID, error) {
	if f == nil || f.subs == nil {
		return uuid.Nil, nil
	}
	return f.subs.ChannelByOwner(ctx, authorID)
}

// StartWorker runs the claim/drain loop until ctx is cancelled. Safe to
// run in every replica: jobs are claimed with FOR UPDATE SKIP LOCKED.
func (f *SubscriberFanout) StartWorker(ctx context.Context) {
	if f == nil || f.pg == nil || f.subs == nil || f.svc == nil {
		slog.Info("fanout: worker not started (missing dependencies)")
		return
	}
	precreateFanoutMetrics()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		gauges := time.NewTicker(fanoutGaugeEvery)
		defer gauges.Stop()
		f.sampleFanoutQueue(ctx)
		cleanup := time.NewTicker(6 * time.Hour)
		defer cleanup.Stop()
		replay := time.NewTicker(fanoutReplayEvery)
		defer replay.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := f.drainOnce(ctx); err != nil {
					slog.Error("fanout: drain error", "error", err)
				}
			case <-replay.C:
				f.replayFailed(ctx)
			case <-gauges.C:
				f.sampleFanoutQueue(ctx)
			case <-cleanup.C:
				if n, err := f.pg.CleanupFanoutRecords(ctx, 7*24*time.Hour); err != nil {
					slog.Error("fanout: cleanup error", "error", err)
				} else if n > 0 {
					slog.Info("fanout: cleaned up completed jobs", "count", n)
				}
			}
		}
	}()
}

func (f *SubscriberFanout) drainOnce(ctx context.Context) error {
	jobs, err := f.pg.ClaimFanoutJobs(ctx, fanoutStaleAfter, fanoutJobBatchSize)
	if err != nil {
		return err
	}
	for i := range jobs {
		job := jobs[i]
		if job.Attempts > fanoutMaxAttempts {
			_ = f.pg.FailFanoutJob(ctx, job.PostID, "exceeded max attempts")
			countFanoutSettled(job.NotifType, fanoutSettledFailed, fanoutReasonAttemptsExhausted)
			slog.Error("fanout: job failed; the hourly replay or a new trigger reopens it",
				"post_id", job.PostID, "attempts", job.Attempts)
			continue
		}
		f.settle(ctx, &job, f.processJob(ctx, &job))
	}
	return nil
}

// settle records the outcome of one pass over a job.
//
//   - done: completed, never run again for this post.
//   - skip (deleted, creator opted out, policy): completed without telling
//     anyone.
//   - wait (post not visible yet): deferred on the backoff curve without
//     deciding anybody and without spending an attempt; expired after
//     about 72 h, and even then a later trigger reopens it.
//   - any other error: released with backoff; failed after the attempts run
//     out, where the replay sweep and any new trigger can reopen it.
func (f *SubscriberFanout) settle(ctx context.Context, job *postgres.FanoutJob, err error) {
	if err == nil {
		_ = f.pg.CompleteFanoutJob(ctx, job.PostID)
		countFanoutSettled(job.NotifType, fanoutSettledCompleted, fanoutReasonNone)
		return
	}
	if s, ok := isSkip(err); ok {
		slog.Info("fanout: upload job ends without notifying", "post_id", job.PostID, "reason", s.reason)
		_ = f.pg.CompleteFanoutJob(ctx, job.PostID)
		countFanoutSettled(job.NotifType, fanoutSettledSkipped, fanoutSkipReason(s.reason))
		return
	}
	if w, ok := isWait(err); ok {
		if job.Waits >= fanoutMaxWaits {
			slog.Warn("fanout: post never became visible; job expired",
				"post_id", job.PostID, "reason", w.reason, "waits", job.Waits)
			_ = f.pg.ExpireFanoutJob(ctx, job.PostID, w.reason)
			countFanoutSettled(job.NotifType, fanoutSettledExpired, fanoutWaitReason(w.reason))
			return
		}
		until := f.clock().Add(fanoutBackoff(job.Waits + 1))
		slog.Info("fanout: post not visible yet; job deferred",
			"post_id", job.PostID, "reason", w.reason, "until", until)
		_ = f.pg.DeferFanoutJob(ctx, job.PostID, w.reason, until)
		countFanoutSettled(job.NotifType, fanoutSettledDeferred, fanoutWaitReason(w.reason))
		return
	}
	retryAt := f.clock().Add(fanoutBackoff(job.Attempts))
	_ = f.pg.ReleaseFanoutJob(ctx, job.PostID, err.Error(), retryAt)
	countFanoutSettled(job.NotifType, fanoutSettledRetried, fanoutReasonError)
	slog.Warn("fanout: job released for retry", "post_id", job.PostID, "retry_at", retryAt, "error", err)
}

// replayFailed is the automatic replay of upload jobs that failed on
// errors. Safe to repeat: delivered recipients carry markers.
func (f *SubscriberFanout) replayFailed(ctx context.Context) {
	n, err := f.pg.RequeueFailedFanoutJobs(ctx, fanoutMaxReplays, fanoutReplayCooldown, fanoutReplayWindow)
	if err != nil {
		fanoutReplayErrors.Inc()
		slog.Error("fanout: replay sweep failed", "error", err)
		return
	}
	fanoutReplayedJobs.Add(float64(n))
	if n > 0 {
		slog.Warn("fanout: failed upload jobs requeued", "count", n)
	}
}

// processJob pages subscribers from the cursor to exhaustion, delivering
// notifications and advancing the cursor after each page. A "creator is
// live" job walks three groups in order, each resumable from its own
// cursor: reminder holders, subscribers, then the creator's followers.
func (f *SubscriberFanout) processJob(ctx context.Context, job *postgres.FanoutJob) error {
	// An upload job first asks whether its post can be announced at all,
	// fresh on every pass: wait and skip end the pass before anyone is
	// decided, so a post still processing is announced once it is ready
	// rather than written off as deleted (lv-1).
	if job.NotifType != LiveNotifType && f.elig != nil && f.elig.postURL != "" {
		f.forgetPostState()
		state, err := f.postStateFor(ctx, job)
		if err != nil {
			return err
		}
		switch state.gate {
		case gateWait:
			return &fanoutWaitError{reason: state.reason}
		case gateSkip:
			return &fanoutSkipError{reason: state.reason}
		}
		if job.Title == "" {
			job.Title = state.title
		}
	}

	// Render input missing from the event (older producer): one lookup
	// for the whole job, never one per recipient.
	if job.ChannelName == "" {
		job.ChannelName = f.channelNameFor(ctx, job)
	}

	if job.NotifType == LiveNotifType {
		// No shelf life: a late or resumed live job still notifies everyone
		// (see live_fanout.go).
		if job.ChannelName == "" {
			job.ChannelName = f.liveCreatorName(ctx, job)
		}
		// Reminder holders first. Once exhausted the phase is persisted as
		// 'subscribers', so a retry of the walk below never re-pages them.
		if job.Phase == postgres.FanoutPhaseReminders {
			if err := f.drainReminders(ctx, job); err != nil {
				return err
			}
		}
	}

	// Subscribers: the whole of an upload job, the middle phase of a live
	// one. A live job already in its follower phase has finished them.
	// No channel ⇒ no subscribers to walk.
	live := job.NotifType == LiveNotifType
	if !live && job.ChannelID == uuid.Nil {
		// An upload job enqueued without its channel: the "post became
		// visible" trigger could not look it up. Resolved here, where a
		// failure is retried with backoff rather than losing the job;
		// a clean "no channel" ends it below with nobody to tell.
		channelID, err := f.subs.ChannelByOwner(ctx, job.AuthorID)
		if err != nil {
			return fmt.Errorf("channel lookup: %w", err)
		}
		job.ChannelID = channelID
	}
	if !live || job.Phase != postgres.FanoutPhaseFollowers {
		if job.ChannelID != uuid.Nil {
			if err := f.drainSubscribers(ctx, job); err != nil {
				return err
			}
		}
	}

	// Followers are told about a live stream only. An upload job ends
	// here whatever its row says: uploads never fall back to followers.
	if !live {
		return nil
	}
	if job.Phase != postgres.FanoutPhaseFollowers {
		// Persisted before the first follower page so a retry re-walks
		// neither the reminder holders nor the subscribers.
		if err := f.pg.BeginFanoutFollowers(ctx, job.PostID); err != nil {
			return fmt.Errorf("begin follower phase: %w", err)
		}
		job.Phase = postgres.FanoutPhaseFollowers
	}
	return f.drainFollowers(ctx, job)
}

// drainSubscribers pages the channel's notify-eligible subscribers from
// the stored cursor to exhaustion.
func (f *SubscriberFanout) drainSubscribers(ctx context.Context, job *postgres.FanoutJob) error {
	after := job.Cursor
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		page, err := f.subs.SubscriberIDs(ctx, job.ChannelID, after, fanoutPageSize)
		if err != nil {
			return fmt.Errorf("subscriber page: %w", err)
		}
		if len(page.IDs) == 0 {
			return nil // drained
		}

		delivered, failed := f.deliverPage(ctx, job, page.IDs)

		// Codex P0-1: the cursor may only move past a page in which every
		// recipient reached a terminal state (delivered, skipped, or
		// ineligible). If ANY recipient failed, we stop here WITHOUT
		// advancing — the job is released and the next attempt re-walks
		// this same page. Re-walking is safe because a delivered marker
		// is now written only after a successful, idempotent inbox write.
		if failed > 0 {
			if err := f.pg.AdvanceFanoutCursor(ctx, job.PostID, after, delivered); err != nil {
				slog.Warn("fanout: cursor persist failed", "post_id", job.PostID, "error", err)
			}
			return fmt.Errorf("%d recipient(s) failed in page; will retry from cursor", failed)
		}

		after = page.NextAfter
		if err := f.pg.AdvanceFanoutCursor(ctx, job.PostID, after, delivered); err != nil {
			// Cursor didn't persist: a retry re-walks the page. Safe —
			// the dedup markers make redelivery a no-op.
			return fmt.Errorf("advance cursor: %w", err)
		}
		if !page.HasMore {
			return nil
		}
	}
}

// deliverPage notifies one page of subscribers in parallel.
// Returns (delivered, failed). A non-zero `failed` blocks cursor
// advancement so no recipient is skipped.
func (f *SubscriberFanout) deliverPage(ctx context.Context, job *postgres.FanoutJob, ids []uuid.UUID) (int64, int64) {
	jobs := make(chan uuid.UUID, fanoutWorkers*2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var delivered, failed int64

	for w := 0; w < fanoutWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for uid := range jobs {
				// Self-exclusion: an author subscribed to their own
				// channel is never notified about their own upload.
				if uid == job.AuthorID {
					countFanoutRecipient(job.NotifType, fanoutRecipientSelf)
					continue
				}

				// Cheap short-circuit: skip recipients already delivered
				// in a previous attempt. This is an optimisation, not the
				// correctness mechanism — the inbox write itself is
				// idempotent, so a stale read here cannot duplicate.
				already, err := f.pg.AlreadyDelivered(ctx, job.PostID, uid)
				if err != nil {
					slog.Warn("fanout: dedup read failed", "user_id", uid, "error", err)
					countFanoutRecipient(job.NotifType, fanoutRecipientFailed)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				if already {
					countFanoutRecipient(job.NotifType, fanoutRecipientAlready)
					continue
				}

				// Per-recipient eligibility (Codex P1-8): blocks,
				// followers-only access, and current post state are
				// re-checked at delivery time, not at enqueue time.
				eligible, err := f.eligible(ctx, job, uid)
				if err != nil {
					slog.Warn("fanout: eligibility check failed", "user_id", uid, "error", err)
					countFanoutRecipient(job.NotifType, fanoutRecipientFailed)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				if !eligible {
					countFanoutRecipient(job.NotifType, fanoutRecipientIneligible)
					// Terminal: record it so we don't re-evaluate this
					// recipient on every retry of the job.
					if _, err := f.pg.MarkDelivered(ctx, job.PostID, uid); err != nil {
						slog.Warn("fanout: ineligible marker failed", "user_id", uid, "error", err)
					}
					continue
				}

				// Idempotent inbox write FIRST. The identity is stable per
				// (post, user, type), so a retry after a partial failure
				// upserts the same row instead of adding a second one.
				identity := fmt.Sprintf("%s:%s:%s", job.PostID, uid, job.NotifType)
				err = f.svc.CreateUploadNotification(ctx, UploadNotification{
					RecipientID: uid,
					AuthorID:    job.AuthorID,
					NotifType:   job.NotifType,
					PostID:      job.PostID,
					ChannelID:   job.ChannelID,
					ChannelName: job.ChannelName,
					Title:       job.Title,
					DeepLink:    job.DeepLink,
					CreatedAt:   job.PostCreatedAt,
					Identity:    identity,
				})
				if err != nil {
					// No marker is written, so this recipient is retried.
					slog.Warn("fanout: notify failed",
						"user_id", uid, "post_id", job.PostID, "error", err)
					countFanoutRecipient(job.NotifType, fanoutRecipientFailed)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}

				// Only now record delivery. If this write fails the
				// recipient is retried and the idempotent insert makes
				// that harmless.
				if _, err := f.pg.MarkDelivered(ctx, job.PostID, uid); err != nil {
					slog.Warn("fanout: delivered marker failed", "user_id", uid, "error", err)
					countFanoutRecipient(job.NotifType, fanoutRecipientFailed)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}

				countFanoutRecipient(job.NotifType, fanoutRecipientNotified)
				mu.Lock()
				delivered++
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	return delivered, failed
}
