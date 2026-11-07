package postgres

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/geo"
	"github.com/google/uuid"
)

// Food audit L3: dispatch work is bounded by the riders near the pickup, not
// by every online rider, and the ranking rules are unchanged.

// The per-partner lookups (latest location, offers today) join only the geo
// prefilter, and the ranked set is capped.
func TestDispatchCandidatesSQL_PrefiltersBeforeThePerRiderLookups(t *testing.T) {
	sql := strings.Join(strings.Fields(dispatchCandidatesSQL), " ")
	near := strings.Index(sql, "WITH near AS ( SELECT DISTINCT l.delivery_partner_id AS id FROM food.delivery_partner_locations l WHERE l.recorded_at >= NOW() - make_interval(secs => $1::float8) AND l.latitude::float8 BETWEEN $2::float8 AND $3::float8 AND l.longitude::float8 BETWEEN $4::float8 AND $5::float8 )")
	drive := strings.Index(sql, "FROM near JOIN food.delivery_partners dp ON dp.id = near.id JOIN LATERAL")
	if near != 0 || drive < 0 {
		t.Fatalf("dispatch query does not drive from the geo prefilter:\n%s", sql)
	}
	if strings.Count(sql, "FROM food.delivery_partners") != 0 {
		t.Fatalf("dispatch query scans delivery_partners directly:\n%s", sql)
	}
	if !strings.HasSuffix(sql, "LIMIT $7") || dispatchCandidateCap != 500 {
		t.Fatalf("ranked set not capped (cap %d):\n%s", dispatchCandidateCap, sql)
	}
}

// rankCandidates over a crowd keeps exactly the riders inside the radius and
// orders them by the unchanged rules: fewest offers today, then longest since
// the last offer (never offered first), then nearest.
func TestRankCandidates_ManyRiders(t *testing.T) {
	lat, lng := 12.97, 77.59
	rng := rand.New(rand.NewSource(7))
	base := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	const riders, radius = 3000, 5.0
	in := make([]DispatchCandidate, 0, riders)
	inside := 0
	for i := 0; i < riders; i++ {
		c := DispatchCandidate{
			PartnerID:   uuid.New(),
			Latitude:    lat + (rng.Float64()-0.5)*0.2,
			Longitude:   lng + (rng.Float64()-0.5)*0.2,
			OffersToday: rng.Intn(4),
		}
		if c.OffersToday > 0 && rng.Intn(3) > 0 {
			at := base.Add(time.Duration(rng.Intn(600)) * time.Minute)
			c.LastOfferedAt = &at
		}
		if geo.HaversineKM(lat, lng, c.Latitude, c.Longitude) <= radius {
			inside++
		}
		in = append(in, c)
	}
	all := rankCandidates(in, lat, lng, radius, 0)
	if len(all) != inside || inside == 0 {
		t.Fatalf("kept %d riders, want the %d inside %.0f km", len(all), inside, radius)
	}
	before := func(a, b DispatchCandidate) bool {
		if a.OffersToday != b.OffersToday {
			return a.OffersToday < b.OffersToday
		}
		switch {
		case a.LastOfferedAt == nil && b.LastOfferedAt != nil:
			return true
		case a.LastOfferedAt != nil && b.LastOfferedAt == nil:
			return false
		case a.LastOfferedAt != nil && !a.LastOfferedAt.Equal(*b.LastOfferedAt):
			return a.LastOfferedAt.Before(*b.LastOfferedAt)
		}
		return a.DistanceKM < b.DistanceKM
	}
	for i, c := range all {
		if c.DistanceKM > radius {
			t.Fatalf("rider %d at %.2f km is outside the radius", i, c.DistanceKM)
		}
		if i > 0 && before(c, all[i-1]) {
			t.Fatalf("riders %d and %d out of order: %+v then %+v", i-1, i, all[i-1], c)
		}
	}
	top := rankCandidates(in, lat, lng, radius, 20)
	if len(top) != 20 {
		t.Fatalf("limit: got %d, want 20", len(top))
	}
	for i := range top {
		if top[i].PartnerID != all[i].PartnerID {
			t.Fatalf("limit changed the order at %d", i)
		}
	}
}

// Against Postgres: a crowd of online riders elsewhere (and stale ones
// nearby) never displaces or hides the fresh riders near the pickup.
func TestListDispatchCandidates_ManyRidersOnlyNearOnesRanked(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	lat := -50 + rng.Float64()*10
	lng := 100 + rng.Float64()*60
	ping := func(pid uuid.UUID, km float64, age time.Duration) {
		if _, err := s.db.Exec(ctx, `
			INSERT INTO food.delivery_partner_locations (delivery_partner_id, latitude, longitude, recorded_at)
			VALUES ($1, $2, $3, NOW() - make_interval(secs => $4::float8))
		`, pid, kmNorth(lat, km), lng, age.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 120; i++ {
		_, far := seedDeliveryPartnerWithStatus(t, s, "ACTIVE", true)
		ping(far, 40+float64(i%10), time.Second)
		_, stale := seedDeliveryPartnerWithStatus(t, s, "ACTIVE", true)
		ping(stale, 1, 30*time.Minute)
	}
	near := map[uuid.UUID]float64{}
	for i := 0; i < 30; i++ {
		_, pid := seedDeliveryPartnerWithStatus(t, s, "ACTIVE", true)
		km := 0.1 + float64(i)*0.1
		ping(pid, km, time.Second)
		near[pid] = km
	}
	got, err := s.ListDispatchCandidates(ctx, DispatchQuery{Lat: lat, Lng: lng, RadiusKM: 5, MaxLocationAge: 2 * time.Minute, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var ours []DispatchCandidate
	for _, c := range got {
		if _, ok := near[c.PartnerID]; ok {
			ours = append(ours, c)
		} else if geo.HaversineKM(lat, lng, c.Latitude, c.Longitude) > 5 {
			t.Fatalf("a rider outside the radius was ranked: %+v", c)
		}
	}
	if len(ours) != len(near) {
		t.Fatalf("ranked %d of the %d near riders", len(ours), len(near))
	}
	for i := 1; i < len(ours); i++ {
		if ours[i].DistanceKM < ours[i-1].DistanceKM {
			t.Fatalf("near riders not nearest first at %d: %v then %v", i, ours[i-1].DistanceKM, ours[i].DistanceKM)
		}
	}
}
