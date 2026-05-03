package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// helperCreateRideForCustomer makes a minimal rider_rides row tied to the
// given customer, returning its id. Used by complaint + safety tests that
// need a foreign-key target without exercising the full ride lifecycle.
func helperCreateRideForCustomer(t *testing.T, s *Store, customerID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r, err := s.CreateRide(ctx, CreateRideInput{
		CustomerUserID: customerID,
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

// TestComplaint_CreateAndFetch — round-trip create + GetComplaint.
func TestComplaint_CreateAndFetch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	rideID := helperCreateRideForCustomer(t, s, customer)
	desc := "driver was rude"
	c, err := s.CreateComplaint(ctx, CreateComplaintInput{
		RideID:      rideID,
		CustomerID:  customer,
		Category:    "driver_behavior",
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("CreateComplaint: %v", err)
	}
	if c.Status != "open" {
		t.Errorf("status = %s; want open", c.Status)
	}

	got, err := s.GetComplaint(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetComplaint: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("id mismatch")
	}
	if got.Description == nil || *got.Description != desc {
		t.Errorf("description round-trip failed")
	}
}

// TestComplaint_StatusTransitions — open -> under_review -> resolved.
func TestComplaint_StatusTransitions(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	rideID := helperCreateRideForCustomer(t, s, customer)
	c, err := s.CreateComplaint(ctx, CreateComplaintInput{
		RideID:     rideID,
		CustomerID: customer,
		Category:   "fare_dispute",
	})
	if err != nil {
		t.Fatalf("CreateComplaint: %v", err)
	}

	// open -> under_review
	c2, err := s.UpdateComplaintStatus(ctx, UpdateComplaintStatusInput{
		ComplaintID: c.ID, Status: "under_review", ResolvedBy: admin,
	})
	if err != nil {
		t.Fatalf("UpdateComplaintStatus -> under_review: %v", err)
	}
	if c2.Status != "under_review" {
		t.Errorf("status = %s; want under_review", c2.Status)
	}
	if c2.ResolvedBy != nil {
		t.Errorf("under_review should not stamp resolved_by yet")
	}

	// under_review -> resolved
	note := "refunded"
	c3, err := s.UpdateComplaintStatus(ctx, UpdateComplaintStatusInput{
		ComplaintID: c.ID, Status: "resolved", Note: &note, ResolvedBy: admin,
	})
	if err != nil {
		t.Fatalf("UpdateComplaintStatus -> resolved: %v", err)
	}
	if c3.Status != "resolved" {
		t.Errorf("status = %s; want resolved", c3.Status)
	}
	if c3.ResolvedBy == nil || *c3.ResolvedBy != admin {
		t.Errorf("resolved_by mismatch: %v", c3.ResolvedBy)
	}
	if c3.ResolvedAt == nil {
		t.Errorf("resolved_at not stamped")
	}
}

// TestComplaint_ListByCustomer — only this customer's rows return.
func TestComplaint_ListByCustomer(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	a := uuid.New()
	b := uuid.New()
	rideA := helperCreateRideForCustomer(t, s, a)
	rideB := helperCreateRideForCustomer(t, s, b)
	_, _ = s.CreateComplaint(ctx, CreateComplaintInput{RideID: rideA, CustomerID: a, Category: "safety"})
	_, _ = s.CreateComplaint(ctx, CreateComplaintInput{RideID: rideB, CustomerID: b, Category: "safety"})

	rowsA, err := s.ListComplaintsByCustomer(ctx, a, 50)
	if err != nil {
		t.Fatalf("ListComplaintsByCustomer: %v", err)
	}
	for _, r := range rowsA {
		if r.CustomerID != a {
			t.Errorf("got complaint for customer %s but expected %s", r.CustomerID, a)
		}
	}
}

// TestComplaint_ListAdminFilter — status filter narrows the result.
func TestComplaint_ListAdminFilter(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	admin := uuid.New()
	r1 := helperCreateRideForCustomer(t, s, customer)
	r2 := helperCreateRideForCustomer(t, s, customer)
	c1, _ := s.CreateComplaint(ctx, CreateComplaintInput{RideID: r1, CustomerID: customer, Category: "safety"})
	_, _ = s.CreateComplaint(ctx, CreateComplaintInput{RideID: r2, CustomerID: customer, Category: "fare_dispute"})
	_, _ = s.UpdateComplaintStatus(ctx, UpdateComplaintStatusInput{
		ComplaintID: c1.ID, Status: "resolved", ResolvedBy: admin,
	})

	open, err := s.ListComplaints(ctx, "open", 100, 0)
	if err != nil {
		t.Fatalf("ListComplaints open: %v", err)
	}
	for _, r := range open {
		if r.Status != "open" {
			t.Errorf("expected open; got %s", r.Status)
		}
	}
	resolved, err := s.ListComplaints(ctx, "resolved", 100, 0)
	if err != nil {
		t.Fatalf("ListComplaints resolved: %v", err)
	}
	for _, r := range resolved {
		if r.Status != "resolved" {
			t.Errorf("expected resolved; got %s", r.Status)
		}
	}
}

// TestComplaint_RejectsBadStatus — UpdateComplaintStatus refuses unknown values.
func TestComplaint_RejectsBadStatus(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	rideID := helperCreateRideForCustomer(t, s, customer)
	c, _ := s.CreateComplaint(ctx, CreateComplaintInput{
		RideID: rideID, CustomerID: customer, Category: "other",
	})
	if _, err := s.UpdateComplaintStatus(ctx, UpdateComplaintStatusInput{
		ComplaintID: c.ID, Status: "garbage", ResolvedBy: uuid.New(),
	}); err == nil {
		t.Error("expected error on unknown status")
	}
}

// TestComplaint_CountOpen — counter respects open + under_review union.
func TestComplaint_CountOpen(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer := uuid.New()
	rideID := helperCreateRideForCustomer(t, s, customer)
	_, _ = s.CreateComplaint(ctx, CreateComplaintInput{RideID: rideID, CustomerID: customer, Category: "other"})

	n, err := s.CountOpenComplaints(ctx)
	if err != nil {
		t.Fatalf("CountOpenComplaints: %v", err)
	}
	if n < 1 {
		t.Errorf("count = %d; want >=1", n)
	}
}
