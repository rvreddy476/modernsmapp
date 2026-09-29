package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/standing"
	"github.com/google/uuid"
)

// Author standing at publication (Copyright Match plan section 6.4, P-5).
//
// ONE choke point, requirePublishStanding, runs on every path that makes
// content live on an author's behalf: CreatePost (every type), CreateThread,
// the composer and reel draft publishes (interactive and the workers), the
// scheduled-post flip (worker, "publish now" and PublishVideo), CreateRepost,
// CreateCrosspost, CreateStoryPending and CreateLiveVODPost. The guard test
// (publish_standing_guard_test.go) fails when a publication entry point or
// a store sink appears without it. Visibility widening (PATCH visibility)
// is NOT publication (founder default F-8) and is not gated here.
//
// trust-safety alone decides. This file never re-derives policy from
// severities: "ok" allows, anything the client reports as denied refuses
// with AuthorSuspendedError (403 AUTHOR_SUSPENDED on the wire, with
// suspended_until), and unknown — trust-safety down, a bad credential, an
// answer outside the contract, or no client configured at all — fails
// closed with ErrStandingUnknown (503 STANDING_UNAVAILABLE; founder
// default F-7).
//
// Background paths (the draft and schedule workers, the live-VOD consumer)
// go through backgroundPublishStanding instead: a deny parks the item as
// blocked with a reason the author's UI can show, and an unknown is retried
// with per-item exponential backoff (1 min doubling to 1 h) until it has
// been unknown for standingBlockAfter (24 h), when the item is parked as
// blocked with reason standing_unavailable rather than retried forever.

// StandingChecker is what the service needs from the standing client.
type StandingChecker interface {
	Check(ctx context.Context, userID uuid.UUID) (*standing.Result, error)
}

// ErrStandingUnknown means the authoritative standing could not be read.
// Publication must NOT proceed through that uncertainty: interactive
// callers answer 503 STANDING_UNAVAILABLE, background callers retry with
// backoff.
var ErrStandingUnknown = errors.New("author standing unavailable")

// ErrAuthorSuspended is the errors.Is target for every *AuthorSuspendedError.
var ErrAuthorSuspended = errors.New("author may not publish")

// AuthorSuspendedError: trust-safety refuses publication for this author.
type AuthorSuspendedError struct {
	AuthorID       uuid.UUID
	Standing       string     // "suspended" or "restricted"
	SuspendedUntil *time.Time // nil when trust-safety names no end
	PolicyVersion  string
}

func (e *AuthorSuspendedError) Error() string {
	if e.SuspendedUntil != nil {
		return fmt.Sprintf("author may not publish: %s until %s", e.Standing, e.SuspendedUntil.UTC().Format(time.RFC3339))
	}
	return "author may not publish: " + e.Standing
}

// Is makes errors.Is(err, ErrAuthorSuspended) hold.
func (e *AuthorSuspendedError) Is(target error) bool { return target == ErrAuthorSuspended }

// BlockReason renders the reason a parked draft or scheduled post carries.
func (e *AuthorSuspendedError) BlockReason() string {
	if e.SuspendedUntil != nil {
		return BlockReasonAuthorSuspended + " until " + e.SuspendedUntil.UTC().Format(time.RFC3339)
	}
	return BlockReasonAuthorSuspended
}

// Block reasons written on parked drafts and scheduled posts. The UI keys
// on the prefix.
const (
	BlockReasonAuthorSuspended     = "author_suspended"
	BlockReasonStandingUnavailable = "standing_unavailable"
)

// Backoff for background paths (plan 6.4: "per-item exponential backoff;
// after 24 h of continuous unknown, the item moves to blocked").
const (
	standingBackoffBase = time.Minute
	standingBackoffMax  = time.Hour
	standingBlockAfter  = 24 * time.Hour
)

// SetStandingChecker wires the trust-safety client. nil means unconfigured:
// every publication fails closed unless SetRequireStandingCheck(false) has
// opted a dev stack out.
func (s *Service) SetStandingChecker(c StandingChecker) { s.standing = c }

// SetRequireStandingCheck controls the ONE deliberate opt-out: a dev stack
// without a signing key. Default true (main.go); false lets publication
// through only when no checker is wired at all — a wired checker is always
// consulted.
func (s *Service) SetRequireStandingCheck(v bool) { s.requireStandingCheck = v }

// StandingConfigured reports whether a checker is wired (boot logging).
func (s *Service) StandingConfigured() bool { return s.standing != nil }

// requirePublishStanding is the choke point for interactive paths.
func (s *Service) requirePublishStanding(ctx context.Context, authorID uuid.UUID) error {
	if s.standing == nil {
		if s.requireStandingCheck {
			return fmt.Errorf("%w: no trust-safety standing client is configured", ErrStandingUnknown)
		}
		return nil // dev: explicitly opted out
	}
	if authorID == uuid.Nil {
		return fmt.Errorf("%w: no author", ErrStandingUnknown)
	}
	_, err := s.standing.Check(ctx, authorID)
	if err == nil {
		return nil
	}
	var denied *standing.DeniedError
	if errors.As(err, &denied) {
		return &AuthorSuspendedError{
			AuthorID:       authorID,
			Standing:       denied.Standing,
			SuspendedUntil: denied.SuspendedUntil,
			PolicyVersion:  denied.PolicyVersion,
		}
	}
	return fmt.Errorf("%w: %v", ErrStandingUnknown, err)
}

// backgroundVerdict is what a background path does next.
type backgroundVerdict int

const (
	// standingProceed: publish.
	standingProceed backgroundVerdict = iota
	// standingRetryLater: leave the item for a later tick (err is
	// ErrStandingUnknown, or nil when the item is inside its backoff
	// window and trust-safety was not even asked).
	standingRetryLater
	// standingBlock: park the item with reason; err says why.
	standingBlock
)

// backgroundPublishStanding is the choke point for background paths. key
// identifies the item across ticks ("post_draft:<id>" …).
func (s *Service) backgroundPublishStanding(ctx context.Context, key string, authorID uuid.UUID) (backgroundVerdict, string, error) {
	b := s.standingBackoffState()
	if !b.due(key) {
		return standingRetryLater, "", fmt.Errorf("%w: %s is backing off", ErrStandingUnknown, key)
	}
	err := s.requirePublishStanding(ctx, authorID)
	switch {
	case err == nil:
		b.clear(key)
		return standingProceed, "", nil
	case errors.Is(err, ErrAuthorSuspended):
		b.clear(key)
		var suspended *AuthorSuspendedError
		reason := BlockReasonAuthorSuspended
		if errors.As(err, &suspended) {
			reason = suspended.BlockReason()
		}
		return standingBlock, reason, err
	default:
		if giveUp, next := b.recordUnknown(key); giveUp {
			slog.Warn("publish standing: unknown for too long; parking the item",
				"key", key, "author_id", authorID, "since", standingBlockAfter)
			b.clear(key)
			return standingBlock, BlockReasonStandingUnavailable, err
		} else {
			slog.Warn("publish standing: unknown; will retry", "key", key, "author_id", authorID, "next_attempt", next)
		}
		return standingRetryLater, "", err
	}
}

// standingBackoff is the per-item retry state. It is in memory: a restart
// forgets it, which only means an item is retried sooner, never published.
type standingBackoff struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[string]*backoffItem
}

type backoffItem struct {
	firstUnknown time.Time
	attempts     int
	nextAttempt  time.Time
}

func newStandingBackoff(now func() time.Time) *standingBackoff {
	if now == nil {
		now = time.Now
	}
	return &standingBackoff{now: now, items: map[string]*backoffItem{}}
}

func (s *Service) standingBackoffState() *standingBackoff {
	s.standingBackoffOnce.Do(func() {
		if s.standingBackoff == nil {
			s.standingBackoff = newStandingBackoff(nil)
		}
	})
	return s.standingBackoff
}

// due reports whether the item may be attempted now.
func (b *standingBackoff) due(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	it := b.items[key]
	return it == nil || !b.now().Before(it.nextAttempt)
}

// recordUnknown notes one unknown answer and schedules the next attempt:
// base × 2^(attempts-1), capped at max. giveUp is true once the item has
// been continuously unknown for standingBlockAfter.
func (b *standingBackoff) recordUnknown(key string) (giveUp bool, next time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	it := b.items[key]
	if it == nil {
		it = &backoffItem{firstUnknown: now}
		b.items[key] = it
	}
	it.attempts++
	delay := standingBackoffBase
	for i := 1; i < it.attempts && delay < standingBackoffMax; i++ {
		delay *= 2
	}
	if delay > standingBackoffMax {
		delay = standingBackoffMax
	}
	it.nextAttempt = now.Add(delay)
	if now.Sub(it.firstUnknown) >= standingBlockAfter {
		return true, it.nextAttempt
	}
	return false, it.nextAttempt
}

// clear forgets an item (published, parked, or answered).
func (b *standingBackoff) clear(key string) {
	b.mu.Lock()
	delete(b.items, key)
	b.mu.Unlock()
}

// Background item keys.
func standingKeyPostDraft(id uuid.UUID) string     { return "post_draft:" + id.String() }
func standingKeyReelDraft(id uuid.UUID) string     { return "reel_draft:" + id.String() }
func standingKeyScheduledPost(id uuid.UUID) string { return "scheduled_post:" + id.String() }
func standingKeyLiveStream(id uuid.UUID) string    { return "live_stream:" + id.String() }
