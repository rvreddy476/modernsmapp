package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Audit 2026-10-30 L3: the deck reads a bounded window from a random pivot,
// not ORDER BY random() over every match.
func TestCandidateSampleSQLIsABoundedWindow(t *testing.T) {
	sql := candidateSampleSQL("p.user_id", "WHERE p.profile_status = 'active'", 9, 8)
	if strings.Contains(strings.ToLower(sql), "random()") {
		t.Fatalf("sample still sorts by random(): %s", sql)
	}
	for _, want := range []string{
		"AND p.user_id >= $9::uuid", "AND p.user_id < $9::uuid",
		"ORDER BY p.user_id\n        LIMIT $8)", "UNION ALL", ") sample\n        LIMIT $8",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("sample SQL lacks %q:\n%s", want, sql)
		}
	}
	if strings.Count(sql, "WHERE p.profile_status = 'active'") != 2 {
		t.Errorf("both branches must carry every filter:\n%s", sql)
	}
}

// With travel on, the geo prefilter has an index-friendly driver: home
// geohash (the indexed column, matching idx_dating_profiles_geohash_prefix's
// predicate) and trip destinations, over the same cell parameters.
func TestTravelGeoDriverPredicate(t *testing.T) {
	p := travelGeoDriverPredicate(5, 3)
	for _, want := range []string{
		"gp.deleted_at IS NULL AND gp.location_geohash IS NOT NULL",
		"gp.location_geohash LIKE $5 OR gp.location_geohash LIKE $6 OR gp.location_geohash LIKE $7",
		"tr.geohash LIKE $5 OR tr.geohash LIKE $6 OR tr.geohash LIKE $7",
		"UNION",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("driver lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "COALESCE") || strings.Contains(p, "$8") {
		t.Errorf("driver is not sargable or uses a wrong parameter:\n%s", p)
	}
}

func TestOrderCandidatesKeepsRequestOrder(t *testing.T) {
	a, b, c, missing := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	byID := map[uuid.UUID]*CandidateProfile{a: {UserID: a}, b: {UserID: b}, c: {UserID: c}}
	got := orderCandidates([]uuid.UUID{c, missing, a, b, a}, byID)
	if len(got) != 3 || got[0].UserID != c || got[1].UserID != a || got[2].UserID != b {
		t.Fatalf("order = %v", got)
	}
}
