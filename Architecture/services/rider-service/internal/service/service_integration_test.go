package service

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newIntegrationService wires up a real Postgres-backed Service for the
// integration suite. Skips the test when TEST_PG_DSN is unset.
func newIntegrationService(t *testing.T) (*Service, *wallet.MockClient, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping rider service integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	mock := wallet.NewMockClient()
	svc := New(st, mock, Config{})
	return svc, mock, func() { pool.Close() }
}

// pickBangaloreCity is a small helper to find the seed city for tests.
func pickBangaloreCity(t *testing.T, svc *Service) *store.City {
	t.Helper()
	cs, err := svc.ListCities(context.Background())
	if err != nil {
		t.Fatalf("list cities: %v", err)
	}
	for i := range cs {
		if cs[i].Name == "Bengaluru" {
			return &cs[i]
		}
	}
	t.Skip("Bengaluru seed missing")
	return nil
}

func TestEstimateFare_BLRAuto5km(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	// Pickup MG Road, drop ~5km away.
	out, err := svc.EstimateFare(context.Background(), FareEstimateRequest{
		PickupLat:   12.9716,
		PickupLng:   77.5946,
		DropLat:     12.9352,
		DropLng:     77.6245,
		VehicleType: "auto",
		CityID:      blr.ID,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	// Sanity: distance is non-zero, fare clears the ₹40 minimum, paise > 0.
	if out.EstimatedDistanceKM <= 0 {
		t.Fatalf("distance should be positive: %v", out.EstimatedDistanceKM)
	}
	if out.FareEstimateINR < 40 {
		t.Fatalf("fare should clear minimum ₹40; got ₹%v", out.FareEstimateINR)
	}
	if out.FareEstimatePaise != int64(math.Round(out.FareEstimateINR*100)) {
		t.Errorf("paise / INR mismatch: %d vs %v", out.FareEstimatePaise, out.FareEstimateINR)
	}
	if out.SurgeMultiplier != 1.0 {
		t.Errorf("default surge should be 1.0; got %v", out.SurgeMultiplier)
	}
}

func TestEstimateFare_MissingCityIsInvalid(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	_, err := svc.EstimateFare(context.Background(), FareEstimateRequest{
		PickupLat: 12.97, PickupLng: 77.59, DropLat: 12.93, DropLng: 77.62,
		VehicleType: "auto",
	})
	if err == nil || !contains(err.Error(), "city_id required") {
		t.Fatalf("expected city_id required; got %v", err)
	}
}

func TestEstimateFare_UnknownVehicleTypeRejected(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	_, err := svc.EstimateFare(context.Background(), FareEstimateRequest{
		PickupLat: 12.97, PickupLng: 77.59, DropLat: 12.93, DropLng: 77.62,
		VehicleType: "boat",
		CityID:      blr.ID,
	})
	if err == nil || !contains(err.Error(), "vehicle_type") {
		t.Fatalf("expected vehicle_type validation; got %v", err)
	}
}

func TestCreatePartnerProfile_RoundTrip(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	uid := uuid.New()
	p, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Anil Kumar",
		Phone:       "+919900099001",
		CityID:      &blr.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Status != "draft" {
		t.Fatalf("expected draft status; got %s", p.Status)
	}
	// Idempotent: re-call returns the existing row.
	again, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Anil Kumar",
		Phone:       "+919900099001",
	})
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if again.ID != p.ID {
		t.Fatalf("re-create should return existing partner")
	}
}

func TestSubmitKYCDocument_TransitionsStatus(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	uid := uuid.New()
	p, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "KYC Test",
		Phone:       "+919900099002",
		CityID:      &blr.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := svc.SubmitKYCDocument(context.Background(), uid, p.ID, SubmitKYCDocumentRequest{
		DocumentType: "driving_license",
		FileURL:      "https://media.example/dl.jpg",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if doc.Status != "pending" {
		t.Fatalf("doc status: %s", doc.Status)
	}
	// Partner should have flipped to pending_verification.
	again, _ := svc.GetMyPartner(context.Background(), uid)
	if again.Status != "pending_verification" {
		t.Fatalf("partner status not transitioned: %s", again.Status)
	}
}

func TestSubmitKYCDocument_AadhaarDropsRawNumber(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	uid := uuid.New()
	p, _ := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Aadhaar Drop",
		Phone:       "+919900099003",
	})
	rawNumber := "123456789012" // simulated leak attempt
	doc, err := svc.SubmitKYCDocument(context.Background(), uid, p.ID, SubmitKYCDocumentRequest{
		DocumentType:   "aadhaar",
		DocumentNumber: &rawNumber, // service MUST drop this
		FileURL:        "https://media.example/aadhaar.jpg",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// DPDP audit: DocumentNumber must be nil after persistence for aadhaar.
	if doc.DocumentNumber != nil {
		t.Fatalf("DPDP violation: aadhaar document_number persisted: %v", *doc.DocumentNumber)
	}
}

func TestSubscribe_WalletPath_ActivatesAndDebits(t *testing.T) {
	svc, walletMock, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	uid := uuid.New()
	p, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Sub Wallet",
		Phone:       "+919900099100",
		CityID:      &blr.ID,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	plan, err := svc.Store().GetPlanByCode(context.Background(), "plus_299")
	if err != nil {
		t.Fatalf("plan lookup: %v", err)
	}
	res, err := svc.Subscribe(context.Background(), uid, plan.ID, "wallet", "idem-wallet-001")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Status != "active" {
		t.Fatalf("status: %s, want active", res.Status)
	}
	if res.SubscriptionID == nil {
		t.Fatalf("subscription id missing")
	}
	if res.AmountPaise != 29900 {
		t.Fatalf("amount paise: %d, want 29900", res.AmountPaise)
	}
	// Wallet mock should have been called once.
	debits := walletMock.Debits()
	if len(debits) != 1 {
		t.Fatalf("expected 1 wallet debit; got %d", len(debits))
	}
	if debits[0].UserID != p.UserID {
		t.Fatalf("debit user id mismatch")
	}
	if debits[0].AmountPaise != 29900 {
		t.Fatalf("debit amount: %d", debits[0].AmountPaise)
	}
}

func TestSubscribe_Idempotency_SameKeyReturnsSamePayment(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	uid := uuid.New()
	_, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Idem Test",
		Phone:       "+919900099101",
		CityID:      &blr.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	plan, _ := svc.Store().GetPlanByCode(context.Background(), "basic_199")
	first, err := svc.Subscribe(context.Background(), uid, plan.ID, "manual", "idem-aaa")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.Subscribe(context.Background(), uid, plan.ID, "manual", "idem-aaa")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.PaymentID != second.PaymentID {
		t.Fatalf("idempotency replay returned different payment id: %v vs %v", first.PaymentID, second.PaymentID)
	}
	bodyA, _ := json.Marshal(first)
	bodyB, _ := json.Marshal(second)
	if string(bodyA) != string(bodyB) {
		t.Errorf("idempotency replay returned different body")
	}
}

func TestSubscribe_ManualPath_StaysPending(t *testing.T) {
	svc, walletMock, cleanup := newIntegrationService(t)
	defer cleanup()
	uid := uuid.New()
	_, _ = svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Manual Test",
		Phone:       "+919900099102",
	})
	plan, _ := svc.Store().GetPlanByCode(context.Background(), "plus_299")
	res, err := svc.Subscribe(context.Background(), uid, plan.ID, "manual", "idem-manual-001")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Status != "pending" {
		t.Fatalf("status: %s, want pending", res.Status)
	}
	if res.SubscriptionID != nil {
		t.Fatalf("manual path should not activate a subscription")
	}
	if len(walletMock.Debits()) != 0 {
		t.Fatalf("manual path must not hit the wallet")
	}
}

func TestSubscribe_UPIPath_ReturnsIntent(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	uid := uuid.New()
	_, _ = svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "UPI Test",
		Phone:       "+919900099103",
	})
	plan, _ := svc.Store().GetPlanByCode(context.Background(), "basic_199")
	res, err := svc.Subscribe(context.Background(), uid, plan.ID, "upi", "idem-upi-001")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Status != "pending" {
		t.Fatalf("upi status: %s, want pending", res.Status)
	}
	if !contains(res.UPIIntentURL, "upi://pay?") {
		t.Fatalf("upi intent missing: %s", res.UPIIntentURL)
	}
}

func TestSubscribe_TrialPlanActivatesWithoutWallet(t *testing.T) {
	svc, walletMock, cleanup := newIntegrationService(t)
	defer cleanup()
	uid := uuid.New()
	_, _ = svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Trial Test",
		Phone:       "+919900099104",
	})
	plan, _ := svc.Store().GetPlanByCode(context.Background(), "trial_7d")
	res, err := svc.Subscribe(context.Background(), uid, plan.ID, "wallet", "idem-trial-001")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Status != "trial" {
		t.Fatalf("trial status: %s, want trial", res.Status)
	}
	if len(walletMock.Debits()) != 0 {
		t.Fatalf("zero-cost trial must not hit the wallet; got %d debits", len(walletMock.Debits()))
	}
}

func TestSubscribe_WalletDebitFailureMarksFailed(t *testing.T) {
	svc, walletMock, cleanup := newIntegrationService(t)
	defer cleanup()
	uid := uuid.New()
	_, _ = svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Fail Test",
		Phone:       "+919900099105",
	})
	plan, _ := svc.Store().GetPlanByCode(context.Background(), "plus_299")
	walletMock.FailDebit()
	_, err := svc.Subscribe(context.Background(), uid, plan.ID, "wallet", "idem-fail-001")
	if err == nil {
		t.Fatalf("expected failure")
	}
}

func TestCreateRide_Idempotent(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	blr := pickBangaloreCity(t, svc)
	cust := uuid.New()
	req := CreateRideRequest{
		PickupAddress:  "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropAddress:    "Indiranagar, Bengaluru",
		DropLat:        12.9784,
		DropLng:        77.6408,
		VehicleType:    "auto",
		CityID:         &blr.ID,
		IdempotencyKey: "ride-idem-001",
	}
	first, err := svc.CreateRide(context.Background(), cust, req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.CreateRide(context.Background(), cust, req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotency replay returned different ride id")
	}
}

// contains is a tiny strings.Contains alias to keep test bodies readable.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
