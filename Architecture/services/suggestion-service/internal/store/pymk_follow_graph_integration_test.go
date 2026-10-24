//go:build integration

package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Live-Postgres checks for the follow-graph People-you-may-know readers
// (24 Oct 2026). Run with -tags integration and SUGGESTION_IT_POSTGRES_DSN
// pointing at a SCRATCH database: the test creates follows / blocks /
// hidden_users if they are missing and writes rows keyed by fresh uuids,
// removing them afterwards. Never point it at a shared app database.

func pymkPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SUGGESTION_IT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SUGGESTION_IT_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS follows (
			follower_id UUID NOT NULL,
			followee_id UUID NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (follower_id, followee_id)
		);
		CREATE INDEX IF NOT EXISTS idx_follows_followee_desc ON follows(followee_id, created_at DESC);
		CREATE TABLE IF NOT EXISTS blocks (
			blocker_id UUID NOT NULL,
			blocked_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (blocker_id, blocked_id)
		);
		CREATE TABLE IF NOT EXISTS hidden_users (
			user_id   UUID PRIMARY KEY,
			reason    TEXT        NOT NULL DEFAULT '',
			hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return pool
}

type pymkFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	ids  []uuid.UUID
}

func (f *pymkFixture) user() uuid.UUID {
	id := uuid.New()
	f.ids = append(f.ids, id)
	return id
}

func (f *pymkFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *pymkFixture) follow(a, b uuid.UUID) {
	f.exec(`INSERT INTO follows (follower_id, followee_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`, a, b)
}

func (f *pymkFixture) mutual(a, b uuid.UUID) { f.follow(a, b); f.follow(b, a) }

func (f *pymkFixture) block(a, b uuid.UUID) {
	f.exec(`INSERT INTO blocks (blocker_id, blocked_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`, a, b)
}

func (f *pymkFixture) hide(a uuid.UUID) {
	f.exec(`INSERT INTO hidden_users (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, a)
}

func (f *pymkFixture) cleanup() {
	ctx := context.Background()
	_, _ = f.pool.Exec(ctx, `DELETE FROM follows WHERE follower_id = ANY($1) OR followee_id = ANY($1)`, f.ids)
	_, _ = f.pool.Exec(ctx, `DELETE FROM blocks WHERE blocker_id = ANY($1) OR blocked_id = ANY($1)`, f.ids)
	_, _ = f.pool.Exec(ctx, `DELETE FROM hidden_users WHERE user_id = ANY($1)`, f.ids)
}

func TestFollowGraphPYMKLive(t *testing.T) {
	pool := pymkPool(t)
	f := &pymkFixture{t: t, pool: pool}
	t.Cleanup(f.cleanup)
	s := New(pool, nil)
	ctx := context.Background()

	viewer := f.user()
	friend1, friend2 := f.user(), f.user()
	f.mutual(viewer, friend1)
	f.mutual(viewer, friend2)

	// A one-way follower of the viewer is not a friend, so their friends are
	// not friends-of-friends.
	oneWay := f.user()
	f.follow(oneWay, viewer)
	viaOneWay := f.user()
	f.mutual(oneWay, viaOneWay)

	// A hidden friend bridges nobody.
	hiddenFriend := f.user()
	f.mutual(viewer, hiddenFriend)
	f.hide(hiddenFriend)
	viaHidden := f.user()
	f.mutual(hiddenFriend, viaHidden)

	both := f.user() // friend of friend1 and friend2 → count 2
	f.mutual(friend1, both)
	f.mutual(friend2, both)

	fanOnly := f.user() // follows friend1, not followed back → not a friend of friend1
	f.follow(fanOnly, friend1)

	alreadyFollowed := f.user()
	f.mutual(friend1, alreadyFollowed)
	f.follow(viewer, alreadyFollowed)

	blockedByViewer, blockedViewer, hidden := f.user(), f.user(), f.user()
	for _, c := range []uuid.UUID{blockedByViewer, blockedViewer, hidden} {
		f.mutual(friend1, c)
	}
	f.block(viewer, blockedByViewer)
	f.block(blockedViewer, viewer)
	f.hide(hidden)

	excluded := f.user()
	f.mutual(friend1, excluded)

	got, err := s.GetFriendsOfFriends(ctx, viewer, []uuid.UUID{excluded})
	if err != nil {
		t.Fatalf("GetFriendsOfFriends: %v", err)
	}
	if got[both] != 2 {
		t.Fatalf("friend of two friends counted %d, want 2 (%v)", got[both], got)
	}
	for name, id := range map[string]uuid.UUID{
		"viewer": viewer, "friend1": friend1, "friend2": friend2,
		"via one-way follower": viaOneWay, "via hidden friend": viaHidden,
		"one-way fan of a friend": fanOnly, "already followed": alreadyFollowed,
		"blocked by viewer": blockedByViewer, "blocked the viewer": blockedViewer,
		"hidden": hidden, "excludeIDs": excluded,
	} {
		if _, ok := got[id]; ok {
			t.Errorf("%s was suggested", name)
		}
	}

	// A nil exclude list must not empty the result (NULL <> ALL is NULL).
	if nilGot, err := s.GetFriendsOfFriends(ctx, viewer, nil); err != nil || nilGot[both] != 2 {
		t.Fatalf("nil excludeIDs: %v, %v", nilGot, err)
	}

	ids, err := s.GetMutualFriendIDs(ctx, viewer, both, 10)
	if err != nil {
		t.Fatalf("GetMutualFriendIDs: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if len(ids) != 2 || !seen[friend1] || !seen[friend2] {
		t.Fatalf("friends in common %v, want friend1 and friend2", ids)
	}
	if ids, _ := s.GetMutualFriendIDs(ctx, viewer, viaHidden, 10); len(ids) != 0 {
		t.Fatalf("a hidden friend was shown in common: %v", ids)
	}

	all, err := s.GetAllUsersWithFriends(ctx)
	if err != nil {
		t.Fatalf("GetAllUsersWithFriends: %v", err)
	}
	inAll := map[uuid.UUID]bool{}
	for _, id := range all {
		inAll[id] = true
	}
	if !inAll[viewer] || !inAll[both] {
		t.Fatal("a user with a mutual follow was not enumerated")
	}
	if inAll[fanOnly] {
		t.Fatal("a user with only a one-way follow was enumerated")
	}
	if inAll[hiddenFriend] || inAll[hidden] {
		t.Fatal("a hidden user was enumerated")
	}
}
