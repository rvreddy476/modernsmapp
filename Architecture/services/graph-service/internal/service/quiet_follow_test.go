package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/graph-service/internal/ratelimit"
	"github.com/atpost/graph-service/internal/store"
	"github.com/google/uuid"
)

// Security sweep #10: a follow across a block used to answer
// 500 INTERNAL_ERROR "cannot follow: blocked", so the blocked party learned
// of the block from the error string. It now answers exactly what a follow
// of a private account answers — "requested" — and nothing else changes.
func TestFollowAcrossABlockAnswersRequested(t *testing.T) {
	for _, wrapped := range []error{
		store.ErrBlockedPair,
		fmt.Errorf("follow: %w", store.ErrBlockedPair),
		fmt.Errorf("follow request: %w", store.ErrBlockedPair),
	} {
		status, err := quietFollowOutcome("", wrapped)
		if err != nil || status != FollowStatusRequested {
			t.Fatalf("%v → (%q, %v), want (%q, nil)", wrapped, status, err, FollowStatusRequested)
		}
	}
	// The word must not survive anywhere a handler would print it.
	if _, err := quietFollowOutcome("", fmt.Errorf("x: %w", store.ErrBlockedPair)); err != nil && strings.Contains(err.Error(), "block") {
		t.Fatalf("the refusal still names the block: %v", err)
	}
}

// Every other outcome passes through untouched: the mapping is for the
// block only.
func TestQuietFollowOutcomeLeavesOtherResultsAlone(t *testing.T) {
	if s, err := quietFollowOutcome(FollowStatusFollowed, nil); err != nil || s != FollowStatusFollowed {
		t.Fatalf("followed → (%q, %v)", s, err)
	}
	if s, err := quietFollowOutcome(RequestFollowStatusFollowing, nil); err != nil || s != RequestFollowStatusFollowing {
		t.Fatalf("following → (%q, %v)", s, err)
	}
	for _, in := range []error{ErrRateLimited, ErrWrongEntityType, ErrCannotFollowSelf, errors.New("db down")} {
		if _, err := quietFollowOutcome("", in); !errors.Is(err, in) {
			t.Fatalf("%v was rewritten to %v", in, err)
		}
	}
}

// Security sweep #22: block, unblock, mute and unmute ask the per-action
// limiter FIRST. The store is nil here, so a write that got past the
// limiter would panic: the refusal proves the order.
type refuseAll struct{ asked []string }

func (r *refuseAll) Allow(_ context.Context, action string, _ uuid.UUID) (bool, error) {
	r.asked = append(r.asked, action)
	return false, nil
}

func TestBlockAndMuteAreRateLimited(t *testing.T) {
	lim := &refuseAll{}
	s := &Service{rateLimit: lim}
	a, b := uuid.New(), uuid.New()
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"block":   func() error { return s.Block(ctx, a, b) },
		"unblock": func() error { return s.Unblock(ctx, a, b) },
		"mute":    func() error { return s.Mute(ctx, a, b) },
		"unmute":  func() error { return s.Unmute(ctx, a, b) },
	} {
		if err := call(); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("%s over quota: %v, want ErrRateLimited", name, err)
		}
	}
	want := map[string]int{ratelimit.ActionBlock: 2, ratelimit.ActionMute: 2}
	got := map[string]int{}
	for _, a := range lim.asked {
		got[a]++
	}
	for action, n := range want {
		if got[action] != n {
			t.Fatalf("limiter asked about %q %d time(s), want %d (asked: %v)", action, got[action], n, lim.asked)
		}
	}
	for _, action := range []string{ratelimit.ActionBlock, ratelimit.ActionMute} {
		if ratelimit.Limit(action) <= 0 {
			t.Fatalf("%q has no quota: the limiter would allow it unconditionally", action)
		}
	}
}
