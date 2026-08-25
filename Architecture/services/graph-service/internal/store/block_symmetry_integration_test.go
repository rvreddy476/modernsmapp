//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module 2 M2-P0-3 — block symmetry, against a live PostgreSQL.
//
// The defect: GetBlockedAndMuted returned only the viewer's OUTGOING
// edges, so if B blocked A, B's content still reached A through the feed
// and every search surface. The person who pressed "block" received no
// protection from the person they blocked.
//
// These run against real SQL because the fix IS the SQL. A mock would
// assert the query I wrote returns what I said it returns.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -run Block -v

// SR-2: delegate to graphPool so the Module 2 suite runs against the same
// COMPLETE schema as the Module 3 suite. Its own bare pgxpool.New assumed the
// database was already migrated; when it was not, these tests failed on a
// missing table rather than proving anything about block symmetry.
func testPool(t *testing.T) *pgxpool.Pool {
	return graphPool(t)
}

// seedBlock inserts blocker → blocked and removes it afterwards.
func seedBlock(t *testing.T, pool *pgxpool.Pool, blocker, blocked uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO blocks (blocker_id, blocked_id, created_at) VALUES ($1, $2, NOW())
		 ON CONFLICT DO NOTHING`, blocker, blocked); err != nil {
		t.Fatalf("seed block: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2`, blocker, blocked)
	})
}

func seedMute(t *testing.T, pool *pgxpool.Pool, muter, muted uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO graph.mutes (muter_id, muted_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, muter, muted); err != nil {
		t.Fatalf("seed mute: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM graph.mutes WHERE muter_id = $1 AND muted_id = $2`, muter, muted)
	})
}

func contains(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// The core regression: when B blocks A, A must not see B's content.
func TestBlockedAndMuted_IsSymmetric(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()

	alice := uuid.New()
	bob := uuid.New()
	seedBlock(t, pool, bob, alice) // bob blocks alice

	// Bob's own view: he blocked Alice, so Alice is withheld from him.
	bobsView, err := s.GetBlockedAndMuted(ctx, bob)
	if err != nil {
		t.Fatalf("GetBlockedAndMuted(bob): %v", err)
	}
	if !contains(bobsView, alice) {
		t.Error("the blocker's own view must exclude the person they blocked")
	}

	// Alice's view: Bob blocked her, so Bob must be withheld from her too.
	// This is the direction that was missing entirely.
	alicesView, err := s.GetBlockedAndMuted(ctx, alice)
	if err != nil {
		t.Fatalf("GetBlockedAndMuted(alice): %v", err)
	}
	if !contains(alicesView, bob) {
		t.Fatal("M2-P0-3 REGRESSION: a user who blocked the viewer is not withheld — " +
			"the blocked party can still see the blocker's content")
	}
}

// Muting must stay one-way. Making it symmetric would let anyone remove
// themselves from someone else's feed by muting them.
func TestMute_StaysOneWay(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()

	alice := uuid.New()
	bob := uuid.New()
	seedMute(t, pool, bob, alice) // bob mutes alice

	bobsView, err := s.GetBlockedAndMuted(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(bobsView, alice) {
		t.Error("the muter's own view must exclude the person they muted")
	}

	alicesView, err := s.GetBlockedAndMuted(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if contains(alicesView, bob) {
		t.Fatal("a mute must NOT be symmetric — muting someone would otherwise " +
			"silently remove you from their feed")
	}
}

// GetBlockedBothWays is the blocks-only building block. It must cover
// both directions and exclude mutes.
//
// It is deliberately NOT what search uses — see
// TestBlockedAndMuted_SearchSuppressionMatrix for the set search receives.
func TestBlockedBothWays_ExcludesMutesIncludesBothBlockDirections(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()

	viewer := uuid.New()
	blockedByViewer := uuid.New()
	blockerOfViewer := uuid.New()
	mutedByViewer := uuid.New()

	seedBlock(t, pool, viewer, blockedByViewer)
	seedBlock(t, pool, blockerOfViewer, viewer)
	seedMute(t, pool, viewer, mutedByViewer)

	ids, err := s.GetBlockedBothWays(ctx, viewer)
	if err != nil {
		t.Fatalf("GetBlockedBothWays: %v", err)
	}

	if !contains(ids, blockedByViewer) {
		t.Error("outgoing block missing")
	}
	if !contains(ids, blockerOfViewer) {
		t.Error("incoming block missing — search would still surface the blocker")
	}
	if contains(ids, mutedByViewer) {
		t.Fatal("GetBlockedBothWays must return blocks only; it picked up a mute")
	}
}

// A mutual block must not produce a duplicate row, which would inflate
// the terms filter sent to OpenSearch on every query.
func TestBlockedBothWays_MutualBlockDeduplicates(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()

	alice := uuid.New()
	bob := uuid.New()
	seedBlock(t, pool, alice, bob)
	seedBlock(t, pool, bob, alice)

	ids, err := s.GetBlockedBothWays(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for _, id := range ids {
		if id == bob {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("mutual block yielded %d entries for the same user, want 1 (UNION should dedupe)", seen)
	}
}

// A viewer with no relationships gets an empty set, not an error — the
// callers fail closed on error, so a spurious error would break search
// for everyone who has never blocked anyone.
func TestBlockedBothWays_NoRelationshipsReturnsEmpty(t *testing.T) {
	pool := testPool(t)
	s := New(pool)

	ids, err := s.GetBlockedBothWays(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("a viewer with no blocks must not error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no exclusions, got %d", len(ids))
	}
}

// Module 2 review P0-5 — the exact set search must receive.
//
// The approved contract is: blocks in BOTH directions, plus the viewer's
// OUTGOING mutes, and never reverse mutes. Search calls the default
// GetBlockedAndMuted, so this asserts that one call composes all four
// cases correctly at the same time — a per-relation test can pass while
// the composition is still wrong.
func TestBlockedAndMuted_SearchSuppressionMatrix(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()

	viewer := uuid.New()
	blockedByViewer := uuid.New()
	blockerOfViewer := uuid.New()
	mutedByViewer := uuid.New()
	reverseMuter := uuid.New()
	unrelated := uuid.New()

	seedBlock(t, pool, viewer, blockedByViewer)
	seedBlock(t, pool, blockerOfViewer, viewer)
	seedMute(t, pool, viewer, mutedByViewer)
	seedMute(t, pool, reverseMuter, viewer)

	ids, err := s.GetBlockedAndMuted(ctx, viewer)
	if err != nil {
		t.Fatalf("GetBlockedAndMuted: %v", err)
	}

	if !contains(ids, blockedByViewer) {
		t.Error("an account the viewer blocked must be suppressed")
	}
	if !contains(ids, blockerOfViewer) {
		t.Error("an account that blocked the viewer must be suppressed — this is the " +
			"direction that was missing entirely")
	}
	if !contains(ids, mutedByViewer) {
		t.Error("M2-P0-5: an account the viewer muted must be suppressed in search")
	}
	if contains(ids, reverseMuter) {
		t.Fatal("REVERSE MUTE must not suppress: someone muting you cannot remove " +
			"you from their own results")
	}
	if contains(ids, unrelated) {
		t.Error("an unrelated account was suppressed")
	}
}
