package service

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationService spins up a service backed by TEST_PG_DSN. Skips when
// TEST_PG_DSN is unset (CI may run unit-only).
func integrationService(t *testing.T) (*Service, *recordingPublisher, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping safety integration test")
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
	pub := &recordingPublisher{}
	walletClient := wallet.NewMockClient()
	svc := New(st, walletClient, Config{})
	svc.SetProducer(pub)
	return svc, pub, func() { pool.Close() }
}

// TestService_SOS_EmitsEvent — TriggerSOS records an incident and emits
// EventRiderSafetySOS via the publisher.
func TestService_SOS_EmitsEvent(t *testing.T) {
	svc, pub, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	ride := helperCreateRideForCustomerService(t, svc.Store(), customer)

	res, err := svc.TriggerSOS(ctx, customer, ride, nil, nil)
	if err != nil {
		t.Fatalf("TriggerSOS: %v", err)
	}
	if res.IncidentID == uuid.Nil {
		t.Errorf("incident id zero")
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.sosCalls) == 0 {
		t.Error("expected EventRiderSafetySOS publish")
	}
}

// TestService_SOS_RejectsForeignRide — SOS for a ride owned by another user
// returns forbidden.
func TestService_SOS_RejectsForeignRide(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	owner := uuid.New()
	other := uuid.New()
	ride := helperCreateRideForCustomerService(t, svc.Store(), owner)
	if _, err := svc.TriggerSOS(ctx, other, ride, nil, nil); err == nil {
		t.Error("expected forbidden")
	}
}

// TestService_ShareToken_RoundTrip — create token, fetch redacted view.
func TestService_ShareToken_RoundTrip(t *testing.T) {
	svc, pub, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	ride := helperCreateRideForCustomerService(t, svc.Store(), customer)

	tok, err := svc.CreateShareToken(ctx, customer, ride)
	if err != nil {
		t.Fatalf("CreateShareToken: %v", err)
	}
	if len(tok.Token) != 32 {
		t.Errorf("token length = %d; want 32", len(tok.Token))
	}
	pub.mu.Lock()
	if len(pub.shareTokenCreated) == 0 {
		t.Error("expected share.token_created event")
	}
	pub.mu.Unlock()

	view, err := svc.GetSharedRide(ctx, tok.Token)
	if err != nil {
		t.Fatalf("GetSharedRide: %v", err)
	}
	if view.RideID != ride.String() {
		t.Errorf("ride id mismatch")
	}
	if view.PickupArea == "" {
		t.Error("pickup area empty")
	}
}

// TestService_TrustedContact_Upsert — set + get round-trip.
func TestService_TrustedContact_Upsert(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	if _, err := svc.SetTrustedContact(ctx, uid, SetTrustedContactRequest{
		Name:       "Asha",
		Phone:      "+91 98765 43210",
		ShareOnSOS: true,
	}); err != nil {
		t.Fatalf("SetTrustedContact: %v", err)
	}
	got, err := svc.GetTrustedContact(ctx, uid)
	if err != nil {
		t.Fatalf("GetTrustedContact: %v", err)
	}
	if got.ContactName != "Asha" {
		t.Errorf("name = %s; want Asha", got.ContactName)
	}
}

// TestService_Complaint_LifecycleWithEvents — raise + admin update emits
// the right pair of events.
func TestService_Complaint_LifecycleWithEvents(t *testing.T) {
	svc, pub, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	ride := helperCreateRideForCustomerService(t, svc.Store(), customer)

	c, err := svc.CreateComplaint(ctx, customer, ride, CreateComplaintRequest{
		Category:    "driver_behavior",
		Description: "rude",
	})
	if err != nil {
		t.Fatalf("CreateComplaint: %v", err)
	}
	pub.mu.Lock()
	if len(pub.complaintRaised) == 0 {
		t.Error("expected complaint.raised")
	}
	pub.mu.Unlock()

	if _, err := svc.UpdateComplaintStatus(ctx, c.ID, admin, UpdateComplaintStatusRequest{
		Status: "resolved", Note: "warned partner",
	}); err != nil {
		t.Fatalf("UpdateComplaintStatus: %v", err)
	}
	pub.mu.Lock()
	if len(pub.complaintUpdated) == 0 {
		t.Error("expected complaint.updated")
	}
	pub.mu.Unlock()
}

// TestService_Admin_ApprovePartner_AdminAction — emits status change + admin action.
func TestService_Admin_ApprovePartner_AdminAction(t *testing.T) {
	svc, pub, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	admin := uuid.New()
	p, err := svc.Store().CreatePartner(ctx, store.CreatePartnerInput{
		UserID: uid, PartnerType: "individual_driver",
		FullName: "Approve Me", Phone: "9000033333",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	if _, err := svc.ApprovePartner(ctx, p.ID, admin); err != nil {
		t.Fatalf("ApprovePartner: %v", err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.partnerStatus) == 0 {
		t.Error("expected partner status change event")
	}
	if len(pub.adminActions) == 0 {
		t.Error("expected admin action event")
	}
	for _, a := range pub.adminActions {
		if a.Action == "partner.approve" && a.AdminID == admin.String() {
			return
		}
	}
	t.Error("partner.approve admin action not found")
}

// TestService_Admin_VerifyDocument — verify happy path.
func TestService_Admin_VerifyDocument(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	admin := uuid.New()
	p, _ := svc.Store().CreatePartner(ctx, store.CreatePartnerInput{
		UserID: uid, PartnerType: "individual_driver",
		FullName: "Doc Owner", Phone: "9000044444",
	})
	doc, _ := svc.Store().CreatePartnerDocument(ctx, store.CreatePartnerDocumentInput{
		PartnerID:    p.ID,
		DocumentType: "driving_license",
		FileURL:      "s3://test/dl.pdf",
	})
	got, err := svc.VerifyDocument(ctx, doc.ID, admin)
	if err != nil {
		t.Fatalf("VerifyDocument: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("status = %s; want approved", got.Status)
	}
}

// TestService_Admin_RejectDocument_RequiresReason — empty reason rejected.
func TestService_Admin_RejectDocument_RequiresReason(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.RejectDocument(ctx, uuid.New(), uuid.New(), ""); err == nil {
		t.Error("expected error for empty reason")
	}
}

// TestService_Admin_VerifyVehicle — happy path.
func TestService_Admin_VerifyVehicle(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	admin := uuid.New()
	p, _ := svc.Store().CreatePartner(ctx, store.CreatePartnerInput{
		UserID: uid, PartnerType: "individual_driver",
		FullName: "V Owner", Phone: "9000055555",
	})
	v, _ := svc.Store().CreateVehicle(ctx, store.CreateVehicleInput{
		PartnerID: p.ID, VehicleType: "auto", RegistrationNumber: "KA01TEST5555",
	})
	if err := svc.VerifyVehicle(ctx, v.ID, admin); err != nil {
		t.Fatalf("VerifyVehicle: %v", err)
	}
	got, _ := svc.Store().GetVehicle(ctx, v.ID)
	if got.Status != "approved" {
		t.Errorf("status = %s; want approved", got.Status)
	}
}

// TestService_Admin_FareRuleCreate_Validates — invalid vehicle_type rejected.
func TestService_Admin_FareRuleCreate_Validates(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	ctx := context.Background()

	cities, _ := svc.Store().ListActiveCities(ctx)
	if len(cities) == 0 {
		t.Skip("no seeded cities")
	}
	if _, err := svc.CreateFareRule(ctx, uuid.New(), CreateFareRuleRequest{
		CityID:      cities[0].ID,
		VehicleType: "spaceship",
	}); err == nil {
		t.Error("expected error for invalid vehicle_type")
	}
}

// TestService_Admin_Dashboard — dashboard returns without error.
func TestService_Admin_Dashboard(t *testing.T) {
	svc, _, cleanup := integrationService(t)
	defer cleanup()
	if _, err := svc.Dashboard(context.Background()); err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
}

// helperCreateRideForCustomerService — service-side helper that mirrors the
// store-test helper. Returns the ride id only.
func helperCreateRideForCustomerService(t *testing.T, s *store.Store, customer uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r, err := s.CreateRide(ctx, store.CreateRideInput{
		CustomerUserID: customer,
		VehicleType:    "auto",
		PickupAddress:  "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropAddress:    "Whitefield, Bengaluru",
		DropLat:        12.9698,
		DropLng:        77.7500,
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	return r.ID
}
