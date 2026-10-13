package service

import (
	"context"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
	"math"
	"testing"
	"time"
)

func TestCustomerLocationDBOwnershipAndFreshness(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	customer := uuid.New()
	quote := mustEstimate(t, svc, fridayPeakIST, &customer, "auto", "")
	ride, _ := assignedRide(t, svc, customer, quote.QuoteID, "partner_assigned", time.Minute, 0, 0)
	if err := svc.Store().UpsertPartnerLocation(ctx, store.UpsertPartnerLocationInput{PartnerID: *ride.PartnerID, LastLat: 17.4, LastLng: 78.5, LastGeohash: "tepg", IsOnline: true}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"partner_assigned", "partner_arriving", "arrived", "in_progress"} {
		if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status=$2::rider_ride_status WHERE id=$1`, ride.ID, status); err != nil {
			t.Fatal(err)
		}
		got, err := svc.GetRide(ctx, customer, ride.ID)
		if err != nil || got.CustomerLocation == nil || got.CustomerLocation.Lat != 17.4 {
			t.Fatalf("%s: missing owner location: %v", status, err)
		}
		if _, err := svc.GetRide(ctx, uuid.New(), ride.ID); err == nil {
			t.Fatal("non-owner read permitted")
		}
	}
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_partner_locations SET updated_at=NOW()-INTERVAL '3 minutes' WHERE partner_id=$1`, *ride.PartnerID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store().SetPartnerOnlineFlag(ctx, *ride.PartnerID, false); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetRide(ctx, customer, ride.ID)
	if err != nil || got.CustomerLocation != nil {
		t.Fatal("duty toggle must not revive stale location", err)
	}
	if err := svc.Store().UpsertPartnerLocation(ctx, store.UpsertPartnerLocationInput{PartnerID: *ride.PartnerID, LastLat: 17.4, LastLng: 78.5, LastGeohash: "tepg"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status='completed' WHERE id=$1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetRide(ctx, customer, ride.ID)
	if err != nil || got.CustomerLocation != nil {
		t.Fatal("completed ride exposed partner location", err)
	}
}

func TestCustomerLocationFresh(t *testing.T) {
	now := time.Now()
	good := store.PartnerLocation{LastLat: 17.4, LastLng: 78.5, LastGeohash: "test", UpdatedAt: now}
	if !customerLocationFresh(&good, now) {
		t.Fatal("recent fix hidden")
	}
	for _, age := range []time.Duration{-time.Second, 121 * time.Second} {
		loc := good
		loc.UpdatedAt = now.Add(-age)
		if customerLocationFresh(&loc, now) {
			t.Fatal("stale/future fix visible")
		}
	}
	for _, lat := range []float64{91, -91, math.NaN(), math.Inf(1)} {
		loc := good
		loc.LastLat = lat
		if customerLocationFresh(&loc, now) {
			t.Fatal("invalid coordinate visible")
		}
	}
	good.LastGeohash = ""
	if customerLocationFresh(&good, now) || customerLocationFresh(nil, now) {
		t.Fatal("placeholder visible")
	}
}
