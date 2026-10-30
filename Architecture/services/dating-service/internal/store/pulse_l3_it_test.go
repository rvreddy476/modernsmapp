// Audit 2026-10-30 L3 against Postgres. TEST_PG_DSN, CI only.
package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// The window wraps round the key space: every eligible candidate is
// reachable whatever the pivot, and the limit (over-fetched 4x) bounds it.
func TestFetchCandidates_SampleWindowReachesEveryCandidate(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	gender := "l3-" + uuid.NewString()[:8]
	viewer := uuid.New()
	ensureProfileForTest(t, s, viewer)
	seedDiscoverableProfile(t, s, viewer, "female")
	ids := make([]uuid.UUID, 0, 6)
	for i := 0; i < 6; i++ {
		id := uuid.New()
		ensureProfileForTest(t, s, id)
		seedDiscoverableProfile(t, s, id, gender)
		ids = append(ids, id)
	}
	for run := 0; run < 10; run++ {
		got, err := s.FetchCandidates(ctx, CandidateQuery{ViewerID: viewer, GenderFilter: gender, OnlyIDs: ids, Limit: 50})
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(got) != len(ids) {
			t.Fatalf("run %d: %d candidates, want all %d (the window must wrap round)", run, len(got), len(ids))
		}
	}
	got, err := s.FetchCandidates(ctx, CandidateQuery{ViewerID: viewer, GenderFilter: gender, OnlyIDs: ids, Limit: 1})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("limit 1 returned %d, want the 4x over-fetch", len(got))
	}
}

// With travel on: a traveller whose destination is in the viewer's cell is
// in the deck; one whose home is there but who is travelling elsewhere is not.
func TestFetchCandidates_TravelGeoDriver(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	s.SetTravelEnabled(true)
	ctx := context.Background()
	gender := "l3t-" + uuid.NewString()[:8]
	viewer, visitor, away, local := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, visitor, away, local} {
		ensureProfileForTest(t, s, id)
	}
	seedDiscoverableProfile(t, s, viewer, "female")
	for _, id := range []uuid.UUID{visitor, away, local} {
		seedDiscoverableProfile(t, s, id, gender)
	}
	// Hyderabad (viewer, local, away's home) and Mumbai (visitor's home,
	// away's destination).
	hyd, mum := [2]float64{17.39, 78.49}, [2]float64{19.08, 72.88}
	setHome := func(id uuid.UUID, pt [2]float64) {
		if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET latitude = $2, longitude = $3, location_geohash = $4 WHERE user_id = $1`,
			id, pt[0], pt[1], EncodeGeohash(pt[0], pt[1], 7)); err != nil {
			t.Fatal(err)
		}
	}
	trip := func(id uuid.UUID, pt [2]float64) {
		if _, err := s.db.Exec(ctx, `INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
            VALUES ($1, 'pass_30d', now() - interval '1 hour', now() + interval '1 day', 'test')
            ON CONFLICT (user_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO dating_travel (user_id, city_code, city_label, latitude, longitude, geohash, starts_at, ends_at)
            VALUES ($1, 'x', 'X', $2, $3, $4, now() - interval '1 hour', now() + interval '1 day')`,
			id, pt[0], pt[1], EncodeGeohash(pt[0], pt[1], 7)); err != nil {
			t.Fatal(err)
		}
	}
	setHome(viewer, hyd)
	setHome(local, hyd)
	setHome(away, hyd)
	trip(away, mum)
	setHome(visitor, mum)
	trip(visitor, hyd)

	got, err := s.FetchCandidates(ctx, CandidateQuery{ViewerID: viewer, GenderFilter: gender,
		ViewerLat: &hyd[0], ViewerLon: &hyd[1], DistanceKmMax: 25, Limit: 50})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	for _, c := range got {
		seen[c.UserID] = true
	}
	if !seen[local] || !seen[visitor] || seen[away] {
		t.Fatalf("deck local=%v visitor=%v away=%v; want local and the visitor, not the one travelling away", seen[local], seen[visitor], seen[away])
	}
}

// Nebula's batch read keeps the pass order and admits exactly the users the
// single read admits (same visibility rule, blocks included).
func TestGetCandidatesForViewer_MatchesSingleRead(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	viewer, a, b, blocked, bare := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, a, b, blocked, bare} {
		ensureProfileForTest(t, s, id)
	}
	seedDiscoverableProfile(t, s, viewer, "female")
	for _, id := range []uuid.UUID{a, b, blocked} {
		seedDiscoverableProfile(t, s, id, "male")
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO dating_blocks (user_id, blocked_id) VALUES ($1, $2)`, blocked, viewer); err != nil {
		t.Fatal(err)
	}
	ask := []uuid.UUID{b, blocked, bare, a, uuid.New()}
	got, err := s.GetCandidatesForViewer(ctx, viewer, ask)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	var want []uuid.UUID
	for _, id := range ask {
		if c, err := s.GetCandidateForViewer(ctx, viewer, id); err == nil && c != nil {
			want = append(want, id)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("batch returned %d, single reads admit %d", len(got), len(want))
	}
	for i := range want {
		if got[i].UserID != want[i] {
			t.Fatalf("batch[%d] = %s, want %s (pass order)", i, got[i].UserID, want[i])
		}
	}
	for _, c := range got {
		if c.UserID == blocked {
			t.Fatal("batch returned a user who blocked the viewer")
		}
	}
}
