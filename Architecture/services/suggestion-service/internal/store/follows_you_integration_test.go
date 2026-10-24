//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Live-Postgres check for the "Follows you" source (25 Oct 2026). Uses
// pymkPool, so it runs only with -tags integration and a
// SUGGESTION_IT_POSTGRES_DSN whose database name ends in _test.
func TestFollowersNotFollowedBackLive(t *testing.T) {
	pool := pymkPool(t)
	f := &pymkFixture{t: t, pool: pool}
	t.Cleanup(f.cleanup)
	s := New(pool, nil)
	ctx := context.Background()

	followAt := func(a, b uuid.UUID, at time.Time) {
		f.exec(`INSERT INTO follows (follower_id, followee_id, created_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, a, b, at)
	}
	now := time.Now().UTC()

	viewer := f.user()
	older, newer := f.user(), f.user()
	followAt(older, viewer, now.Add(-72*time.Hour))
	followAt(newer, viewer, now.Add(-time.Hour))

	friend := f.user() // followed back
	f.mutual(viewer, friend)

	blockedByViewer, blockedViewer, hidden, excluded := f.user(), f.user(), f.user(), f.user()
	for _, id := range []uuid.UUID{blockedByViewer, blockedViewer, hidden, excluded} {
		followAt(id, viewer, now.Add(-2*time.Hour))
	}
	f.block(viewer, blockedByViewer)
	f.block(blockedViewer, viewer)
	f.hide(hidden)

	followedOnly := f.user() // the viewer follows them; they do not follow back
	f.follow(viewer, followedOnly)

	got, err := s.GetFollowersNotFollowedBack(ctx, viewer, []uuid.UUID{excluded})
	if err != nil {
		t.Fatalf("GetFollowersNotFollowedBack: %v", err)
	}
	if len(got) != 2 || got[0].UserID != newer || got[1].UserID != older {
		t.Fatalf("got %+v, want [newer, older] (newest first, everyone else excluded)", got)
	}
	if got[0].FollowedAt.Before(got[1].FollowedAt) || got[0].FollowedAt.IsZero() {
		t.Fatalf("follow times %v, %v", got[0].FollowedAt, got[1].FollowedAt)
	}

	// A nil exclude list must not empty the result (NULL <> ALL is NULL).
	nilGot, err := s.GetFollowersNotFollowedBack(ctx, viewer, nil)
	if err != nil || len(nilGot) != 3 {
		t.Fatalf("nil excludeIDs: %+v, %v (want newer, excluded, older)", nilGot, err)
	}

	// Following back removes them.
	f.follow(viewer, newer)
	after, err := s.GetFollowersNotFollowedBack(ctx, viewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after {
		if e.UserID == newer {
			t.Fatal("a follower the viewer followed back is still returned")
		}
	}
}
