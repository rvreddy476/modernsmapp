// Lane D7 store tests: snapped storage, the location change limits, the
// explain allowance, the deck radius steps, the migration and new-profile
// privacy defaults. The unit tests run without a database; the integration
// tests need TEST_PG_DSN on a database whose name ends in _test.
package store

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"testing"

	"github.com/atpost/dating-service/database"
	"github.com/google/uuid"
)

func TestSnapCoordinate(t *testing.T) {
	t.Parallel()
	cases := map[float64]float64{
		12.97649: 12.98, 12.97499: 12.97, 77.5993: 77.6, -33.8688: -33.87,
		0.125: 0.13, -0.125: -0.12, // half up, towards +Inf
		0.004: 0, -0.004: 0, 90: 90, -180: -180,
	}
	for in, want := range cases {
		if got := SnapCoordinate(in); got != want {
			t.Errorf("SnapCoordinate(%v) = %v, want %v", in, got, want)
		}
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		v := r.Float64()*360 - 180
		s := SnapCoordinate(v)
		if SnapCoordinate(s) != s {
			t.Fatalf("not idempotent: %v -> %v -> %v", v, s, SnapCoordinate(s))
		}
		if math.Abs(s-v) > 0.005+1e-9 {
			t.Fatalf("%v snapped %v: moved more than half a cell", v, s)
		}
		if cells := s * LocationGridCellsPerDegree; math.Abs(cells-math.Round(cells)) > 1e-6 {
			t.Fatalf("%v snapped %v: off the 0.01 grid", v, s)
		}
	}
}

func TestValidateLocation(t *testing.T) {
	t.Parallel()
	f := func(v float64) *float64 { return &v }
	lat, lng, err := ValidateLocation(f(17.38549), f(78.48671))
	if err != nil || lat != 17.39 || lng != 78.49 {
		t.Fatalf("valid = %v, %v, %v; want 17.39, 78.49", lat, lng, err)
	}
	bad := []struct {
		name     string
		lat, lng *float64
	}{
		{"latitude only", f(17.3), nil},
		{"longitude only", nil, f(78.4)},
		{"latitude > 90", f(90.01), f(10)},
		{"latitude < -90", f(-91), f(10)},
		{"longitude > 180", f(10), f(180.5)},
		{"longitude < -180", f(10), f(-181)},
		{"NaN", f(math.NaN()), f(10)},
		{"Inf", f(10), f(math.Inf(1))},
		{"null island", f(0), f(0)},
		{"snaps to null island", f(0.004), f(-0.004)},
	}
	for _, tc := range bad {
		if _, _, err := ValidateLocation(tc.lat, tc.lng); !errors.Is(err, ErrInvalidLocation) {
			t.Errorf("%s: err=%v, want ErrInvalidLocation", tc.name, err)
		}
	}
}

func TestEffectiveDiscoveryRadiusKm(t *testing.T) {
	t.Parallel()
	for in, want := range map[int]int{0: 0, -3: 0, 1: 5, 5: 5, 6: 10, 10: 10, 11: 25, 25: 25, 26: 50, 51: 100, 101: 250, 499: 500, 500: 500, 501: 501} {
		if got := EffectiveDiscoveryRadiusKm(in); got != want {
			t.Errorf("EffectiveDiscoveryRadiusKm(%d) = %d, want %d", in, got, want)
		}
	}
}

func d7Coords(t *testing.T, s *Store, id uuid.UUID) (lat, lng *float64, geohash *string) {
	t.Helper()
	if err := s.db.QueryRow(context.Background(), `
        SELECT latitude, longitude, location_geohash FROM dating_profiles WHERE user_id = $1`, id).
		Scan(&lat, &lng, &geohash); err != nil {
		t.Fatalf("read coords: %v", err)
	}
	return lat, lng, geohash
}

func d7LocationChanges(t *testing.T, s *Store, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM dating_location_changes WHERE user_id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count location changes: %v", err)
	}
	return n
}

func d7Point(lat, lng float64) UpsertProfileParams {
	return UpsertProfileParams{Latitude: &lat, Longitude: &lng}
}

func TestD7Store_StoredLocationIsSnappedAndSubCellMoveIsNoop(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	id := uuid.New()

	p, err := s.UpsertProfile(ctx, id, d7Point(12.97649, 77.59351))
	if err != nil {
		t.Fatalf("set location: %v", err)
	}
	if p.Latitude == nil || *p.Latitude != 12.98 || p.Longitude == nil || *p.Longitude != 77.59 {
		t.Fatalf("returned location = %v,%v; want the snapped 12.98,77.59", p.Latitude, p.Longitude)
	}
	lat, lng, gh := d7Coords(t, s, id)
	if *lat != 12.98 || *lng != 77.59 {
		t.Fatalf("stored location = %v,%v; want the snapped 12.98,77.59", *lat, *lng)
	}
	if gh == nil || *gh != EncodeGeohash(12.98, 77.59, LocationGeohashPrecision) {
		t.Fatalf("stored geohash = %v; want the snapped point's %q", gh, EncodeGeohash(12.98, 77.59, LocationGeohashPrecision))
	}
	if n := d7LocationChanges(t, s, id); n != 1 {
		t.Fatalf("location changes after the first set = %d, want 1", n)
	}

	// A move inside the same grid cell, well within 15 minutes: a no-op.
	if _, err := s.UpsertProfile(ctx, id, d7Point(12.9751, 77.5949)); err != nil {
		t.Fatalf("sub-cell move: %v", err)
	}
	if lat, lng, _ := d7Coords(t, s, id); *lat != 12.98 || *lng != 77.59 {
		t.Fatalf("sub-cell move changed the stored point to %v,%v", *lat, *lng)
	}
	if n := d7LocationChanges(t, s, id); n != 1 {
		t.Fatalf("a sub-cell move counted as a change (%d changes)", n)
	}
}

func TestD7Store_LocationChangeRateLimits(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	id := uuid.New()

	if _, err := s.UpsertProfile(ctx, id, d7Point(12.97, 77.59)); err != nil {
		t.Fatalf("first set: %v", err)
	}
	// The 2nd change within 15 minutes is refused, and nothing in that call
	// is written.
	bio := "should not land"
	params := d7Point(13.05, 77.59)
	params.Bio = &bio
	_, err := s.UpsertProfile(ctx, id, params)
	var limited *LocationRateLimitError
	if !errors.Is(err, ErrLocationChangeRateLimited) || !errors.As(err, &limited) || limited.Limits != DefaultLocationChangeLimits() {
		t.Fatalf("2nd change within 15 minutes: err=%v, want *LocationRateLimitError with the default limits", err)
	}
	if lat, _, _ := d7Coords(t, s, id); *lat != 12.97 {
		t.Fatalf("refused change moved the stored point to %v", *lat)
	}
	if p, err := s.GetProfile(ctx, id); err != nil || p.Bio == bio {
		t.Fatalf("refused call wrote the bio (%v)", err)
	}

	// 10 changes in the last 24h, none within 15 minutes: the 11th is refused.
	if _, err := s.db.Exec(ctx, `DELETE FROM dating_location_changes WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := s.db.Exec(ctx, `
            INSERT INTO dating_location_changes (user_id, changed_at)
            VALUES ($1, now() - make_interval(mins => $2))`, id, 20+i*60); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpsertProfile(ctx, id, d7Point(13.05, 77.59)); !errors.Is(err, ErrLocationChangeRateLimited) {
		t.Fatalf("11th change in 24h: err=%v, want ErrLocationChangeRateLimited", err)
	}
	// With 9 in the window (one aged past 24h) the 10th is accepted.
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_location_changes SET changed_at = now() - INTERVAL '25 hours'
        WHERE user_id = $1 AND changed_at = (SELECT MIN(changed_at) FROM dating_location_changes WHERE user_id = $1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertProfile(ctx, id, d7Point(13.05, 77.59)); err != nil {
		t.Fatalf("10th change in 24h: %v", err)
	}
	if lat, _, _ := d7Coords(t, s, id); *lat != 13.05 {
		t.Fatalf("accepted change stored %v, want 13.05", *lat)
	}
	if n := d7LocationChanges(t, s, id); n != 10 {
		t.Fatalf("changes in the window after the 10th = %d, want 10 (the aged row trimmed)", n)
	}
}

func TestD7Store_InvalidLocationRefusedAndNothingWritten(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	f := func(v float64) *float64 { return &v }
	for name, params := range map[string]UpsertProfileParams{
		"null island":    {Latitude: f(0), Longitude: f(0)},
		"latitude only":  {Latitude: f(17.3)},
		"out of range":   {Latitude: f(95), Longitude: f(78)},
		"longitude only": {Longitude: f(78)},
	} {
		id := uuid.New()
		if _, err := s.UpsertProfile(ctx, id, params); !errors.Is(err, ErrInvalidLocation) {
			t.Fatalf("%s: err=%v, want ErrInvalidLocation", name, err)
		}
		if _, err := s.GetProfile(ctx, id); !errors.Is(err, ErrProfileNotFound) {
			t.Fatalf("%s: a refused location created the profile (%v)", name, err)
		}
	}
}

// The SQL used by the migration is the Go arithmetic, bit for bit.
func TestD7Store_SQLSnapAndGeohashMatchGo(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 400; i++ {
		lat, lng := r.Float64()*180-90, r.Float64()*360-180
		var sLat, sLng float64
		var ghRaw, ghSnapped string
		if err := s.db.QueryRow(ctx, `
            SELECT dating_snap_coordinate($1::float8), dating_snap_coordinate($2::float8),
                   dating_geohash_encode($1::float8, $2::float8, 7),
                   dating_geohash_encode(dating_snap_coordinate($1::float8), dating_snap_coordinate($2::float8), 7)`,
			lat, lng).Scan(&sLat, &sLng, &ghRaw, &ghSnapped); err != nil {
			t.Fatalf("sql: %v", err)
		}
		if sLat != SnapCoordinate(lat) || sLng != SnapCoordinate(lng) {
			t.Fatalf("(%v,%v): sql snap %v,%v != go %v,%v", lat, lng, sLat, sLng, SnapCoordinate(lat), SnapCoordinate(lng))
		}
		if ghRaw != EncodeGeohash(lat, lng, 7) || ghSnapped != EncodeGeohash(SnapCoordinate(lat), SnapCoordinate(lng), 7) {
			t.Fatalf("(%v,%v): sql geohash %q/%q != go %q/%q", lat, lng, ghRaw, ghSnapped,
				EncodeGeohash(lat, lng, 7), EncodeGeohash(SnapCoordinate(lat), SnapCoordinate(lng), 7))
		}
	}
}

func TestD7Store_MigrationSnapsExistingRowsIdempotently(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	id := uuid.New()
	ensureProfileForTest(t, s, id)
	// A pre-D7 row: exact coordinates and the exact point's geohash.
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_profiles SET latitude = 17.385044, longitude = 78.486671, location_geohash = $2
        WHERE user_id = $1`, id, EncodeGeohash(17.385044, 78.486671, 7)); err != nil {
		t.Fatal(err)
	}
	var updatedBefore string
	if err := s.db.QueryRow(ctx, `SELECT updated_at::text FROM dating_profiles WHERE user_id = $1`, id).Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if err := database.BootstrapSchema(ctx, s.db); err != nil {
			t.Fatalf("bootstrap run %d: %v", run, err)
		}
		lat, lng, gh := d7Coords(t, s, id)
		if *lat != 17.39 || *lng != 78.49 || gh == nil || *gh != EncodeGeohash(17.39, 78.49, 7) {
			t.Fatalf("run %d: row = %v,%v %v; want 17.39,78.49 with the snapped geohash", run, *lat, *lng, gh)
		}
	}
	var updatedAfter string
	if err := s.db.QueryRow(ctx, `SELECT updated_at::text FROM dating_profiles WHERE user_id = $1`, id).Scan(&updatedAfter); err != nil {
		t.Fatal(err)
	}
	if updatedAfter != updatedBefore {
		t.Fatalf("migration touched updated_at (%s -> %s)", updatedBefore, updatedAfter)
	}
	var unsnapped int
	if err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_profiles
        WHERE latitude IS NOT NULL AND longitude IS NOT NULL
          AND (latitude <> dating_snap_coordinate(latitude) OR longitude <> dating_snap_coordinate(longitude)
            OR location_geohash IS DISTINCT FROM
               dating_geohash_encode(dating_snap_coordinate(latitude), dating_snap_coordinate(longitude), 7))`).Scan(&unsnapped); err != nil {
		t.Fatal(err)
	}
	if unsnapped != 0 {
		t.Fatalf("%d rows still unsnapped after the migration", unsnapped)
	}
}

func TestD7Store_NewProfilePrivacyDefaults(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	id := uuid.New()
	intent := "serious"
	if _, err := s.UpsertProfile(ctx, id, UpsertProfileParams{Intent: &intent}); err != nil {
		t.Fatalf("create: %v", err)
	}
	p, err := s.GetPrivacy(ctx, id)
	if err != nil {
		t.Fatalf("privacy: %v", err)
	}
	if !p.HideLastActive || p.EchoesConsent || !p.ApproximateLocation {
		t.Fatalf("new profile privacy = %+v; want hide_last_active on, echoes_consent off, approximate_location on", p)
	}
	// Echoes is an explicit opt-in, and withdrawing it drops the snapshot.
	if p, err = s.UpdatePrivacy(ctx, id, PrivacyUpdate{EchoesConsent: ptrBool(true)}); err != nil || !p.EchoesConsent {
		t.Fatalf("opt in = %+v, %v", p, err)
	}
	if err := s.UpsertEchoCache(ctx, id, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if p, err = s.UpdatePrivacy(ctx, id, PrivacyUpdate{EchoesConsent: ptrBool(false), ApproximateLocation: ptrBool(false)}); err != nil ||
		p.EchoesConsent || !p.ApproximateLocation {
		t.Fatalf("opt out = %+v, %v; want echoes off and approximate_location still on", p, err)
	}
	if _, err := s.GetEchoCache(ctx, id); !errors.Is(err, ErrEchoCacheNotFound) {
		t.Fatalf("echo cache after opting out: %v, want gone", err)
	}
}

// Stepping the distance preference cannot measure a candidate: 6 km admits
// exactly what 10 km does.
func TestD7Store_DeckRadiusIsStepped(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	gender := "d7-" + uuid.NewString()[:8]
	viewer, near := uuid.New(), uuid.New()
	seedDiscoverableProfile(t, s, viewer, "female")
	seedDiscoverableProfile(t, s, near, gender)
	if _, err := s.UpsertProfile(ctx, viewer, d7Point(12.97, 77.59)); err != nil {
		t.Fatal(err)
	}
	// 0.06 degrees of latitude north: ~6.7 km, bucket km_5_10.
	if _, err := s.UpsertProfile(ctx, near, d7Point(13.03, 77.59)); err != nil {
		t.Fatal(err)
	}
	lat, lng := 12.97, 77.59
	fetch := func(radius int) bool {
		t.Helper()
		cs, err := s.FetchCandidates(ctx, CandidateQuery{
			ViewerID: viewer, GenderFilter: gender, DistanceKmMax: radius,
			ViewerLat: &lat, ViewerLon: &lng, Limit: 50,
		})
		if err != nil {
			t.Fatalf("fetch radius %d: %v", radius, err)
		}
		return containsCandidate(cs, near)
	}
	if fetch(5) {
		t.Fatalf("a ~6.7 km candidate is in a 5 km deck")
	}
	for _, radius := range []int{6, 7, 10} {
		if !fetch(radius) {
			t.Fatalf("radius %d (stepped to 10) left out a ~6.7 km candidate", radius)
		}
	}
}

func TestD7Store_ExplainQuota(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	viewer := uuid.New()
	for i := 0; i < 3; i++ {
		if err := s.ConsumeExplainQuota(ctx, viewer, 3); err != nil {
			t.Fatalf("explain %d of 3: %v", i+1, err)
		}
	}
	var limited *ExplainRateLimitError
	if err := s.ConsumeExplainQuota(ctx, viewer, 3); !errors.Is(err, ErrExplainRateLimited) || !errors.As(err, &limited) || limited.Limit != 3 {
		t.Fatalf("explain 4 of 3: err=%v, want *ExplainRateLimitError{3}", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_explain_ledger SET requested_at = now() - INTERVAL '25 hours' WHERE viewer_id = $1`, viewer); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeExplainQuota(ctx, viewer, 3); err != nil {
		t.Fatalf("explain after the window passed: %v", err)
	}
}
