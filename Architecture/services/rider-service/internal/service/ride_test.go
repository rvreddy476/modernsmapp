package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// TestValidRideTransition_TableDriven exhaustively walks the spec table.
func TestValidRideTransition_TableDriven(t *testing.T) {
	allowed := map[string][]string{
		"requested":         {"searching_partner", "cancelled_by_customer", "cancelled_by_admin", "expired", "failed"},
		"searching_partner": {"partner_assigned", "cancelled_by_customer", "cancelled_by_admin", "expired", "failed"},
		"partner_assigned":  {"partner_arriving", "cancelled_by_customer", "cancelled_by_partner", "cancelled_by_admin", "failed"},
		"partner_arriving":  {"arrived", "cancelled_by_customer", "cancelled_by_partner", "cancelled_by_admin", "failed"},
		"arrived":           {"otp_verified", "cancelled_by_customer", "cancelled_by_partner", "cancelled_by_admin", "failed"},
		"otp_verified":      {"in_progress", "failed"},
		"in_progress":       {"completed", "cancelled_by_customer", "cancelled_by_partner", "cancelled_by_admin", "failed"},
	}
	for from, tos := range allowed {
		for _, to := range tos {
			if err := validRideTransition(from, to); err != nil {
				t.Errorf("expected %s -> %s allowed; got %v", from, to, err)
			}
		}
	}
	rejected := [][2]string{
		{"requested", "in_progress"},
		{"requested", "completed"},
		{"in_progress", "requested"},
		{"completed", "in_progress"},        // terminal
		{"cancelled_by_customer", "expired"}, // terminal
		{"otp_verified", "completed"},
		{"otp_verified", "arrived"},
		{"arrived", "in_progress"}, // must go through otp_verified
	}
	for _, pair := range rejected {
		if err := validRideTransition(pair[0], pair[1]); err == nil {
			t.Errorf("expected %s -> %s rejected; got nil", pair[0], pair[1])
		}
	}
}

func TestValidRideTransition_SameStateRejected(t *testing.T) {
	if err := validRideTransition("requested", "requested"); err == nil {
		t.Fatalf("same-state transition must be rejected")
	}
}

func TestComputeCancellationFee_BeforeAssignedZero(t *testing.T) {
	for _, status := range []string{"requested", "searching_partner", "partner_assigned"} {
		r := &store.Ride{Status: status}
		if got := computeCancellationFeePaise(r); got != 0 {
			t.Errorf("status=%s: expected 0; got %d", status, got)
		}
	}
}

func TestComputeCancellationFee_PartnerArriving(t *testing.T) {
	r := &store.Ride{Status: "partner_arriving"}
	if got := computeCancellationFeePaise(r); got != 1500 {
		t.Errorf("partner_arriving: expected 1500; got %d", got)
	}
}

func TestComputeCancellationFee_Arrived(t *testing.T) {
	r := &store.Ride{Status: "arrived"}
	if got := computeCancellationFeePaise(r); got != 5000 {
		t.Errorf("arrived: expected 5000; got %d", got)
	}
}

func TestComputeCancellationFee_InProgressProrated(t *testing.T) {
	est := 100.0
	r := &store.Ride{Status: "in_progress", EstimatedFare: &est}
	if got := computeCancellationFeePaise(r); got != 1000 {
		t.Errorf("10%% of ₹100 should be 1000 paise; got %d", got)
	}
	// Cap at ₹100 even when 10% exceeds.
	bigEst := 5000.0
	r2 := &store.Ride{Status: "in_progress", EstimatedFare: &bigEst}
	if got := computeCancellationFeePaise(r2); got != 10000 {
		t.Errorf("cap should be 10000; got %d", got)
	}
}

func TestComputeCancellationFee_TerminalReturnsZero(t *testing.T) {
	for _, status := range []string{"completed", "cancelled_by_customer", "expired", "failed"} {
		r := &store.Ride{Status: status}
		if got := computeCancellationFeePaise(r); got != 0 {
			t.Errorf("terminal status %s: expected 0; got %d", status, got)
		}
	}
}

func TestGenerateOTPAndHash_RoundTrip(t *testing.T) {
	plain, hash, err := generateOTPAndHash()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(plain) != 4 {
		t.Fatalf("OTP must be 4 digits; got %q", plain)
	}
	for _, ch := range plain {
		if ch < '0' || ch > '9' {
			t.Fatalf("OTP must be all digits; got %q", plain)
		}
	}
	if !strings.HasPrefix(hash, "r1$") {
		t.Fatalf("hash must be versioned; got %q", hash)
	}
}

func TestGenerateOTPAndHash_DistinctEachCall(t *testing.T) {
	a, _, _ := generateOTPAndHash()
	b, _, _ := generateOTPAndHash()
	// 1 in 10000 chance of collision; a single observation is fine.
	if a == b {
		t.Logf("two OTPs collided (rare but possible): %q", a)
	}
	// Hashes always distinct due to random salt.
	_, ha, _ := generateOTPAndHash()
	_, hb, _ := generateOTPAndHash()
	if ha == hb {
		t.Fatalf("two hashes collided — random salt missing?")
	}
}

func TestEarningsSince_ReturnsRecentWindow(t *testing.T) {
	now := time.Now().UTC()
	if today := earningsSince("today"); today.After(now) {
		t.Fatalf("today must be <= now")
	}
	week := earningsSince("week")
	if now.Sub(week) < 6*24*time.Hour {
		t.Fatalf("week must span at least 6 days back; got %v", now.Sub(week))
	}
	month := earningsSince("month")
	if now.Sub(month) < 25*24*time.Hour {
		t.Fatalf("month must span at least 25 days back; got %v", now.Sub(month))
	}
}

// --- Integration-style tests (TEST_PG_DSN gated) -------------------------

// TestRateRide_ServiceLayer_RequiresCompleted is a small integration-style
// test that uses the real Postgres-backed Service to confirm the service
// layer rejects ratings on non-completed rides.
func TestRateRide_ServiceLayer_RequiresCompleted(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	cust := uuid.New()
	r, err := svc.CreateRide(context.Background(), cust, CreateRideRequest{
		PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59,
		DropAddress: "D", DropLat: 12.93, DropLng: 77.62,
		VehicleType: "auto", CityID: &blr.ID,
		IdempotencyKey: "ride-rate-pre-001",
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	err = svc.RateRide(context.Background(), cust, r.ID, RateRideRequest{Rating: 5})
	if err == nil || !strings.Contains(err.Error(), "only completed") {
		t.Fatalf("expected only-completed rejection; got %v", err)
	}
}

// TestCancelRide_ServiceLayer_BeforeAssignedZeroFee verifies the wallet is
// not hit when the cancellation fee is zero (cancel right after request).
func TestCancelRide_ServiceLayer_BeforeAssignedZeroFee(t *testing.T) {
	svc, walletMock, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	cust := uuid.New()
	r, err := svc.CreateRide(context.Background(), cust, CreateRideRequest{
		PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59,
		DropAddress: "D", DropLat: 12.93, DropLng: 77.62,
		VehicleType: "auto", CityID: &blr.ID,
		IdempotencyKey: "ride-cancel-zero-001",
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	if _, err := svc.CancelRide(context.Background(), cust, r.ID, "customer", CancelRideRequest{Reason: "changed mind"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(walletMock.Debits()) != 0 {
		t.Fatalf("zero-fee cancel must not hit wallet")
	}
}
