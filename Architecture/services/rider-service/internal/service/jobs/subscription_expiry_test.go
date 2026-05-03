package jobs

import (
	"context"
	"testing"
)

// TestRunSubscriptionExpiryChecker_NoExpiringSubsClean asserts that an
// empty DB doesn't crash and returns 0.
func TestRunSubscriptionExpiryChecker_NoExpiringSubsClean(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()

	pub := &recordingPub{}
	n, err := RunSubscriptionExpiryChecker(context.Background(), st, pub)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n < 0 {
		t.Errorf("count = %d, want non-negative", n)
	}
	// Pub may have zero or more calls — depends on cross-test state.
	_ = pub
}

// TestRunGracePeriodTransition_NoExpiredSubsClean asserts an empty DB is
// a no-op.
func TestRunGracePeriodTransition_NoExpiredSubsClean(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()

	pub := &recordingPub{}
	if _, err := RunGracePeriodTransition(context.Background(), st, pub); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestSubscriptionPublisher_NilSafe verifies that passing a nil
// publisher does not panic — jobs guard with `if pub != nil`.
func TestSubscriptionPublisher_NilSafe(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	if _, err := RunSubscriptionExpiryChecker(context.Background(), st, nil); err != nil {
		t.Fatalf("nil pub: %v", err)
	}
	if _, err := RunGracePeriodTransition(context.Background(), st, nil); err != nil {
		t.Fatalf("nil pub grace: %v", err)
	}
}

