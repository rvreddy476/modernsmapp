package service

import (
	"testing"
	"time"
)

// Food server lane (9 Nov 2026): the kitchen's new-order push names its
// accept deadline, in RFC 3339 UTC, under the key the Kitchen app reads
// (feature/kitchen NewOrderPush: accept_deadline_at, else expires_at).
func TestKitchenNewOrderDataCarriesTheAcceptDeadline(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	deadline := time.Date(2026, 11, 9, 15, 33, 0, 0, ist)
	p := kitchenPush()
	p.AcceptDeadlineAt = &deadline
	data := FoodPushData(p)
	if got := data["accept_deadline_at"]; got != "2026-11-09T10:03:00Z" {
		t.Fatalf("accept_deadline_at = %q, want 2026-11-09T10:03:00Z", got)
	}
	if !FoodPushDataKeys["accept_deadline_at"] {
		t.Fatal("accept_deadline_at is not allowlisted")
	}

	// A legacy event without a deadline names none (the app falls back to the
	// default window), and no other food push ever carries the key.
	if _, has := FoodPushData(kitchenPush())["accept_deadline_at"]; has {
		t.Fatal("a push without a deadline invented one")
	}
	for _, other := range []FoodPush{riderPush(), customerPush()} {
		other.AcceptDeadlineAt = &deadline
		if _, has := FoodPushData(other)["accept_deadline_at"]; has {
			t.Fatalf("%s carries accept_deadline_at", other.Type)
		}
	}
}

// The partner queue's android ttl is the whole seconds left before the offer
// or accept deadline, and nothing is sent with under a second left.
func TestRemainingAndroidTTL(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		left time.Duration
		ttl  string
		live bool
	}{
		{20 * time.Second, "20s", true},
		{19*time.Second + 900*time.Millisecond, "19s", true},
		{time.Second, "1s", true},
		{999 * time.Millisecond, "", false},
		{-time.Second, "", false},
	} {
		ttl, live := remainingAndroidTTL(now.Add(tc.left), now)
		if ttl != tc.ttl || live != tc.live {
			t.Fatalf("left %v: ttl=%q live=%v, want %q %v", tc.left, ttl, live, tc.ttl, tc.live)
		}
	}
}
