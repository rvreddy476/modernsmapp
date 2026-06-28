//go:build integration

package integration

import (
	"testing"
	"time"
)

// Eventually polls fn until it returns true or timeout elapses, then fails the
// test. Use it for Kafka-driven side effects (feed fan-out, view counts,
// notifications, search indexing, cache invalidation) where the result is
// eventually consistent — read-once assertions would flake.
//
//	Eventually(t, 15*time.Second, "post should fan out to follower feed", func() bool {
//	    return feedContains(t, ctx, follower, postID)
//	})
func Eventually(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Eventually: %q not satisfied within %s", what, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
