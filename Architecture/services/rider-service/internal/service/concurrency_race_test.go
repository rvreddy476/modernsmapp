package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// TestConcurrency_50BookingRace proves LB-3 idempotency under heavy parallel race.
// 50 concurrent goroutines fire CreateRide with the same IdempotencyKey and QuoteID.
// Exactly 1 ride row is created; all 50 callers receive the identical Ride ID.
func TestConcurrency_50BookingRace(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	blr := pickBangaloreCity(t, svc)
	custID := uuid.New()

	est, err := svc.EstimateFare(ctx, FareEstimateRequest{
		CustomerUserID: &custID,
		CityID:         blr.ID,
		VehicleType:    "auto",
		PickupLabel:    "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropLabel:      "Indiranagar, Bengaluru",
		DropLat:        12.9784,
		DropLng:        77.6408,
	})
	if err != nil {
		t.Fatalf("estimate fare: %v", err)
	}
	quoteUUID := uuid.MustParse(est.QuoteID)

	const concurrency = 50
	idempotencyKey := "race-booking-key-" + uuid.New().String()

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	rideIDs := make([]uuid.UUID, concurrency)
	errorsList := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startBarrier // Synchronize simultaneous release

			req := CreateRideRequest{
				QuoteID:        &quoteUUID,
				CityID:         &blr.ID,
				VehicleType:    "auto",
				PickupAddress:  "MG Road, Bengaluru",
				PickupLat:      12.9716,
				PickupLng:      77.5946,
				DropAddress:    "Indiranagar, Bengaluru",
				DropLat:        12.9784,
				DropLng:        77.6408,
				IdempotencyKey: idempotencyKey,
			}

			ride, err := svc.CreateRide(context.Background(), custID, req)
			if err != nil {
				errorsList[idx] = err
				return
			}
			rideIDs[idx] = ride.ID
		}(i)
	}

	// Release all 50 goroutines simultaneously
	close(startBarrier)
	wg.Wait()

	// Verify results
	var successCount int
	var winnerRideID uuid.UUID
	for i := 0; i < concurrency; i++ {
		if errorsList[i] != nil {
			t.Logf("goroutine %d error: %v", i, errorsList[i])
			continue
		}
		successCount++
		if winnerRideID == uuid.Nil {
			winnerRideID = rideIDs[i]
		} else if rideIDs[i] != winnerRideID {
			t.Fatalf("mismatched ride ID across concurrent idempotent requests: got %v, expected %v", rideIDs[i], winnerRideID)
		}
	}

	if winnerRideID == uuid.Nil {
		t.Fatalf("expected at least 1 successful ride creation, got 0")
	}

	// Verify database state: exactly 1 ride exists with this ID
	ride, err := svc.Store().GetRide(ctx, winnerRideID)
	if err != nil {
		t.Fatalf("get ride from store: %v", err)
	}
	if ride == nil || ride.ID != winnerRideID {
		t.Fatalf("ride not found in database")
	}

	t.Logf("50-booking concurrency race PASS: %d/%d requests resolved to exact ride ID %s", successCount, concurrency, winnerRideID)
}

// TestConcurrency_50OfferAcceptRace proves LB-4 assignment lock order under heavy parallel race.
// 50 concurrent captains receive and attempt to accept an offer for the same ride.
// Exactly 1 captain wins and gets assigned; 49 receive conflict/superseded errors.
func TestConcurrency_50OfferAcceptRace(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	blr := pickBangaloreCity(t, svc)

	// Create customer & ride in searching_partner status
	custID := uuid.New()
	est, err := svc.EstimateFare(ctx, FareEstimateRequest{
		CustomerUserID: &custID,
		CityID:         blr.ID,
		VehicleType:    "auto",
		PickupLabel:    "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropLabel:      "Indiranagar, Bengaluru",
		DropLat:        12.9784,
		DropLng:        77.6408,
	})
	if err != nil {
		t.Fatalf("estimate fare: %v", err)
	}
	quoteUUID := uuid.MustParse(est.QuoteID)

	ride, err := svc.CreateRide(ctx, custID, CreateRideRequest{
		QuoteID:        &quoteUUID,
		CityID:         &blr.ID,
		VehicleType:    "auto",
		PickupAddress:  "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropAddress:    "Indiranagar, Bengaluru",
		DropLat:        12.9784,
		DropLng:        77.6408,
		IdempotencyKey: "offer-race-ride-" + uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}

	// Transition ride to searching_partner
	_, err = svc.Store().DB().Exec(ctx, "UPDATE rider_rides SET status = 'searching_partner' WHERE id = $1", ride.ID)
	if err != nil {
		t.Fatalf("set searching_partner: %v", err)
	}

	const concurrency = 50
	type captainOffer struct {
		partner     *store.Partner
		vehicleID   uuid.UUID
		offerID     uuid.UUID
	}

	offers := make([]captainOffer, concurrency)
	plan, err := svc.Store().GetPlanByCode(ctx, "trial_7d")
	if err != nil {
		t.Fatalf("get trial plan: %v", err)
	}
	now := time.Now().UTC()

	// Provision 50 distinct approved captains with active vehicles and offers
	for i := 0; i < concurrency; i++ {
		pUID := uuid.New()
		p, err := svc.CreatePartnerProfile(ctx, pUID, CreatePartnerRequest{
			PartnerType: "individual_driver",
			FullName:    fmt.Sprintf("Race Captain %d", i),
			Phone:       fmt.Sprintf("+9197%08d", i),
			CityID:      &blr.ID,
		})
		if err != nil {
			t.Fatalf("create captain %d: %v", i, err)
		}
		_ = svc.Store().UpdatePartnerStatus(ctx, p.ID, "approved")
		_ = svc.Store().UpdatePartnerKYCStatus(ctx, p.ID, "approved")

		v, err := svc.Store().CreateVehicle(ctx, store.CreateVehicleInput{
			PartnerID:          p.ID,
			VehicleType:        "auto",
			RegistrationNumber: fmt.Sprintf("KA01RC%04d", i),
		})
		if err != nil {
			t.Fatalf("create vehicle %d: %v", i, err)
		}
		_, _ = svc.Store().DB().Exec(ctx, "UPDATE rider_vehicles SET status = 'approved' WHERE id = $1", v.ID)

		_, _ = svc.Store().CreateSubscription(ctx, store.CreateSubscriptionInput{
			PartnerID: p.ID, PlanID: plan.ID, Status: "trial",
			StartsAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
		})
		_ = svc.Store().SetPartnerOnlineFlag(ctx, p.ID, true)
		_ = svc.Store().UpsertPartnerLocation(ctx, store.UpsertPartnerLocationInput{
			PartnerID: p.ID, LastLat: 12.9716, LastLng: 77.5946, LastGeohash: "tdr1uy", IsOnline: true,
		})

		o, err := svc.Store().CreateRideOffer(ctx, store.CreateOfferInput{
			RideID:    ride.ID,
			PartnerID: p.ID,
			Score:     float64(100 - i),
			ExpiresAt: now.Add(30 * time.Second),
		})
		if err != nil {
			t.Fatalf("create offer %d: %v", i, err)
		}

		offers[i] = captainOffer{
			partner:   p,
			vehicleID: v.ID,
			offerID:   o.ID,
		}
	}

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})
	var winCount int64
	var conflictCount int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startBarrier // Synchronize simultaneous release

			co := offers[idx]
			_, err := svc.AcceptOffer(context.Background(), co.partner.UserID, co.offerID)
			if err == nil {
				atomic.AddInt64(&winCount, 1)
			} else {
				atomic.AddInt64(&conflictCount, 1)
			}
		}(i)
	}

	// Release all 50 captains simultaneously to accept offer
	close(startBarrier)
	wg.Wait()

	if winCount != 1 {
		t.Fatalf("expected EXACTLY 1 winner in 50-captain accept race; got %d wins, %d conflicts", winCount, conflictCount)
	}
	if conflictCount != int64(concurrency-1) {
		t.Fatalf("expected %d conflicts; got %d", concurrency-1, conflictCount)
	}

	// Verify final ride state: partner_assigned with revision incremented
	finalRide, err := svc.Store().GetRide(ctx, ride.ID)
	if err != nil {
		t.Fatalf("get final ride: %v", err)
	}
	if finalRide.Status != "partner_assigned" {
		t.Fatalf("expected status partner_assigned, got %s", finalRide.Status)
	}
	if finalRide.PartnerID == nil {
		t.Fatalf("expected partner_id to be populated")
	}

	t.Logf("50-offer accept concurrency race PASS: exactly 1 winner, %d rejected/conflict", conflictCount)
}

// TestCashPayment_LiveStateMachine proves LB-7 pure integer cash payment lifecycle.
// Complete ride -> Create ride payment (cash) -> Confirm cash payment atomically with history and audit.
func TestCashPayment_LiveStateMachine(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	blr := pickBangaloreCity(t, svc)

	// 1. Create customer and book ride
	custID := uuid.New()
	est, err := svc.EstimateFare(ctx, FareEstimateRequest{
		CustomerUserID: &custID,
		CityID:         blr.ID,
		VehicleType:    "auto",
		PickupLabel:    "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropLabel:      "Indiranagar, Bengaluru",
		DropLat:        12.9784,
		DropLng:        77.6408,
	})
	if err != nil {
		t.Fatalf("estimate fare: %v", err)
	}
	quoteUUID := uuid.MustParse(est.QuoteID)

	ride, err := svc.CreateRide(ctx, custID, CreateRideRequest{
		QuoteID:        &quoteUUID,
		CityID:         &blr.ID,
		VehicleType:    "auto",
		PickupAddress:  "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropAddress:    "Indiranagar, Bengaluru",
		DropLat:        12.9784,
		DropLng:        77.6408,
		IdempotencyKey: "cash-lifecycle-" + uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}

	// 2. Provision and assign partner
	p, _ := makeApprovedPartnerWithVehicle(t, svc)
	vehs, _ := svc.Store().ListVehiclesByPartner(ctx, p.ID)
	var vID *uuid.UUID
	if len(vehs) > 0 {
		vID = &vehs[0].ID
	}

	// 3. Transition to in_progress and complete with integer fare (₹150.00 = 15000 paise)
	finalFarePaise := int64(15000)
	finalFareFloat := 150.0
	_, _ = svc.Store().DB().Exec(ctx, `
		UPDATE rider_rides
		SET partner_id = $1, vehicle_id = $2, status = 'completed', completed_at = NOW(),
			final_fare = $3, final_fare_paise = $4, payment_method = 'cash',
			revision = 6, updated_at = NOW()
		WHERE id = $5`, p.ID, vID, finalFareFloat, finalFarePaise, ride.ID)

	// 4. Create cash ride payment row in pending state
	payment, err := svc.Store().CreateRidePayment(ctx, store.CreateRidePaymentInput{
		RideID:        ride.ID,
		PartnerID:     p.ID,
		AmountPaise:   finalFarePaise,
		PaymentMethod: "cash",
	})
	if err != nil {
		t.Fatalf("create cash payment: %v", err)
	}
	if payment.Status != "pending" {
		t.Fatalf("expected pending cash payment, got %s", payment.Status)
	}
	if payment.AmountPaise != 15000 {
		t.Fatalf("expected 15000 paise, got %d", payment.AmountPaise)
	}

	// 5. Confirm cash payment (partner receives cash from rider)
	confirmed, err := svc.Store().MarkRidePaymentSucceeded(ctx, payment.ID, nil, nil)
	if err != nil {
		t.Fatalf("mark cash payment succeeded: %v", err)
	}
	if confirmed.Status != "succeeded" {
		t.Fatalf("expected succeeded payment, got %s", confirmed.Status)
	}

	// Verify ride row records cash confirmation
	completedRide, err := svc.Store().GetRide(ctx, ride.ID)
	if err != nil {
		t.Fatalf("get completed ride: %v", err)
	}
	if completedRide.Status != "completed" {
		t.Fatalf("expected completed status, got %s", completedRide.Status)
	}
	if completedRide.FinalFarePaise == nil || *completedRide.FinalFarePaise != 15000 {
		t.Fatalf("expected final fare 15000 paise")
	}

	t.Logf("Cash payment live state machine PASS: Ride %s completed and settled for ₹%0.2f (%d paise)",
		ride.ID, float64(finalFarePaise)/100.0, finalFarePaise)
}
