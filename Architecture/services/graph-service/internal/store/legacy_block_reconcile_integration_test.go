//go:build integration

package store

import (
	"context"
	sharedevents "github.com/atpost/shared/events"
	"testing"

	"github.com/google/uuid"
)

// Module 3 M3-P0-1 / SR-3 — legacy block reconciliation, against live PostgreSQL.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -run LegacyBlock -v
//
// Every row in profile-service's shadow `profile.blocks` is a block a real
// user asked for and never received: feed, search, chat and notifications all
// enforce the canonical table, which never heard about it. Retiring the routes
// stops new divergence; this reconciler is what helps the people already
// unprotected.

// fixtureLegacySource serves a fixed list, so the merge rule is tested rather
// than the SQL that happens to read the legacy table.
type fixtureLegacySource struct {
	pairs []LegacyBlockPair
}

func (f *fixtureLegacySource) LegacyBlocks(_ context.Context, offset, limit int) ([]LegacyBlockPair, error) {
	if offset >= len(f.pairs) {
		return nil, nil
	}
	end := offset + limit
	if end > len(f.pairs) {
		end = len(f.pairs)
	}
	return f.pairs[offset:end], nil
}

func canonicalBlockExists(t *testing.T, s *Store, blocker, blocked uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := s.db.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM blocks WHERE blocker_id = $1 AND blocked_id = $2)`,
		blocker, blocked).Scan(&exists); err != nil {
		t.Fatalf("check block: %v", err)
	}
	return exists
}

// A legacy block that never reached the canonical graph must become real —
// and must sever the relationships standing across it, exactly as a fresh
// block would. Importing it as a bare row would leave a block alongside a live
// follow edge, which is the inconsistent state M3-P0-6 exists to prevent.
func TestLegacyBlockReconcile_ImportsUnprotectedBlocksAndSeversRelationships(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	// The user pressed Block in profile-service. Nothing canonical happened,
	// so the follow edges are still live and the two are still connected.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO follows (follower_id, followee_id) VALUES ($1,$2)`, []any{alice, bob}},
		{`INSERT INTO follows (follower_id, followee_id) VALUES ($1,$2)`, []any{bob, alice}},
		{`INSERT INTO close_friends (user_id, friend_id) VALUES ($1,$2)`, []any{alice, bob}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if canonicalBlockExists(t, s, alice, bob) {
		t.Fatal("precondition: the block must NOT already be canonical")
	}

	src := &fixtureLegacySource{pairs: []LegacyBlockPair{{BlockerID: alice, BlockedID: bob}}}
	res, err := s.ReconcileLegacyBlocks(ctx, src, 100)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Imported != 1 {
		t.Fatalf("imported %d, want 1 (result %+v)", res.Imported, res)
	}

	if !canonicalBlockExists(t, s, alice, bob) {
		t.Fatal("the legacy block was not carried into the canonical graph; the " +
			"user is still unprotected everywhere that enforces blocks")
	}
	// The import must behave like a real block, not a row insert.
	assertNoRelationship(t, pool, alice, bob, "after legacy block import")

	var events int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM graph_outbox_events WHERE event_type=$3 AND actor_id=$1 AND target_id=$2`,
		alice, bob, sharedevents.UserBlocked).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events == 0 {
		t.Fatal("the imported block emitted no durable event, so chat never severs " +
			"the conversation and the block stays invisible downstream")
	}
}

// ANY-BLOCK-WINS: the reconciler must never remove a canonical block because
// the legacy table lacks it, and must never read an absence as an unblock.
// A false negative here silently exposes someone to an account they blocked.
func TestLegacyBlockReconcile_NeverRemovesACanonicalBlock(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("canonical block: %v", err)
	}

	// The legacy table says nothing about this pair — and mentions an
	// unrelated one, so the reconciler genuinely runs.
	carol, dave := pairFixture(t, pool)
	src := &fixtureLegacySource{pairs: []LegacyBlockPair{{BlockerID: carol, BlockedID: dave}}}
	if _, err := s.ReconcileLegacyBlocks(ctx, src, 100); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if !canonicalBlockExists(t, s, alice, bob) {
		t.Fatal("a canonical block was REMOVED because the legacy table did not " +
			"list it. Absence is not an unblock.")
	}
}

// Direction is preserved: a block by B of A does not satisfy a block by A of
// B. Both may be independently true, and the canonical store records who asked.
func TestLegacyBlockReconcile_PreservesDirection(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, bob, alice); err != nil {
		t.Fatalf("seed reverse block: %v", err)
	}

	// Legacy says alice blocked bob — the opposite direction, still missing.
	src := &fixtureLegacySource{pairs: []LegacyBlockPair{{BlockerID: alice, BlockedID: bob}}}
	res, err := s.ReconcileLegacyBlocks(ctx, src, 100)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Imported != 1 {
		t.Fatalf("imported %d, want 1: an existing reverse block was mistaken for "+
			"this one, so alice's own block was never applied (result %+v)", res.Imported, res)
	}
	if !canonicalBlockExists(t, s, alice, bob) || !canonicalBlockExists(t, s, bob, alice) {
		t.Fatal("both directions must exist independently after reconciliation")
	}
}

// Idempotent: reruns import nothing new and emit no duplicate transitions.
// The reconciler runs continuously, so a non-idempotent pass would multiply
// events on every tick.
func TestLegacyBlockReconcile_IsIdempotent(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	src := &fixtureLegacySource{pairs: []LegacyBlockPair{{BlockerID: alice, BlockedID: bob}}}
	first, err := s.ReconcileLegacyBlocks(ctx, src, 100)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.Imported != 1 {
		t.Fatalf("first pass imported %d, want 1", first.Imported)
	}

	second, err := s.ReconcileLegacyBlocks(ctx, src, 100)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Imported != 0 {
		t.Errorf("second pass imported %d, want 0: the reconciler re-applies work "+
			"on every tick", second.Imported)
	}
	if second.Skipped != 1 {
		t.Errorf("second pass skipped %d, want 1", second.Skipped)
	}
}

// Self-blocks in the legacy data must be skipped, not attempted: BlockAtomic
// rejects them, and an error there would abort the whole reconciliation and
// leave every later row unimported.
func TestLegacyBlockReconcile_SkipsSelfBlocksWithoutAborting(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	src := &fixtureLegacySource{pairs: []LegacyBlockPair{
		{BlockerID: alice, BlockedID: alice}, // corrupt legacy row
		{BlockerID: alice, BlockedID: bob},   // must still be imported
	}}
	res, err := s.ReconcileLegacyBlocks(ctx, src, 100)
	if err != nil {
		t.Fatalf("a corrupt legacy row aborted the whole reconciliation: %v", err)
	}
	if res.Imported != 1 || res.Skipped != 1 {
		t.Fatalf("result %+v, want 1 imported / 1 skipped", res)
	}
	if !canonicalBlockExists(t, s, alice, bob) {
		t.Fatal("the valid block after the corrupt row was never imported")
	}
}

// Batching must not drop or repeat rows at a page boundary.
func TestLegacyBlockReconcile_PaginatesWithoutLosingRows(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()

	const n = 7
	var pairs []LegacyBlockPair
	type pr struct{ a, b uuid.UUID }
	var made []pr
	for i := 0; i < n; i++ {
		a, b := pairFixture(t, pool)
		pairs = append(pairs, LegacyBlockPair{BlockerID: a, BlockedID: b})
		made = append(made, pr{a, b})
	}

	// batchSize 2 over 7 rows: three full pages plus a short one.
	res, err := s.ReconcileLegacyBlocks(ctx, &fixtureLegacySource{pairs: pairs}, 2)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Scanned != n || res.Imported != n {
		t.Fatalf("result %+v, want %d scanned / %d imported", res, n, n)
	}
	for _, p := range made {
		if !canonicalBlockExists(t, s, p.a, p.b) {
			t.Errorf("block %s→%s was lost at a page boundary", p.a, p.b)
		}
	}
}
