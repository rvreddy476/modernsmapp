//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Live-Postgres checks for the readers moved off the retired connections
// table on 25 Oct 2026. Same rules as pymk_follow_graph_integration_test.go
// (whose pymkPool / pymkFixture this reuses): SUGGESTION_IT_POSTGRES_DSN must
// point at a SCRATCH database; rows are keyed by fresh uuids and removed.

func TestConnectionsRetiredReadersLive(t *testing.T) {
	pool := pymkPool(t)
	f := &pymkFixture{t: t, pool: pool}
	t.Cleanup(f.cleanup)
	s := New(pool, nil)
	ctx := context.Background()

	viewer := f.user()
	b, c := f.user(), f.user()
	f.mutual(viewer, b)
	f.mutual(viewer, c)
	f.mutual(b, c)

	hiddenFriend := f.user()
	f.mutual(viewer, hiddenFriend)
	f.hide(hiddenFriend)

	oneWayFollower := f.user() // follows the viewer, not followed back
	f.follow(oneWayFollower, viewer)
	oneWayFollowee := f.user() // the viewer follows, no follow back
	f.follow(viewer, oneWayFollowee)

	t.Run("GetFriendIDs", func(t *testing.T) {
		ids, err := s.GetFriendIDs(ctx, viewer)
		if err != nil {
			t.Fatal(err)
		}
		got := map[uuid.UUID]bool{}
		for _, id := range ids {
			got[id] = true
		}
		if len(ids) != 3 || !got[b] || !got[c] || !got[hiddenFriend] {
			t.Fatalf("friends %v, want b, c and the hidden friend (it is an exclusion list)", ids)
		}
	})

	t.Run("GetTriadicClosureCandidates", func(t *testing.T) {
		d := f.user() // friend of both b and c: closes the b–c triangle
		f.mutual(b, d)
		f.mutual(c, d)

		onlyB := f.user() // friend of b only: closes nothing
		f.mutual(b, onlyB)

		fan := f.user() // follows b and c, neither follows back
		f.follow(fan, b)
		f.follow(fan, c)

		followed, hidden, blockedByViewer, blockedViewer, excluded := f.user(), f.user(), f.user(), f.user(), f.user()
		for _, x := range []uuid.UUID{followed, hidden, blockedByViewer, blockedViewer, excluded} {
			f.mutual(b, x)
			f.mutual(c, x)
		}
		f.follow(viewer, followed)
		f.hide(hidden)
		f.block(viewer, blockedByViewer)
		f.block(blockedViewer, viewer)

		got, err := s.GetTriadicClosureCandidates(ctx, viewer, []uuid.UUID{excluded}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if got[d] != 1 {
			t.Fatalf("d closes one triangle, counted %d (%v)", got[d], got)
		}
		for name, id := range map[string]uuid.UUID{
			"viewer": viewer, "b": b, "c": c, "friend of one friend": onlyB, "one-way fan": fan,
			"already followed": followed, "hidden": hidden, "blocked by viewer": blockedByViewer,
			"blocked the viewer": blockedViewer, "excludeIDs": excluded,
		} {
			if _, ok := got[id]; ok {
				t.Errorf("%s was a triadic candidate", name)
			}
		}
		if nilGot, err := s.GetTriadicClosureCandidates(ctx, viewer, nil, 100); err != nil || nilGot[d] != 1 {
			t.Fatalf("nil excludeIDs: %v, %v", nilGot, err)
		}
	})

	t.Run("GetSocialProofCandidates", func(t *testing.T) {
		both := f.user() // followed one-way by b and c
		f.follow(b, both)
		f.follow(c, both)

		viaFan := f.user() // followed only by a one-way follower of the viewer
		f.follow(oneWayFollower, viaFan)

		viaHidden := f.user()
		f.follow(hiddenFriend, viaHidden)

		followed, hidden, blockedByViewer, blockedViewer := f.user(), f.user(), f.user(), f.user()
		for _, x := range []uuid.UUID{followed, hidden, blockedByViewer, blockedViewer} {
			f.follow(b, x)
		}
		f.follow(viewer, followed)
		f.hide(hidden)
		f.block(viewer, blockedByViewer)
		f.block(blockedViewer, viewer)

		got, err := s.GetSocialProofCandidates(ctx, viewer, 200)
		if err != nil {
			t.Fatal(err)
		}
		if got[both] != 2 {
			t.Fatalf("followed by two friends, counted %d (%v)", got[both], got)
		}
		for name, id := range map[string]uuid.UUID{
			"viewer": viewer, "friend b": b, "friend c": c, "via a one-way follower": viaFan,
			"via a hidden friend": viaHidden, "already followed": followed, "hidden": hidden,
			"blocked by viewer": blockedByViewer, "blocked the viewer": blockedViewer,
		} {
			if _, ok := got[id]; ok {
				t.Errorf("%s was a social-proof candidate", name)
			}
		}
	})

	t.Run("GetBlockCountByFriends", func(t *testing.T) {
		target, clean := f.user(), f.user()
		f.block(b, target)
		f.block(c, target)
		f.block(oneWayFollower, target) // not a friend
		f.block(oneWayFollowee, target) // not a friend
		f.block(hiddenFriend, target)   // hidden friends bridge nothing

		n, err := s.GetBlockCountByFriends(ctx, viewer, target)
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("friends who blocked the target: %d, want 2", n)
		}
		batch, err := s.GetBlockCountByFriendsBatch(ctx, viewer, []uuid.UUID{target, clean})
		if err != nil {
			t.Fatal(err)
		}
		if batch[target] != 2 {
			t.Fatalf("batch count %d, want 2 (%v)", batch[target], batch)
		}
		if _, ok := batch[clean]; ok {
			t.Fatalf("a candidate no friend blocked has a count: %v", batch)
		}
	})
}
