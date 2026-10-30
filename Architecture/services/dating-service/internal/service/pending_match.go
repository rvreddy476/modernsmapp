// Mutual sparks whose match formation failed are retried (audit 2026-10-30
// L2).
//
// createSpark writes a dating_pending_matches marker for the pair before it
// forms the match and clears it once the match exists. If formation fails
// before CreateOrGetOpenMatch inserts the row (a transient error in a profile
// lookup, the database blipping), the saga reconciler has nothing to retry;
// the marker is what RetryPendingMatches picks up. A failure after the row
// exists (chat-service down) is the saga reconciler's: the retry here then
// finds the open match and clears the marker.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

const (
	// pendingMatchFirstDelay keeps the retrier off a pair the live request
	// is still forming.
	pendingMatchFirstDelay = 30 * time.Second
	// pendingMatchLease is how long a claimed marker is hidden from other
	// replicas while one retries it.
	pendingMatchLease = 2 * time.Minute
	// pendingMatchMaxAttempts: after this many failures the marker is
	// dead-lettered (failed_at) for an operator instead of retried forever.
	pendingMatchMaxAttempts = 50
	pendingMatchBatch       = 100
	pendingMatchInterval    = time.Minute
)

// pendingMatchBackoff is 30 s doubled per failure, capped at an hour.
func pendingMatchBackoff(attempts int) time.Duration {
	d := 30 * time.Second
	for i := 0; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// pendingMatchErrorIsFinal reports whether a FormMatch error means the pair
// can no longer match (blocked, a profile held, gone or not onboarded), so
// the marker is dropped rather than retried. Anything else is transient.
func pendingMatchErrorIsFinal(err error) bool {
	var onboarding *OnboardingIncompleteError
	switch {
	case errors.Is(err, ErrCandidateUnavailable),
		errors.Is(err, store.ErrMatchPairBlocked),
		errors.Is(err, ErrProfilePendingReview),
		errors.Is(err, ErrProfileRestricted),
		errors.Is(err, ErrProfileSuspended),
		errors.As(err, &onboarding):
		return true
	}
	return false
}

// recordPendingMatch writes the pair's marker. Best effort: a failure is
// logged and the live attempt still runs.
func (s *Service) recordPendingMatch(ctx context.Context, x, y uuid.UUID, target map[string]any) {
	if err := s.store.RecordPendingMatch(ctx, x, y, target, pendingMatchFirstDelay); err != nil {
		slog.Error("pending match marker not written; a failed match formation will not be retried",
			"user_a", x, "user_b", y, "error", err)
	}
}

func (s *Service) clearPendingMatch(ctx context.Context, x, y uuid.UUID) {
	if err := s.store.ClearPendingMatch(ctx, x, y); err != nil {
		// The retrier will find the open match and clear it.
		slog.Warn("pending match marker not cleared", "user_a", x, "user_b", y, "error", err)
	}
}

// RetryPendingMatches runs one pass over due markers and returns how many
// pairs it matched. Safe on several replicas at once.
func (s *Service) RetryPendingMatches(ctx context.Context) (int, error) {
	due, err := s.store.ClaimDuePendingMatches(ctx, pendingMatchBatch, pendingMatchLease)
	if err != nil {
		return 0, err
	}
	matched := 0
	for _, pm := range due {
		ok, err := s.retryPendingMatch(ctx, pm)
		if err != nil {
			slog.Warn("pending match retry failed", "user_a", pm.UserA, "user_b", pm.UserB,
				"attempts", pm.Attempts+1, "error", err)
			giveUp := pm.Attempts+1 >= pendingMatchMaxAttempts
			if rerr := s.store.RecordPendingMatchFailure(ctx, pm.UserA, pm.UserB, err.Error(),
				pendingMatchBackoff(pm.Attempts), giveUp); rerr != nil {
				slog.Warn("pending match failure not recorded; the lease retries it", "error", rerr)
			}
			continue
		}
		if ok {
			matched++
		}
	}
	return matched, nil
}

// retryPendingMatch returns (true, nil) when it formed the match, (false,
// nil) when the marker was dropped, and an error to retry later.
func (s *Service) retryPendingMatch(ctx context.Context, pm *store.PendingMatch) (bool, error) {
	// Still mutual both ways (since the last closure, not declined)?
	ab, err := s.store.HasReverseSparks(ctx, pm.UserA, pm.UserB)
	if err != nil {
		return false, err
	}
	ba, err := s.store.HasReverseSparks(ctx, pm.UserB, pm.UserA)
	if err != nil {
		return false, err
	}
	if !ab || !ba {
		return false, s.store.ClearPendingMatch(ctx, pm.UserA, pm.UserB)
	}
	// Already matched (the live request formed it and only the clear
	// failed, or the saga reconciler owns a half-formed one): nothing to do.
	if open, err := s.store.HasOpenMatch(ctx, pm.UserA, pm.UserB); err != nil {
		return false, err
	} else if open {
		return false, s.store.ClearPendingMatch(ctx, pm.UserA, pm.UserB)
	}
	match, err := s.FormMatch(ctx, pm.UserA, pm.UserB, pm.SparkTarget)
	if err != nil {
		if pendingMatchErrorIsFinal(err) {
			slog.Info("pending match dropped: the pair can no longer match", "user_a", pm.UserA, "user_b", pm.UserB, "error", err)
			return false, s.store.ClearPendingMatch(ctx, pm.UserA, pm.UserB)
		}
		return false, err
	}
	if err := s.store.ClearPendingMatch(ctx, pm.UserA, pm.UserB); err != nil {
		return false, err
	}
	// The pair had no open match a moment ago, so this pass formed it
	// (FormMatch emitted match.formed); spark.matched as the live path does.
	if s.producer != nil {
		_ = s.producer.PublishSparkMatched(ctx, match.ID, match.UserA, match.UserB)
	}
	slog.Info("pending match formed by retry", "match_id", match.ID, "attempts", pm.Attempts+1)
	return true, nil
}

// StartPendingMatchRetrier runs RetryPendingMatches every minute until ctx
// is cancelled. Call in a goroutine.
func (s *Service) StartPendingMatchRetrier(ctx context.Context) {
	ticker := time.NewTicker(pendingMatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.RetryPendingMatches(ctx); err != nil {
				slog.Warn("pending match retrier pass failed", "error", err)
			} else if n > 0 {
				slog.Info("pending match retrier pass", "matched", n)
			}
		}
	}
}
