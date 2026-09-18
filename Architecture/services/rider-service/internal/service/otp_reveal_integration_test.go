package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCustomerRide_RevealsOTPOnlyWhileAssigned pins the one place the ride
// OTP leaves the service: the customer's own ride views, from assignment
// until the OTP is used. Without this the captain can never start a ride
// from the app (the customer has nothing to read out).
func TestCustomerRide_RevealsOTPOnlyWhileAssigned(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayPeakIST, &cust, "auto", "")
	ride, _ := assignedRide(t, svc, cust, q.QuoteID, "partner_assigned", time.Minute, 0, 0)

	plain, hash, sealed, err := svc.generateOTPAndHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().DB().Exec(ctx, `
		UPDATE rider_rides SET otp_code = $2, otp_encrypted = $3, otp_expires_at = NOW() + INTERVAL '30 minutes'
		WHERE id = $1`, ride.ID, hash, sealed); err != nil {
		t.Fatal(err)
	}

	for _, status := range []string{"partner_assigned", "partner_arriving", "arrived"} {
		if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = $2::rider_ride_status WHERE id = $1`, ride.ID, status); err != nil {
			t.Fatal(err)
		}
		active, err := svc.GetActiveRideForCustomer(ctx, cust)
		if err != nil || active == nil {
			t.Fatalf("%s: active ride: %v %v", status, active, err)
		}
		if active.OTPCode == nil || *active.OTPCode != plain {
			t.Fatalf("%s: customer must read the OTP from the active ride, got %v", status, active.OTPCode)
		}
		one, err := svc.GetRide(ctx, cust, ride.ID)
		if err != nil {
			t.Fatal(err)
		}
		if one.OTPCode == nil || *one.OTPCode != plain {
			t.Fatalf("%s: customer must read the OTP from GET /rides/:id, got %v", status, one.OTPCode)
		}
		if _, err := svc.GetRide(ctx, uuid.New(), ride.ID); err == nil {
			t.Fatalf("%s: another user must not read the ride", status)
		}
	}

	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'in_progress' WHERE id = $1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	active, err := svc.GetActiveRideForCustomer(ctx, cust)
	if err != nil || active == nil {
		t.Fatalf("in_progress: %v %v", active, err)
	}
	if active.OTPCode != nil {
		t.Fatalf("in_progress: the OTP is spent and must not be shown, got %q", *active.OTPCode)
	}
}
