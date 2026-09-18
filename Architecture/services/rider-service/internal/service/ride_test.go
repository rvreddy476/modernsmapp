package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/pricing"
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

// The cancellation fee is the fare rule's, owed by the customer only when
// they (or a no-show) cancel after assignment and past the rule's free
// window. The pure rule is pricing.CancellationFee; the service wires the
// ride's assigned_at and the store's rule (TestCancelRide_* below and in
// service_integration_test.go).
func TestCancellationFee_RuleAndActors(t *testing.T) {
	rule := pricing.FareRule{CancellationFeePaise: 1500, CancelFreeSeconds: 120}
	now := time.Now().UTC()
	assignedLate := now.Add(-3 * time.Minute)
	assignedJustNow := now.Add(-30 * time.Second)
	if got := pricing.CancellationFee(rule, "customer", nil, now); got != 0 {
		t.Errorf("no partner assigned: expected 0; got %d", got)
	}
	if got := pricing.CancellationFee(rule, "customer", &assignedJustNow, now); got != 0 {
		t.Errorf("inside the free window: expected 0; got %d", got)
	}
	if got := pricing.CancellationFee(rule, "customer", &assignedLate, now); got != 1500 {
		t.Errorf("customer after the free window: expected 1500; got %d", got)
	}
	if got := pricing.CancellationFee(rule, pricing.CancelNoShow, &assignedLate, now); got != 1500 {
		t.Errorf("no-show: expected 1500; got %d", got)
	}
	for _, by := range []string{"partner", "admin", "system"} {
		if got := pricing.CancellationFee(rule, by, &assignedLate, now); got != 0 {
			t.Errorf("%s cancels: expected 0; got %d", by, got)
		}
	}
}

func TestGenerateOTPAndHash_FailsClosedWithoutKeys(t *testing.T) {
	s := &Service{}
	if _, _, _, err := s.generateOTPAndHash(context.Background()); err == nil {
		t.Fatal("no sealer configured must refuse to mint an OTP")
	}
}

func TestGenerateOTPAndHash_RoundTrip(t *testing.T) {
	s := &Service{otpCrypto: testOTPCrypto(t)}
	plain, hash, enc, err := s.generateOTPAndHash(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if opened, err := s.otpCrypto.OpenOTP(context.Background(), enc); err != nil || opened != plain {
		t.Fatalf("sealed otp must open to the plaintext: %q %v", opened, err)
	}
	if len(plain) != 4 {
		t.Fatalf("OTP must be 4 digits; got %q", plain)
	}
	for _, ch := range plain {
		if ch < '0' || ch > '9' {
			t.Fatalf("OTP must be all digits; got %q", plain)
		}
	}
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "r1$") {
		t.Fatalf("hash must be versioned; got %q", hash)
	}
	if len(enc) == 0 {
		t.Fatalf("encrypted material must not be empty")
	}
}

func TestGenerateOTPAndHash_DistinctEachCall(t *testing.T) {
	s := &Service{otpCrypto: testOTPCrypto(t)}
	a, _, _, _ := s.generateOTPAndHash(context.Background())
	b, _, _, _ := s.generateOTPAndHash(context.Background())
	// 1 in 10000 chance of collision; a single observation is fine.
	if a == b {
		t.Logf("two OTPs collided (rare but possible): %q", a)
	}
	// Hashes always distinct due to random salt.
	_, ha, _, _ := s.generateOTPAndHash(context.Background())
	_, hb, _, _ := s.generateOTPAndHash(context.Background())
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
	est, err := svc.EstimateFare(context.Background(), FareEstimateRequest{
		CustomerUserID: &cust, CityID: blr.ID, VehicleType: "auto",
		PickupLabel: "P", PickupLat: 12.9716, PickupLng: 77.5946,
		DropLabel: "D", DropLat: 12.9352, DropLng: 77.6245,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	quoteID := uuid.MustParse(est.QuoteID)

	r, err := svc.CreateRide(context.Background(), cust, CreateRideRequest{
		QuoteID: &quoteID,
		PickupAddress: "P", PickupLat: 12.9716, PickupLng: 77.5946,
		DropAddress: "D", DropLat: 12.9352, DropLng: 77.6245,
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
	est, err := svc.EstimateFare(context.Background(), FareEstimateRequest{
		CustomerUserID: &cust, CityID: blr.ID, VehicleType: "auto",
		PickupLabel: "P", PickupLat: 12.9716, PickupLng: 77.5946,
		DropLabel: "D", DropLat: 12.9352, DropLng: 77.6245,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	quoteID := uuid.MustParse(est.QuoteID)

	r, err := svc.CreateRide(context.Background(), cust, CreateRideRequest{
		QuoteID: &quoteID,
		PickupAddress: "P", PickupLat: 12.9716, PickupLng: 77.5946,
		DropAddress: "D", DropLat: 12.9352, DropLng: 77.6245,
		VehicleType: "auto", CityID: &blr.ID,
		IdempotencyKey: "ride-cancel-zero-001",
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	if _, err := svc.CancelRide(context.Background(), cust, r.ID, "customer", CancelRideRequest{Reason: "changed mind", ExpectedRevision: 1}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(walletMock.Debits()) != 0 {
		t.Fatalf("zero-fee cancel must not hit wallet")
	}
}
