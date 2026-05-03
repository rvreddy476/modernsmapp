package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestSafety_CreateAndAcknowledge — open -> acknowledged round-trip.
func TestSafety_CreateAndAcknowledge(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)

	cust := customer
	rideRef := rid
	in, err := s.CreateSafetyIncident(ctx, CreateSafetyIncidentInput{
		RideID:     &rideRef,
		CustomerID: &cust,
		Kind:       "sos_triggered",
		Severity:   "critical",
		Metadata:   []byte(`{"trigger":"customer_app"}`),
	})
	if err != nil {
		t.Fatalf("CreateSafetyIncident: %v", err)
	}
	if in.Status != "open" {
		t.Errorf("status = %s; want open", in.Status)
	}
	ack, err := s.AcknowledgeSafetyIncident(ctx, in.ID, admin)
	if err != nil {
		t.Fatalf("AcknowledgeSafetyIncident: %v", err)
	}
	if ack.Status != "acknowledged" {
		t.Errorf("status = %s; want acknowledged", ack.Status)
	}
	if ack.AcknowledgedBy == nil || *ack.AcknowledgedBy != admin {
		t.Errorf("acknowledged_by mismatch")
	}
}

// TestSafety_Resolve — write resolution note to metadata.
func TestSafety_Resolve(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)
	cust := customer
	rideRef := rid
	in, _ := s.CreateSafetyIncident(ctx, CreateSafetyIncidentInput{
		RideID:     &rideRef,
		CustomerID: &cust,
		Kind:       "sos_triggered",
		Severity:   "high",
	})
	res, err := s.ResolveSafetyIncident(ctx, in.ID, admin, "false alarm")
	if err != nil {
		t.Fatalf("ResolveSafetyIncident: %v", err)
	}
	if res.Status != "resolved" {
		t.Errorf("status = %s; want resolved", res.Status)
	}
	if res.ResolvedBy == nil || *res.ResolvedBy != admin {
		t.Errorf("resolved_by mismatch")
	}
}

// TestSafety_ResolveTwice_NoOp — already-resolved row returns not-found.
func TestSafety_ResolveTwice_NoOp(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)
	cust := customer
	rideRef := rid
	in, _ := s.CreateSafetyIncident(ctx, CreateSafetyIncidentInput{
		RideID: &rideRef, CustomerID: &cust, Kind: "sos_triggered",
	})
	if _, err := s.ResolveSafetyIncident(ctx, in.ID, admin, "first"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, err := s.ResolveSafetyIncident(ctx, in.ID, admin, "second"); err == nil {
		t.Error("expected error on second resolve")
	}
}

// TestSafety_CountOpen — counter counts open + acknowledged.
func TestSafety_CountOpen(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)
	cust := customer
	rideRef := rid
	_, _ = s.CreateSafetyIncident(ctx, CreateSafetyIncidentInput{
		RideID: &rideRef, CustomerID: &cust, Kind: "sos_triggered",
	})
	n, err := s.CountOpenSafetyIncidents(ctx)
	if err != nil {
		t.Fatalf("CountOpenSafetyIncidents: %v", err)
	}
	if n < 1 {
		t.Errorf("count = %d; want >=1", n)
	}
}

// TestSafety_ListByStatus — filter narrows results.
func TestSafety_ListByStatus(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)
	cust := customer
	rideRef := rid

	in1, _ := s.CreateSafetyIncident(ctx, CreateSafetyIncidentInput{
		RideID: &rideRef, CustomerID: &cust, Kind: "sos_triggered",
	})
	_, _ = s.CreateSafetyIncident(ctx, CreateSafetyIncidentInput{
		RideID: &rideRef, CustomerID: &cust, Kind: "sos_triggered",
	})
	_, _ = s.AcknowledgeSafetyIncident(ctx, in1.ID, admin)

	openOnly, err := s.ListSafetyIncidents(ctx, "open", 100, 0)
	if err != nil {
		t.Fatalf("ListSafetyIncidents open: %v", err)
	}
	for _, r := range openOnly {
		if r.Status != "open" {
			t.Errorf("expected open; got %s", r.Status)
		}
	}
}

// --- Share tokens ----------------------------------------------------------

// TestShareToken_RoundTrip — create, lookup, view-bump, expiry.
func TestShareToken_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)
	tok := "abcdef0123456789abcdef0123456789"
	expires := time.Now().Add(24 * time.Hour)
	stored, err := s.CreateShareToken(ctx, tok, rid, customer, expires)
	if err != nil {
		t.Fatalf("CreateShareToken: %v", err)
	}
	if stored.Token != tok {
		t.Errorf("token mismatch")
	}

	got, err := s.LookupShareToken(ctx, tok)
	if err != nil {
		t.Fatalf("LookupShareToken: %v", err)
	}
	if got.RideID != rid {
		t.Errorf("ride id mismatch")
	}
	if err := s.MarkShareTokenViewed(ctx, tok); err != nil {
		t.Fatalf("MarkShareTokenViewed: %v", err)
	}
	got2, _ := s.LookupShareToken(ctx, tok)
	if got2.ViewCount != got.ViewCount+1 {
		t.Errorf("view_count not incremented: before %d after %d", got.ViewCount, got2.ViewCount)
	}
	if got2.LastViewedAt == nil {
		t.Errorf("last_viewed_at not stamped")
	}
}

// TestShareToken_Expired — past-expiry lookup returns ErrShareTokenExpired.
func TestShareToken_Expired(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	rid := helperCreateRideForCustomer(t, s, customer)
	tok := "expired00000000000000000000000a"
	_, _ = s.CreateShareToken(ctx, tok, rid, customer, time.Now().Add(-1*time.Hour))
	_, err := s.LookupShareToken(ctx, tok)
	if err == nil {
		t.Error("expected expired error")
	}
}

// TestShareToken_NotFound — unknown token surfaces ErrShareTokenNotFound.
func TestShareToken_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	if _, err := s.LookupShareToken(context.Background(), "nope"); err == nil {
		t.Error("expected not_found")
	}
}

// --- Trusted contact ------------------------------------------------------

// TestTrustedContact_Upsert — insert + update via the same upsert.
func TestTrustedContact_Upsert(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uid := uuid.New()
	rel := "spouse"
	t1, err := s.UpsertTrustedContact(ctx, UpsertTrustedContactInput{
		UserID:              uid,
		ContactName:         "Asha",
		ContactPhone:        "+91 98765 43210",
		ContactRelationship: &rel,
		ShareLocationOnSOS:  true,
	})
	if err != nil {
		t.Fatalf("Upsert insert: %v", err)
	}
	if t1.ContactName != "Asha" {
		t.Errorf("name = %s; want Asha", t1.ContactName)
	}
	t2, err := s.UpsertTrustedContact(ctx, UpsertTrustedContactInput{
		UserID:             uid,
		ContactName:        "Bhavna",
		ContactPhone:       "+91 90000 00000",
		ShareLocationOnSOS: false,
	})
	if err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if t2.ContactName != "Bhavna" {
		t.Errorf("name not updated: %s", t2.ContactName)
	}
	if t2.ShareLocationOnSOS {
		t.Error("share flag not updated")
	}
}

// TestTrustedContact_NotFound — fresh user surfaces ErrTrustedContactNotFound.
func TestTrustedContact_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	if _, err := s.GetTrustedContact(context.Background(), uuid.New()); err == nil {
		t.Error("expected not_found")
	}
}
