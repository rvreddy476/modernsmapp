// Audit 2026-10-30 L2: a mutual spark whose match formation failed is retried
// from dating_pending_matches. Integration tests skip without TEST_PG_DSN
// (CI only, "_test" database).
package service

import (
	"context"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func l2Markers(t *testing.T, st *store.Store, x, y uuid.UUID) int {
	t.Helper()
	return d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_pending_matches
        WHERE user_a = LEAST($1::uuid, $2::uuid) AND user_b = GREATEST($1::uuid, $2::uuid)`, x, y)
}

// makeDue moves the pair's marker into the past so the next pass claims it.
func l2MakeDue(t *testing.T, st *store.Store, x, y uuid.UUID) {
	t.Helper()
	d8Exec(t, st, `UPDATE dating_pending_matches SET next_attempt_at = now() - interval '1 second'
        WHERE user_a = LEAST($1::uuid, $2::uuid) AND user_b = GREATEST($1::uuid, $2::uuid)`, x, y)
}

// A live mutual spark that forms its match leaves no marker behind.
func TestAuditL2_LiveMatchClearsMarker(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	d3Match(t, svc, a, b)
	if n := l2Markers(t, st, a, b); n != 0 {
		t.Fatalf("markers after a formed match = %d, want 0", n)
	}
}

// Both sparks exist, no match was formed (the formation failed before the
// match row), and the marker is due: the retrier forms the match and clears
// the marker.
func TestAuditL2_RetrierFormsLostMatch(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	for _, p := range [][2]uuid.UUID{{a, b}, {b, a}} {
		if _, err := st.CreateSparkWithOptions(ctx, p[0], p[1], "photo", "0", "", store.SparkOptions{}); err != nil {
			t.Fatalf("seed spark: %v", err)
		}
	}
	if err := st.RecordPendingMatch(ctx, b, a, map[string]any{"target_kind": "photo", "target_ref": "0"}, 0); err != nil {
		t.Fatalf("record marker: %v", err)
	}
	l2MakeDue(t, st, a, b)
	if open, _ := st.HasOpenMatch(ctx, a, b); open {
		t.Fatal("setup: the pair already has a match")
	}

	if _, err := svc.RetryPendingMatches(ctx); err != nil {
		t.Fatalf("retry pass: %v", err)
	}
	if open, err := st.HasOpenMatch(ctx, a, b); err != nil || !open {
		t.Fatalf("after retry: open match=%v err=%v, want the lost match formed", open, err)
	}
	if n := l2Markers(t, st, a, b); n != 0 {
		t.Fatalf("markers after the retry formed the match = %d, want 0", n)
	}
	if got := len(eventsOfType(t, rec, "dating.match.formed")); got != 1 {
		t.Fatalf("match.formed events = %d, want 1", got)
	}
}

// A marker for a pair that is no longer mutual, or is blocked, is dropped
// without forming a match.
func TestAuditL2_RetrierDropsPairsThatCannotMatch(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()

	// Only one spark: not mutual.
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	if _, err := st.CreateSparkWithOptions(ctx, a, b, "photo", "0", "", store.SparkOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPendingMatch(ctx, a, b, nil, 0); err != nil {
		t.Fatal(err)
	}
	l2MakeDue(t, st, a, b)

	// Mutual but blocked: the block deletes the sparks, so seed them after.
	c, d := uuid.New(), uuid.New()
	seedActiveProfile(t, st, c)
	seedActiveProfile(t, st, d)
	d8Exec(t, st, `INSERT INTO dating_blocks (user_id, blocked_id) VALUES ($1, $2)`, c, d)
	d8Exec(t, st, `INSERT INTO dating_sparks (from_user_id, to_user_id, target_kind, target_ref) VALUES ($1, $2, 'photo', '0'), ($2, $1, 'photo', '0')`, c, d)
	if err := st.RecordPendingMatch(ctx, c, d, nil, 0); err != nil {
		t.Fatal(err)
	}
	l2MakeDue(t, st, c, d)

	if _, err := svc.RetryPendingMatches(ctx); err != nil {
		t.Fatalf("retry pass: %v", err)
	}
	for _, p := range [][2]uuid.UUID{{a, b}, {c, d}} {
		if n := l2Markers(t, st, p[0], p[1]); n != 0 {
			t.Fatalf("marker for %v kept; want dropped", p)
		}
		if open, _ := st.HasOpenMatch(ctx, p[0], p[1]); open {
			t.Fatalf("pair %v matched; want no match", p)
		}
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_matches
        WHERE user_a = LEAST($1::uuid, $2::uuid) AND user_b = GREATEST($1::uuid, $2::uuid)`, c, d); n != 0 {
		t.Fatalf("blocked pair has %d match rows", n)
	}
}

// Two passes at once (two replicas) claim a due marker once.
func TestAuditL2_ClaimIsExclusive(t *testing.T) {
	_, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	if err := st.RecordPendingMatch(ctx, a, b, nil, 0); err != nil {
		t.Fatal(err)
	}
	l2MakeDue(t, st, a, b)
	first, err := st.ClaimDuePendingMatches(ctx, 1000, pendingMatchLease)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.ClaimDuePendingMatches(ctx, 1000, pendingMatchLease)
	if err != nil {
		t.Fatal(err)
	}
	has := func(list []*store.PendingMatch) bool {
		for _, pm := range list {
			x, y := pm.UserA, pm.UserB
			if (x == a && y == b) || (x == b && y == a) {
				return true
			}
		}
		return false
	}
	if !has(first) || has(second) {
		t.Fatalf("claimed by first=%v second=%v; want exactly the first (leased)", has(first), has(second))
	}
	if err := st.ClearPendingMatch(ctx, a, b); err != nil {
		t.Fatal(err)
	}
}
