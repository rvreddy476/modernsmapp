package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The scheduled composer-draft queue (2026-10-30, tube audit H1 and M10).
//
// 3b489cfb9 stopped scheduled REEL drafts jamming their claim window
// (reel_draft_queue.go); post_drafts had the same jam. The worker claims the
// 50 earliest-due drafts, and a draft CreatePost refused for anything but a
// handful of reasons was released with its schedule_at unchanged, so it was
// first in line on every tick, forever. Fifty such drafts (a long video saved
// with no title, a text over the limit) held the whole window. The same
// pattern now applies here:
//
//   - validateDraft holds a draft to CreatePost's title and text rules when
//     it is saved, so it cannot be scheduled in a shape the publish refuses.
//   - A refusal of the draft itself parks it as 'blocked' with a reason
//     (postDraftBlockReason, the reel queue's list plus a spam rejection);
//     the author edits it, which revives it.
//   - Anything else is transient: the worker returns the draft with a next
//     attempt time that doubles up to an hour (reelDraftRetryDelay) and
//     parks it as publish_failed after maxReelDraftPublishAttempts. A draft
//     waiting on its next attempt is not claimed (migration 080), so it holds
//     no place in the window. That includes a draft whose author's standing
//     is unknown: it used to be claimed and released on every tick.
//   - Each publish runs under its own deadline (scheduledDraftPublishTimeout,
//     M10), so one hung dependency costs one draft a minute, not the tick.

// scheduledDraftPublishTimeout bounds one scheduled draft's publication
// (reel or composer). CreatePost makes several outbound calls (standing,
// media, monetization, copyright, the Action safety check), each with its
// own client timeout; this is the ceiling over all of them, so a dependency
// that hangs stalls one draft for at most this long instead of the whole
// ticker until the 10-minute stale-claim reclaim. A minute is the tick: a
// healthy publish takes well under a second.
const scheduledDraftPublishTimeout = 60 * time.Second

// draftSettleTimeout bounds the bookkeeping write after a publish, on a
// context that survives the publish's own deadline: a draft whose publish
// timed out must still be deferred, not left 'publishing' until the reclaim.
const draftSettleTimeout = 5 * time.Second

// postDraftQueue is the composer queue's store slice (*postgres.Store).
type postDraftQueue interface {
	ClaimDuePostDrafts(ctx context.Context, now time.Time, staleAfter time.Duration, limit int) ([]postgres.PostDraft, error)
	DeferPostDraftPublish(ctx context.Context, id uuid.UUID, token *uuid.UUID, plan postgres.PostDraftRetryPlan) (bool, error)
}

// reelDraftQueue is the reel queue's store slice (*postgres.Store).
type reelDraftQueue interface {
	ClaimDueReelDrafts(ctx context.Context, now time.Time, staleAfter time.Duration, limit int) ([]postgres.ReelDraft, error)
	DeferReelDraftPublish(ctx context.Context, draftID uuid.UUID, plan postgres.ReelDraftRetryPlan) (bool, error)
}

func (s *Service) reelDraftQueue() reelDraftQueue {
	if s.reelDraftQueueSeam != nil {
		return s.reelDraftQueueSeam
	}
	return s.pgStore
}

// draftQueueSeams are the scheduled-draft workers' test seams; all nil in
// production.
type draftQueueSeams struct {
	// postDraftQueueSeam stands in for the Postgres store (nil = pgStore).
	postDraftQueueSeam postDraftQueue
	// publishPostDraftSeam stands in for publishDraftRow on the worker path.
	publishPostDraftSeam func(ctx context.Context, d *postgres.PostDraft) error
	// reelDraftQueueSeam stands in for the Postgres store (nil = pgStore).
	reelDraftQueueSeam reelDraftQueue
	// publishReelDraftSeam stands in for publishClaimedReelDraft.
	publishReelDraftSeam func(ctx context.Context, draftID, authorID uuid.UUID) error
	// draftPublishTimeout overrides scheduledDraftPublishTimeout (0 = it).
	draftPublishTimeout time.Duration
}

func (s *Service) postDraftQueue() postDraftQueue {
	if s.postDraftQueueSeam != nil {
		return s.postDraftQueueSeam
	}
	return s.pgStore
}

func (s *Service) scheduledDraftTimeout() time.Duration {
	if s.draftPublishTimeout > 0 {
		return s.draftPublishTimeout
	}
	return scheduledDraftPublishTimeout
}

// withDraftDeadline runs one scheduled draft's publication under its own
// deadline (M10).
func (s *Service) withDraftDeadline(ctx context.Context, publish func(ctx context.Context) error) error {
	dctx, cancel := context.WithTimeout(ctx, s.scheduledDraftTimeout())
	defer cancel()
	return publish(dctx)
}

// settleContext is the bookkeeping context after a publish attempt: it
// outlives the publish's deadline (and a cancelled tick) for a bounded time.
func settleContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), draftSettleTimeout)
}

// postDraftBlockReason reports whether a failed publication of a claimed
// composer draft is terminal, and the reason it is parked with: the reel
// queue's refusals of the draft itself, plus a spam rejection.
func postDraftBlockReason(err error) (string, bool) {
	if reason, terminal := reelDraftBlockReason(err); terminal {
		return reason, true
	}
	if err != nil && strings.Contains(err.Error(), "content rejected") {
		return "content_rejected", true
	}
	return "", false
}

// PublishScheduledPostDrafts is the worker tick: claim due drafts (SKIP
// LOCKED, multi-replica safe) and publish each under its own deadline. A
// refusal of the draft itself has already parked it (publishDraftRow); any
// other failure returns it with a next attempt time, a no-op for a draft
// that is already parked.
func (s *Service) PublishScheduledPostDrafts(ctx context.Context) (int, error) {
	q := s.postDraftQueue()
	claimed, err := q.ClaimDuePostDrafts(ctx, time.Now().UTC(), staleClaimAfter, 50)
	if err != nil {
		return 0, fmt.Errorf("claim due drafts: %w", err)
	}
	published := 0
	for i := range claimed {
		d := &claimed[i]
		err := s.withDraftDeadline(ctx, func(dctx context.Context) error {
			if s.publishPostDraftSeam != nil {
				return s.publishPostDraftSeam(dctx, d)
			}
			_, perr := s.publishDraftRow(dctx, d, false)
			return perr
		})
		if err == nil {
			published++
			continue
		}
		slog.Warn("scheduled draft publish failed", "draft_id", d.ID, "err", err)
		sctx, cancel := settleContext(ctx)
		parked, derr := q.DeferPostDraftPublish(sctx, d.ID, d.ClaimToken, postgres.PostDraftRetryPlan(reelDraftRetryPlan(time.Now().UTC())))
		cancel()
		if derr != nil {
			// The claim goes stale and is reclaimed (staleClaimAfter).
			slog.Warn("scheduled draft publish: could not defer the draft", "draft_id", d.ID, "err", derr)
		} else if parked {
			slog.Warn("scheduled draft publish: parked after repeated failures",
				"draft_id", d.ID, "attempts", maxReelDraftPublishAttempts, "err", err)
		}
	}
	return published, nil
}

// settleFailedDraftPublish settles a claimed composer draft whose
// publication failed: a refusal of the draft itself parks it with the
// reason; anything else is released for the author to retry (interactive)
// or left claimed for the worker to defer with backoff.
func (s *Service) settleFailedDraftPublish(ctx context.Context, d *postgres.PostDraft, err error, interactive bool) {
	sctx, cancel := settleContext(ctx)
	defer cancel()
	if reason, terminal := postDraftBlockReason(err); terminal {
		s.pgStore.MarkPostDraftBlocked(sctx, d.ID, reason, d.ClaimToken) //nolint:errcheck
		return
	}
	if interactive {
		s.pgStore.ReleasePostDraftClaim(sctx, d.ID, d.ClaimToken) //nolint:errcheck
	}
}
