// Lane D3 store integration tests: one open match per pair, insert-or-get,
// the mutual check ignoring sparks older than the pair's last closure, the
// block transaction, unmatch deleting sparks, the pass cooldown and the
// spark ledger. Skipped unless TEST_PG_DSN is set; refuses a database not
// named *_test.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sparkCountBetween(t *testing.T, s *Store, a, b uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(), `
        SELECT COUNT(*)::int FROM dating_sparks
        WHERE (from_user_id = $1 AND to_user_id = $2) OR (from_user_id = $2 AND to_user_id = $1)`, a, b).Scan(&n); err != nil {
		t.Fatalf("count sparks: %v", err)
	}
	return n
}

func TestD3Store_MutualCheckIgnoresSparksBeforeClosure(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)

	if _, err := s.CreateSpark(ctx, a, b, "photo", "0", ""); err != nil {
		t.Fatalf("spark a->b: %v", err)
	}
	if _, err := s.CreateSpark(ctx, b, a, "photo", "0", ""); err != nil {
		t.Fatalf("spark b->a: %v", err)
	}
	if ok, err := s.HasReverseSparks(ctx, a, b); err != nil || !ok {
		t.Fatalf("mutual before any match = %v, %v; want true", ok, err)
	}
	id, created, err := s.CreateOrGetOpenMatch(ctx, a, b, nil)
	if err != nil || !created {
		t.Fatalf("create match: created=%v err=%v", created, err)
	}
	// Expiry leaves the sparks in place; only the closure time guards.
	if _, err := s.db.Exec(ctx, `UPDATE dating_matches SET status = 'expired', closed_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if ok, err := s.HasReverseSparks(ctx, a, b); err != nil || ok {
		t.Fatalf("a spark from before the closure still counts as interest (ok=%v err=%v)", ok, err)
	}
	// a repeats the same spark: the row is renewed and counts again, but
	// b's stale spark still does not.
	if _, err := s.CreateSpark(ctx, a, b, "photo", "0", ""); err != nil {
		t.Fatalf("renew a->b: %v", err)
	}
	if ok, _ := s.HasReverseSparks(ctx, b, a); !ok {
		t.Fatalf("renewed spark a->b does not count")
	}
	if ok, _ := s.HasReverseSparks(ctx, a, b); ok {
		t.Fatalf("stale spark b->a counts after only a renewed")
	}
	if _, err := s.CreateSpark(ctx, b, a, "photo", "0", ""); err != nil {
		t.Fatalf("renew b->a: %v", err)
	}
	if ok, _ := s.HasReverseSparks(ctx, a, b); !ok {
		t.Fatalf("two fresh sparks do not count as mutual")
	}
}

func TestD3Store_OneOpenMatchPerPair(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)

	id1, created1, err := s.CreateOrGetOpenMatch(ctx, a, b, nil)
	if err != nil || !created1 {
		t.Fatalf("first: created=%v err=%v", created1, err)
	}
	id2, created2, err := s.CreateOrGetOpenMatch(ctx, b, a, nil)
	if err != nil || created2 || id2 != id1 {
		t.Fatalf("second = %s created=%v err=%v; want the existing %s", id2, created2, err, id1)
	}
	ca, cb := canonicalPair(a, b)
	_, err = s.db.Exec(ctx, `INSERT INTO dating_matches (user_a, user_b, status) VALUES ($1, $2, 'conversing')`, ca, cb)
	if err == nil || !strings.Contains(err.Error(), "uq_dating_matches_open_pair") {
		t.Fatalf("a second open match row was accepted: %v", err)
	}
	if err := s.CloseMatch(ctx, id1, a); err != nil {
		t.Fatalf("close: %v", err)
	}
	id3, created3, err := s.CreateOrGetOpenMatch(ctx, a, b, nil)
	if err != nil || !created3 || id3 == id1 {
		t.Fatalf("after close: id=%s created=%v err=%v; want a new match", id3, created3, err)
	}
}

func TestD3Store_CreateOrGetOpenMatchRefusesBlockedPair(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	if _, err := s.db.Exec(ctx, `INSERT INTO dating_blocks (user_id, blocked_id) VALUES ($1, $2)`, b, a); err != nil {
		t.Fatalf("seed block: %v", err)
	}
	if blocked, err := s.IsBlockedEitherWay(ctx, a, b); err != nil || !blocked {
		t.Fatalf("IsBlockedEitherWay(a, b) = %v, %v; want true for b's block", blocked, err)
	}
	if _, _, err := s.CreateOrGetOpenMatch(ctx, a, b, nil); !errors.Is(err, ErrMatchPairBlocked) {
		t.Fatalf("match for a blocked pair: err=%v, want ErrMatchPairBlocked", err)
	}
}

func TestD3Store_BlockSeversPairInOneTransaction(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{a, b, c} {
		ensureProfileForTest(t, s, id)
	}
	for _, sp := range [][2]uuid.UUID{{a, b}, {b, a}, {a, c}} {
		if _, err := s.CreateSpark(ctx, sp[0], sp[1], "photo", "0", ""); err != nil {
			t.Fatalf("seed spark: %v", err)
		}
	}
	if err := s.AddStash(ctx, a, b, time.Time{}); err != nil {
		t.Fatalf("stash a->b: %v", err)
	}
	if err := s.AddStash(ctx, b, a, time.Time{}); err != nil {
		t.Fatalf("stash b->a: %v", err)
	}
	id, _, err := s.CreateOrGetOpenMatch(ctx, a, b, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}

	out, err := s.BlockUserAndSever(ctx, a, b)
	if err != nil {
		t.Fatalf("block: %v", err)
	}
	if len(out.ClosedMatches) != 1 || out.ClosedMatches[0].ID != id || out.ClosedMatches[0].Status != "closed" ||
		out.ClosedMatches[0].ClosedBy == nil || *out.ClosedMatches[0].ClosedBy != a {
		t.Fatalf("closed matches = %+v; want %s closed by the blocker", out.ClosedMatches, id)
	}
	if n := sparkCountBetween(t, s, a, b); n != 0 {
		t.Fatalf("%d sparks survive the block", n)
	}
	if n := sparkCountBetween(t, s, a, c); n != 1 {
		t.Fatalf("an unrelated spark was deleted (count %d)", n)
	}
	for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
		stashes, err := s.ListStash(ctx, pair[0])
		if err != nil {
			t.Fatalf("list stash: %v", err)
		}
		for _, st := range stashes {
			if st.CandidateID == pair[1] {
				t.Fatalf("stash %s -> %s survives the block", pair[0], pair[1])
			}
		}
	}
	again, err := s.BlockUserAndSever(ctx, a, b)
	if err != nil || len(again.ClosedMatches) != 0 {
		t.Fatalf("repeat block = %+v, %v; want an idempotent no-op", again, err)
	}
}

func TestD3Store_CloseMatchDeletesSparksBothWays(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{a, b, c} {
		ensureProfileForTest(t, s, id)
	}
	for _, sp := range [][2]uuid.UUID{{a, b}, {b, a}, {b, c}} {
		if _, err := s.CreateSpark(ctx, sp[0], sp[1], "prompt", "p1", ""); err != nil {
			t.Fatalf("seed spark: %v", err)
		}
	}
	id, _, err := s.CreateOrGetOpenMatch(ctx, a, b, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := s.CloseMatch(ctx, id, b); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := sparkCountBetween(t, s, a, b); n != 0 {
		t.Fatalf("unmatch kept %d sparks between the pair", n)
	}
	if n := sparkCountBetween(t, s, b, c); n != 1 {
		t.Fatalf("unmatch deleted an unrelated spark (count %d)", n)
	}
	m, err := s.GetMatch(ctx, id)
	if err != nil || m.Status != "closed" {
		t.Fatalf("match after close = %+v, %v", m, err)
	}
}

func TestD3Store_PassCooldownExcludesFromDeck(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	gender := "d3-" + uuid.NewString()[:8]
	viewer, candidate := uuid.New(), uuid.New()
	seedDiscoverableProfile(t, s, viewer, "female")
	seedDiscoverableProfile(t, s, candidate, gender)

	deck := func() []CandidateProfile {
		out, err := s.FetchCandidates(ctx, CandidateQuery{ViewerID: viewer, GenderFilter: gender, ExcludePassed: true, Limit: 50})
		if err != nil {
			t.Fatalf("fetch candidates: %v", err)
		}
		return out
	}
	if !containsCandidate(deck(), candidate) {
		t.Fatalf("candidate missing from the deck before the pass")
	}
	first, err := s.RecordPass(ctx, viewer, candidate, "not_my_type")
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if containsCandidate(deck(), candidate) {
		t.Fatalf("passed candidate still in the next deck")
	}
	second, err := s.RecordPass(ctx, viewer, candidate, "")
	if err != nil || !second.Equal(first) {
		t.Fatalf("repeat pass moved passed_at %v -> %v (%v)", first, second, err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_passes SET passed_at = now() - INTERVAL '31 days' WHERE user_id = $1 AND candidate_id = $2`, viewer, candidate); err != nil {
		t.Fatalf("age pass: %v", err)
	}
	if !containsCandidate(deck(), candidate) {
		t.Fatalf("candidate still excluded after the %s cooldown", PassCooldown)
	}
	rearmed, err := s.RecordPass(ctx, viewer, candidate, "")
	if err != nil || time.Since(rearmed) > time.Minute {
		t.Fatalf("pass after the cooldown did not re-arm: %v %v", rearmed, err)
	}
	if containsCandidate(deck(), candidate) {
		t.Fatalf("re-armed pass does not exclude the candidate")
	}
}

func TestD3Store_SparkLedgerSurvivesRevoke(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	from, to := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, from)
	ensureProfileForTest(t, s, to)
	const limit = 3
	var first *Spark
	for i := 0; i < limit; i++ {
		sp, err := s.CreateSparkWithQuota(ctx, from, to, "prompt", fmt.Sprintf("p%d", i), "", limit)
		if err != nil {
			t.Fatalf("spark %d: %v", i, err)
		}
		if first == nil {
			first = sp
		}
	}
	if _, err := s.CreateSparkWithQuota(ctx, from, to, "prompt", "p0", "", limit); err != nil {
		t.Fatalf("repeating an existing spark used quota: %v", err)
	}
	if _, err := s.CreateSparkWithQuota(ctx, from, to, "prompt", "extra", "", limit); !errors.Is(err, ErrSparkRateLimited) {
		t.Fatalf("spark %d: err=%v, want ErrSparkRateLimited", limit+1, err)
	}
	if err := s.DeleteSpark(ctx, first.ID, from); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.CreateSparkWithQuota(ctx, from, to, "prompt", "extra", "", limit); !errors.Is(err, ErrSparkRateLimited) {
		t.Fatalf("revoking refunded the allowance: err=%v", err)
	}
}
