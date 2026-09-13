package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/routing"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	etaRider      = routing.LatLng{Lat: 12.95, Lng: 77.60}
	etaRestaurant = routing.LatLng{Lat: 12.9716, Lng: 77.5946}
	etaCustomer   = routing.LatLng{Lat: 12.9784, Lng: 77.6408}
	etaNow        = time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
)

// legRouter answers only the legs it was given.
type legRouter struct {
	mu    sync.Mutex
	legs  map[[2]routing.LatLng]routing.Route
	asked [][2]routing.LatLng
}

func (l *legRouter) Route(_ context.Context, from, to routing.LatLng) (routing.Route, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, [2]routing.LatLng{from, to})
	r, ok := l.legs[[2]routing.LatLng{from, to}]
	if !ok {
		return routing.Route{}, errors.New("unpriced leg")
	}
	return r, nil
}

func leg(from, to routing.LatLng, d time.Duration, source string) ([2]routing.LatLng, routing.Route) {
	return [2]routing.LatLng{from, to}, routing.Route{Duration: d, Source: source}
}

func newLegRouter(entries ...any) *legRouter {
	l := &legRouter{legs: map[[2]routing.LatLng]routing.Route{}}
	for i := 0; i+1 < len(entries); i += 2 {
		l.legs[entries[i].([2]routing.LatLng)] = entries[i+1].(routing.Route)
	}
	return l
}

func etaPtr(t time.Time) *time.Time { return &t }

func TestETAForJob_FormulaPerState(t *testing.T) {
	rest, cust := etaRestaurant, etaCustomer
	base := postgres.ETAJob{OrderID: uuid.New(), Restaurant: &rest, Customer: &cust, OrderStatus: "DELIVERY_ASSIGNED"}
	with := func(assignment string, ready *time.Time) postgres.ETAJob {
		j := base
		j.AssignmentStatus, j.FoodReadyAt = assignment, ready
		return j
	}
	g, h := routing.SourceGoogle, routing.SourceHaversine
	cases := []struct {
		name       string
		job        postgres.ETAJob
		router     *legRouter
		wantAt     time.Time
		wantSource string
		wantOK     bool
	}{
		{"accepted, food ready: rider to restaurant then restaurant to customer",
			with("ACCEPTED", nil),
			newLegRouter(k(leg(etaRider, rest, 6*time.Minute, g)), v(leg(etaRider, rest, 6*time.Minute, g)), k(leg(rest, cust, 14*time.Minute, g)), v(leg(rest, cust, 14*time.Minute, g))),
			etaNow.Add(20 * time.Minute), g, true},
		{"accepted, food not ready: waits for the kitchen",
			with("ACCEPTED", etaPtr(etaNow.Add(30*time.Minute))),
			newLegRouter(k(leg(etaRider, rest, 6*time.Minute, g)), v(leg(etaRider, rest, 6*time.Minute, g)), k(leg(rest, cust, 14*time.Minute, g)), v(leg(rest, cust, 14*time.Minute, g))),
			etaNow.Add(44 * time.Minute), g, true},
		{"accepted, food ready before the rider arrives: the ride decides",
			with("ACCEPTED", etaPtr(etaNow.Add(2*time.Minute))),
			newLegRouter(k(leg(etaRider, rest, 6*time.Minute, g)), v(leg(etaRider, rest, 6*time.Minute, g)), k(leg(rest, cust, 14*time.Minute, g)), v(leg(rest, cust, 14*time.Minute, g))),
			etaNow.Add(20 * time.Minute), g, true},
		{"at the restaurant: no rider leg, waits for the food",
			with("ARRIVED_AT_RESTAURANT", etaPtr(etaNow.Add(10*time.Minute))),
			newLegRouter(k(leg(rest, cust, 14*time.Minute, g)), v(leg(rest, cust, 14*time.Minute, g))),
			etaNow.Add(24 * time.Minute), g, true},
		{"picked up: rider to customer",
			with("PICKED_UP", nil),
			newLegRouter(k(leg(etaRider, cust, 9*time.Minute, g)), v(leg(etaRider, cust, 9*time.Minute, g))),
			etaNow.Add(9 * time.Minute), g, true},
		{"at the customer: rider to customer",
			with("ARRIVED_AT_CUSTOMER", nil),
			newLegRouter(k(leg(etaRider, cust, 30*time.Second, h)), v(leg(etaRider, cust, 30*time.Second, h))),
			etaNow.Add(30 * time.Second), h, true},
		{"one haversine leg labels the whole ETA haversine",
			with("ACCEPTED", nil),
			newLegRouter(k(leg(etaRider, rest, 6*time.Minute, h)), v(leg(etaRider, rest, 6*time.Minute, h)), k(leg(rest, cust, 14*time.Minute, g)), v(leg(rest, cust, 14*time.Minute, g))),
			etaNow.Add(20 * time.Minute), h, true},
		{"no customer point: no ETA", func() postgres.ETAJob { j := with("PICKED_UP", nil); j.Customer = nil; return j }(), newLegRouter(), time.Time{}, "", false},
		{"no restaurant point before pickup: no ETA", func() postgres.ETAJob { j := with("ACCEPTED", nil); j.Restaurant = nil; return j }(), newLegRouter(), time.Time{}, "", false},
		{"a leg that cannot be priced: no ETA", with("PICKED_UP", nil), newLegRouter(), time.Time{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(nil).WithRouter(tc.router)
			got, ok := svc.etaForJob(context.Background(), tc.job, etaRider, etaNow)
			if ok != tc.wantOK || !got.At.Equal(tc.wantAt) || got.Source != tc.wantSource {
				t.Fatalf("eta = %v %q ok=%v, want %v %q ok=%v", got.At, got.Source, ok, tc.wantAt, tc.wantSource, tc.wantOK)
			}
		})
	}
	// ARRIVED_AT_RESTAURANT never prices the rider leg.
	r := newLegRouter(k(leg(rest, cust, time.Minute, g)), v(leg(rest, cust, time.Minute, g)))
	New(nil).WithRouter(r).etaForJob(context.Background(), with("ARRIVED_AT_RESTAURANT", nil), etaRider, etaNow)
	if len(r.asked) != 1 || r.asked[0] != [2]routing.LatLng{rest, cust} {
		t.Fatalf("asked %v", r.asked)
	}
}

func k(key [2]routing.LatLng, _ routing.Route) [2]routing.LatLng { return key }
func v(_ [2]routing.LatLng, r routing.Route) routing.Route       { return r }

type etaWrite struct {
	orderID          uuid.UUID
	claimedAt, etaAt time.Time
	source           string
}

type etaFakeStore struct {
	Store
	res    *postgres.DeliveryLocationResult
	mu     sync.Mutex
	writes []etaWrite
	apply  bool
}

func (f *etaFakeStore) UpdateDeliveryLocation(context.Context, uuid.UUID, postgres.LocationUpdate) (*postgres.DeliveryLocationResult, error) {
	return f.res, nil
}

func (f *etaFakeStore) RecordOrderETA(_ context.Context, orderID uuid.UUID, claimedAt, etaAt time.Time, source string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, etaWrite{orderID, claimedAt, etaAt, source})
	return f.apply, nil
}

// A ping that claimed an order's ETA prices it, writes it under the claim and
// puts it on that order's rider.location frame; an order it did not claim
// carries its stored ETA; an order with no ETA carries no ETA keys; nothing
// goes to the outbox.
func TestUpdateDeliveryLocation_RecomputedETARidesTheFrame(t *testing.T) {
	recomputed, stored, none := uuid.New(), uuid.New(), uuid.New()
	rest, cust := etaRestaurant, etaCustomer
	claimed := etaNow.Add(-time.Second)
	storedAt := etaNow.Add(12 * time.Minute)
	res := &postgres.DeliveryLocationResult{
		Latitude: etaRider.Lat, Longitude: etaRider.Lng, RecordedAtTime: etaNow,
		Frames: []postgres.RiderLocationFrame{
			{OrderID: recomputed, ETAAt: etaPtr(etaNow.Add(40 * time.Minute)), ETASource: routing.SourceHaversine},
			{OrderID: stored, ETAAt: &storedAt, ETASource: routing.SourceHaversine},
			{OrderID: none},
		},
		ETAJobs: []postgres.ETAJob{{
			OrderID: recomputed, AssignmentStatus: "PICKED_UP", OrderStatus: "PICKED_UP",
			Restaurant: &rest, Customer: &cust, ClaimedAt: claimed,
		}},
	}
	st := &etaFakeStore{res: res, apply: true}
	svc, rt, ob := capturingService(st)
	svc.WithRouter(newLegRouter(k(leg(etaRider, cust, 9*time.Minute, routing.SourceGoogle)), v(leg(etaRider, cust, 9*time.Minute, routing.SourceGoogle))))

	if _, err := svc.UpdateDeliveryLocation(context.Background(), uuid.New(), postgres.LocationUpdate{}); err != nil {
		t.Fatal(err)
	}
	if len(st.writes) != 1 || st.writes[0] != (etaWrite{recomputed, claimed, etaNow.Add(9 * time.Minute), routing.SourceGoogle}) {
		t.Fatalf("writes = %+v", st.writes)
	}
	frames := map[string]map[string]any{}
	for _, ev := range rt.events {
		var f map[string]any
		if err := json.Unmarshal(ev.raw, &f); err != nil {
			t.Fatal(err)
		}
		frames[f["order_id"].(string)] = f
	}
	if f := frames[recomputed.String()]; f["eta_at"] != "2026-09-13T06:39:00Z" || f["eta_source"] != "google" {
		t.Fatalf("recomputed frame = %v", f)
	}
	if f := frames[stored.String()]; f["eta_at"] != "2026-09-13T06:42:00Z" || f["eta_source"] != "haversine" {
		t.Fatalf("stored frame = %v", f)
	}
	keys := []string{}
	for key := range frames[none.String()] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"lat", "lng", "order_id", "recorded_at"}) {
		t.Fatalf("frame without an eta has keys %v", keys)
	}
	if len(ob.events) != 0 {
		t.Fatalf("an eta went to the outbox: %v", ob.events)
	}

	// A write that lost to a newer claim is not announced: the frame keeps the
	// stored ETA.
	st.apply, st.writes, rt.events = false, nil, nil
	if _, err := svc.UpdateDeliveryLocation(context.Background(), uuid.New(), postgres.LocationUpdate{}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range rt.events {
		var f map[string]any
		_ = json.Unmarshal(ev.raw, &f)
		if f["order_id"] == recomputed.String() && f["eta_at"] != "2026-09-13T07:10:00Z" {
			t.Fatalf("unapplied recompute announced: %v", f)
		}
	}
}
