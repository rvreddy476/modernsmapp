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
//     subscriber notify_on (filtered in SQL by user-service), viewer push
//     preferences, self-exclusion, and per-(post,user) dedup.

const (
	fanoutPageSize     = 500
	fanoutWorkers      = 16
	fanoutStaleAfter   = 10 * time.Minute
	fanoutMaxAttempts  = 5
	fanoutJobBatchSize = 10
)

// SubscriberFanout owns the durable upload-notification pipeline.
type SubscriberFanout struct {
	svc  *Service
	pg   *postgres.Store
	subs *subscribers.Client

	// Per-recipient eligibility (Codex P1-8) and its short-lived
	// per-job post-state cache.
	elig           *eligibilityDeps
	stateMu        sync.Mutex
	stateCache     *postState
	stateCachePost uuid.UUID
	stateCacheAt   time.Time
}

func NewSubscriberFanout(svc *Service, pg *postgres.Store, subs *subscribers.Client) *SubscriberFanout {
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
}

// Enqueue records the fan-out durably. Called synchronously from the
// Kafka consumer so a committed event always has persisted work.
func (f *SubscriberFanout) Enqueue(ctx context.Context, p EnqueueParams) error {
	if f == nil || f.pg == nil {
		return nil
	}
	return f.pg.EnqueueFanoutJob(ctx, &postgres.FanoutJob{
		PostID:        p.PostID,
		ChannelID:     p.ChannelID,
		AuthorID:      p.AuthorID,
		ContentType:   p.ContentType,
		DeepLink:      p.DeepLink,
		NotifType:     p.NotifType,
		Visibility:    p.Visibility,
		PostCreatedAt: p.CreatedAt,
	})
}

// ResolveChannel returns the author's canonical channel id, or uuid.Nil
// when they have none (⇒ no subscriber fan-out, no fallback).
func (f *SubscriberFanout) ResolveChannel(ctx context.Context, authorID uuid.UUID) uuid.UUID {
	if f == nil || f.subs == nil {
		return uuid.Nil
	}
	channelID, err := f.subs.ChannelByOwner(ctx, authorID)
	if err != nil {
		slog.Warn("fanout: channel lookup failed", "author_id", authorID, "error", err)
		return uuid.Nil
	}
	return channelID
}

// StartWorker runs the claim/drain loop until ctx is cancelled. Safe to
// run in every replica: jobs are claimed with FOR UPDATE SKIP LOCKED.
func (f *SubscriberFanout) StartWorker(ctx context.Context) {
	if f == nil || f.pg == nil || f.subs == nil {
		slog.Info("fanout: worker not started (missing dependencies)")
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		cleanup := time.NewTicker(6 * time.Hour)
		defer cleanup.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := f.drainOnce(ctx); err != nil {
					slog.Error("fanout: drain error", "error", err)
				}
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
			slog.Error("fanout: job failed permanently",
				"post_id", job.PostID, "attempts", job.Attempts)
			continue
		}
		if err := f.processJob(ctx, &job); err != nil {
			_ = f.pg.ReleaseFanoutJob(ctx, job.PostID, err.Error())
			slog.Warn("fanout: job released for retry", "post_id", job.PostID, "error", err)
			continue
		}
		_ = f.pg.CompleteFanoutJob(ctx, job.PostID)
	}
	return nil
}

// processJob pages subscribers from the cursor to exhaustion, delivering
// notifications and advancing the cursor after each page.
func (f *SubscriberFanout) processJob(ctx context.Context, job *postgres.FanoutJob) error {
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
					continue
				}

				// Cheap short-circuit: skip recipients already delivered
				// in a previous attempt. This is an optimisation, not the
				// correctness mechanism — the inbox write itself is
				// idempotent, so a stale read here cannot duplicate.
				already, err := f.pg.AlreadyDelivered(ctx, job.PostID, uid)
				if err != nil {
					slog.Warn("fanout: dedup read failed", "user_id", uid, "error", err)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				if already {
					continue
				}

				// Per-recipient eligibility (Codex P1-8): blocks,
				// followers-only access, and current post state are
				// re-checked at delivery time, not at enqueue time.
				eligible, err := f.eligible(ctx, job, uid)
				if err != nil {
					slog.Warn("fanout: eligibility check failed", "user_id", uid, "error", err)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				if !eligible {
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
				if err := f.svc.CreateNotificationIdempotent(ctx, uid, job.AuthorID,
					job.NotifType, "post", job.PostID, job.DeepLink,
					job.PostCreatedAt, identity); err != nil {
					// No marker is written, so this recipient is retried.
					slog.Warn("fanout: notify failed",
						"user_id", uid, "post_id", job.PostID, "error", err)
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
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}

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
