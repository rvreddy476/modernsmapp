package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
)

// Audit 2026-10-30 L2: the retrier drops a pair that can no longer match and
// retries everything else.
func TestPendingMatchErrorIsFinal(t *testing.T) {
	t.Parallel()
	final := []error{
		ErrCandidateUnavailable,
		fmt.Errorf("wrapped: %w", store.ErrMatchPairBlocked),
		ErrProfilePendingReview,
		ErrProfileRestricted,
		ErrProfileSuspended,
		&OnboardingIncompleteError{ProfileStatus: "draft", Step: "draft"},
	}
	for _, err := range final {
		if !pendingMatchErrorIsFinal(err) {
			t.Errorf("%v: want final (marker dropped)", err)
		}
	}
	for _, err := range []error{
		errors.New("conn reset"),
		fmt.Errorf("create conversation: %w", errors.New("503")),
	} {
		if pendingMatchErrorIsFinal(err) {
			t.Errorf("%v: transient error treated as final; the mutual spark would be lost", err)
		}
	}
}

func TestPendingMatchBackoff(t *testing.T) {
	t.Parallel()
	if got := pendingMatchBackoff(0); got != 30*time.Second {
		t.Fatalf("first backoff = %v", got)
	}
	if got := pendingMatchBackoff(1); got != time.Minute {
		t.Fatalf("second backoff = %v", got)
	}
	if got := pendingMatchBackoff(100); got != time.Hour {
		t.Fatalf("capped backoff = %v", got)
	}
	if pendingMatchMaxAttempts <= 0 || pendingMatchFirstDelay <= 0 || pendingMatchLease <= 0 {
		t.Fatal("retrier constants must be positive")
	}
}
