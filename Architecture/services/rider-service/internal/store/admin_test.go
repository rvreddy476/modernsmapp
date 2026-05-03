package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestAdmin_Dashboard_BasicShape — counts return without error.
func TestAdmin_Dashboard_BasicShape(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	out, err := s.AdminDashboardCounts(ctx)
	if err != nil {
		t.Fatalf("AdminDashboardCounts: %v", err)
	}
	// All counters should be non-negative.
	if out.TotalPartners < 0 || out.OpenComplaints < 0 || out.OpenSafetyIncidents < 0 {
		t.Error("dashboard counters must be non-negative")
	}
}

// TestAdmin_ListPartners_StatusFilter — filter narrows result.
func TestAdmin_ListPartners_StatusFilter(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	_, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uid,
		PartnerType: "individual_driver",
		FullName:    "Filter Test",
		Phone:       "9876512300",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	rows, err := s.ListPartners(ctx, PartnerListFilter{Status: "draft", Limit: 100})
	if err != nil {
		t.Fatalf("ListPartners: %v", err)
	}
	for _, r := range rows {
		if r.Status != "draft" {
			t.Errorf("expected draft; got %s", r.Status)
		}
	}
}

// TestAdmin_ListPartners_QueryFilter — name search matches.
func TestAdmin_ListPartners_QueryFilter(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	_, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uid,
		PartnerType: "individual_driver",
		FullName:    "Unique Quartz Name",
		Phone:       "9000000123",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	rows, err := s.ListPartners(ctx, PartnerListFilter{Query: "Quartz", Limit: 100})
	if err != nil {
		t.Fatalf("ListPartners: %v", err)
	}
	found := false
	for _, r := range rows {
		if strings.Contains(r.FullName, "Quartz") {
			found = true
		}
	}
	if !found {
		t.Error("query filter did not return expected partner")
	}
}

// TestAdmin_ApprovePartner — status transition to approved + verified.
func TestAdmin_ApprovePartner(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uid,
		PartnerType: "individual_driver",
		FullName:    "Approve Test",
		Phone:       "9000099999",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	if err := s.SetPartnerApprovedAt(ctx, p.ID); err != nil {
		t.Fatalf("SetPartnerApprovedAt: %v", err)
	}
	got, _ := s.GetPartner(ctx, p.ID)
	if got.Status != "approved" {
		t.Errorf("status = %s; want approved", got.Status)
	}
	if got.KYCStatus != "approved" {
		t.Errorf("kyc_status = %s; want approved", got.KYCStatus)
	}
	if got.ApprovedAt == nil {
		t.Error("approved_at not stamped")
	}
}

// TestAdmin_DocumentVerifyReject — round-trip verify then reject.
func TestAdmin_DocumentVerifyReject(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uid, PartnerType: "individual_driver",
		FullName: "Doc Test", Phone: "9000088888",
	})
	doc, err := s.CreatePartnerDocument(ctx, CreatePartnerDocumentInput{
		PartnerID:    p.ID,
		DocumentType: "driving_license",
		FileURL:      "s3://test/dl.pdf",
	})
	if err != nil {
		t.Fatalf("CreatePartnerDocument: %v", err)
	}
	v, err := s.SetPartnerDocumentStatus(ctx, doc.ID, "approved", nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Status != "approved" {
		t.Errorf("status = %s; want approved", v.Status)
	}
	r := "blurry"
	rejected, err := s.SetPartnerDocumentStatus(ctx, doc.ID, "rejected", &r)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Errorf("status = %s; want rejected", rejected.Status)
	}
	if rejected.RejectionReason == nil || *rejected.RejectionReason != r {
		t.Errorf("rejection_reason mismatch")
	}
}

// TestAdmin_VehicleStatus — round-trip approve / reject vehicle.
func TestAdmin_VehicleStatus(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uid, PartnerType: "individual_driver",
		FullName: "Vehicle Owner", Phone: "9000066666",
	})
	v, err := s.CreateVehicle(ctx, CreateVehicleInput{
		PartnerID:          p.ID,
		VehicleType:        "auto",
		RegistrationNumber: "KA01TEST7777",
	})
	if err != nil {
		t.Fatalf("CreateVehicle: %v", err)
	}
	if err := s.SetVehicleStatus(ctx, v.ID, "approved"); err != nil {
		t.Fatalf("set vehicle approved: %v", err)
	}
	got, _ := s.GetVehicle(ctx, v.ID)
	if got.Status != "approved" {
		t.Errorf("vehicle status = %s; want approved", got.Status)
	}
}

// TestAdmin_ListLiveRides — only non-terminal rides return.
func TestAdmin_ListLiveRides(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	_ = helperCreateRideForCustomer(t, s, customer) // status='requested'
	rows, err := s.ListLiveRides(ctx, 50)
	if err != nil {
		t.Fatalf("ListLiveRides: %v", err)
	}
	for _, r := range rows {
		if r.Status == "completed" || r.Status == "expired" || r.Status == "failed" || strings.HasPrefix(r.Status, "cancelled_") {
			t.Errorf("live list returned terminal ride: %s", r.Status)
		}
	}
}

// TestAdmin_FareRuleCRUD — create + update round-trip.
func TestAdmin_FareRuleCRUD(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	cities, _ := s.ListActiveCities(ctx)
	if len(cities) == 0 {
		t.Skip("no seeded cities; skipping fare-rule round-trip")
	}
	cityID := cities[0].ID
	r, err := s.CreateFareRule(ctx, CreateFareRuleInput{
		CityID:          cityID,
		VehicleType:     "premium",
		BaseFare:        50,
		PerKMFare:       18,
		PerMinuteFare:   1,
		MinimumFare:     100,
		PlatformFee:     10,
		NightMultiplier: 1.25,
		PeakMultiplier:  1.5,
		CancellationFee: 50,
	})
	if err != nil {
		t.Fatalf("CreateFareRule: %v", err)
	}
	want := 22.0
	upd, err := s.UpdateFareRule(ctx, r.ID, UpdateFareRuleInput{PerKMFare: &want})
	if err != nil {
		t.Fatalf("UpdateFareRule: %v", err)
	}
	if upd.PerKMFare != want {
		t.Errorf("per_km_fare = %v; want %v", upd.PerKMFare, want)
	}
}

// TestAdmin_CreateZone — polygon WKT round-trip.
func TestAdmin_CreateZone(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	cities, _ := s.ListActiveCities(ctx)
	if len(cities) == 0 {
		t.Skip("no seeded cities")
	}
	z, err := s.CreateZone(ctx, CreateZoneInput{
		CityID:      cities[0].ID,
		Name:        "Test Zone Alpha",
		BoundaryWKT: "POLYGON((77.50 12.90, 77.55 12.90, 77.55 12.95, 77.50 12.95, 77.50 12.90))",
	})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if z.Name != "Test Zone Alpha" {
		t.Errorf("name mismatch")
	}
}

// TestAdmin_ListPaymentsByStatus — filter narrows.
func TestAdmin_ListPaymentsByStatus(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rows, err := s.ListSubscriptionPaymentsByStatus(ctx, "pending", 100, 0)
	if err != nil {
		t.Fatalf("ListSubscriptionPaymentsByStatus: %v", err)
	}
	for _, r := range rows {
		if r.Status != "pending" {
			t.Errorf("expected pending; got %s", r.Status)
		}
	}
}
