//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Live-Postgres checks for the 24 Oct 2026 follow-graph queries:
// CheckFollowSecondDegree, MutualFollowCounts and the hidden-account filter
// on GetConnections. Run with -tags integration and POSTGRES_DSN set.

func followAudiencePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := graphPool(t)
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS hidden_users (
			user_id   UUID PRIMARY KEY,
			reason    TEXT        NOT NULL DEFAULT '',
			hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		t.Fatalf("hidden_users: %v", err)
	}
	return pool
}

func follow(t *testing.T, pool *pgxpool.Pool, follower, followee uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO follows (follower_id, followee_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`, follower, followee); err != nil {
		t.Fatalf("follow: %v", err)
	}
}

func hide(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO hidden_users (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatalf("hide: %v", err)
	}
}

func TestCheckFollowSecondDegreeLive(t *testing.T) {
	pool := followAudiencePool(t)
	s := New(pool)
	ctx := context.Background()
	actor, target, both, onlyActor, hidden := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	got, err := s.CheckFollowSecondDegree(ctx, actor, target)
	if err != nil || got {
		t.Fatalf("no shared follower = %v, %v; want false", got, err)
	}
	follow(t, pool, onlyActor, actor)
	if got, _ := s.CheckFollowSecondDegree(ctx, actor, target); got {
		t.Fatal("a follower of the actor alone made the pair second degree")
	}
	// The target following the actor is not a THIRD party.
	follow(t, pool, target, actor)
	if got, _ := s.CheckFollowSecondDegree(ctx, actor, target); got {
		t.Fatal("the target itself counted as the shared follower")
	}
	// A hidden shared follower does not count.
	follow(t, pool, hidden, actor)
	follow(t, pool, hidden, target)
	hide(t, pool, hidden)
	if got, _ := s.CheckFollowSecondDegree(ctx, actor, target); got {
		t.Fatal("a hidden account counted as the shared follower")
	}
	follow(t, pool, both, actor)
	follow(t, pool, both, target)
	if got, err := s.CheckFollowSecondDegree(ctx, actor, target); err != nil || !got {
		t.Fatalf("someone following both = %v, %v; want true", got, err)
	}
}

func TestMutualFollowCountsLive(t *testing.T) {
	pool := followAudiencePool(t)
	s := New(pool)
	ctx := context.Background()
	viewer, target, other := uuid.New(), uuid.New(), uuid.New()
	friendOfBoth, oneWay, hiddenFriend := uuid.New(), uuid.New(), uuid.New()

	mutual := func(a, b uuid.UUID) { follow(t, pool, a, b); follow(t, pool, b, a) }
	mutual(viewer, friendOfBoth)
	mutual(target, friendOfBoth)
	// oneWay is the viewer's friend but only follows the target (no follow
	// back): not a friend of the target.
	mutual(viewer, oneWay)
	follow(t, pool, oneWay, target)
	mutual(viewer, hiddenFriend)
	mutual(target, hiddenFriend)
	hide(t, pool, hiddenFriend)
	// The viewer and target being friends is not a mutual friend.
	mutual(viewer, target)

	counts, err := s.MutualFollowCounts(ctx, viewer, []uuid.UUID{target, target, other, viewer})
	if err != nil {
		t.Fatal(err)
	}
	if counts[target] != 1 {
		t.Fatalf("target count %d, want 1 (friendOfBoth only): %v", counts[target], counts)
	}
	if _, ok := counts[other]; ok {
		t.Fatalf("a stranger got a count: %v", counts)
	}
	if _, ok := counts[viewer]; ok {
		t.Fatalf("the viewer counted against themselves: %v", counts)
	}
}

func TestGetConnectionsOmitsHiddenAccountsLive(t *testing.T) {
	pool := followAudiencePool(t)
	s := New(pool)
	ctx := context.Background()
	owner, visible, hidden := uuid.New(), uuid.New(), uuid.New()
	for _, other := range []uuid.UUID{visible, hidden} {
		a, b := normalizePair(owner, other)
		if _, err := pool.Exec(ctx, `INSERT INTO connections (user_a, user_b, created_at) VALUES ($1, $2, NOW())`, a, b); err != nil {
			t.Fatalf("connection: %v", err)
		}
	}
	hide(t, pool, hidden)
	ids, err := s.GetConnections(ctx, owner, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != visible {
		t.Fatalf("connections %v, want only %s", ids, visible)
	}
}
