package postgres

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
)

// kmNorth returns the latitude `km` north of lat (1 degree = kmPerDegLat).
func kmNorth(lat, km float64) float64 { return lat + km/kmPerDegLat }

func TestRankCandidates(t *testing.T) {
	lat, lng := 12.0, 77.0
	mk := func(km float64) DispatchCandidate {
		return DispatchCandidate{PartnerID: uuid.New(), UserID: uuid.New(), Latitude: kmNorth(lat, km), Longitude: lng}
	}
	c1, c4, c6, c2 := mk(1), mk(4), mk(6), mk(2)
	in := []DispatchCandidate{c4, c6, c1, c2}

	all := rankCandidates(in, lat, lng, 5, 0)
	if len(all) != 3 {
		t.Fatalf("radius filter: want 3 within 5 km, got %d", len(all))
	}
	wantOrder := []uuid.UUID{c1.PartnerID, c2.PartnerID, c4.PartnerID}
	for i, id := range wantOrder {
		if all[i].PartnerID != id {
			t.Fatalf("position %d: want nearest-first order, got %+v", i, all)
		}
	}
	if all[0].DistanceKM < 0.99 || all[0].DistanceKM > 1.01 {
		t.Fatalf("distance not filled: %v", all[0].DistanceKM)
	}
	top := rankCandidates(in, lat, lng, 5, 2)
	if len(top) != 2 || top[0].PartnerID != c1.PartnerID || top[1].PartnerID != c2.PartnerID {
		t.Fatalf("limit: want the 2 nearest, got %+v", top)
	}
}

func TestListDispatchCandidates_NearFreshActiveOnlineOnly(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Random far-off base so partners left by earlier runs never interfere.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	lat := -50 + rng.Float64()*10
	lng := 100 + rng.Float64()*60

	type ping struct {
		km  float64
		age time.Duration
	}
	seed := func(status string, online bool, pings ...ping) uuid.UUID {
		_, pid := seedDeliveryPartnerWithStatus(t, s, status, online)
		for _, p := range pings {
			if _, err := s.db.Exec(ctx, `
				INSERT INTO food.delivery_partner_locations (delivery_partner_id, latitude, longitude, recorded_at)
				VALUES ($1, $2, $3, NOW() - make_interval(secs => $4::float8))
			`, pid, kmNorth(lat, p.km), lng, p.age.Seconds()); err != nil {
				t.Fatal(err)
			}
		}
		return pid
	}
	nearer := seed("ACTIVE", true, ping{0.5, 5 * time.Second})
	near := seed("ACTIVE", true, ping{1, 10 * time.Second})
	latestNear := seed("ACTIVE", true, ping{30, 100 * time.Second}, ping{2, time.Second}) // moved in
	movedAway := seed("ACTIVE", true, ping{1.5, 60 * time.Second}, ping{30, time.Second})
	stale := seed("ACTIVE", true, ping{3, 10 * time.Minute})
	far := seed("ACTIVE", true, ping{20, time.Second})
	offline := seed("ACTIVE", false, ping{1, time.Second})
	suspended := seed("SUSPENDED", true, ping{1, time.Second})

	got, err := s.ListDispatchCandidates(ctx, DispatchQuery{
		Lat: lat, Lng: lng, RadiusKM: 5, MaxLocationAge: 120 * time.Second, Limit: 20,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ours := map[uuid.UUID]string{nearer: "nearer", near: "near", latestNear: "latestNear",
		movedAway: "movedAway", stale: "stale", far: "far", offline: "offline", suspended: "suspended"}
	var order []string
	for _, c := range got {
		if name, ok := ours[c.PartnerID]; ok {
			order = append(order, name)
			if c.DistanceKM <= 0 || c.UserID == uuid.Nil {
				t.Fatalf("candidate %s missing distance/user id: %+v", name, c)
			}
		}
	}
	want := []string{"nearer", "near", "latestNear"}
	if len(order) != len(want) {
		t.Fatalf("want %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("want %v, got %v", want, order)
		}
	}
}
