package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestPartnerHistoryAndPrivateSupport(t *testing.T) {
	s, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	partner, _ := makeApprovedPartnerWithVehicle(t, s)
	other, _ := makeApprovedPartnerWithVehicle(t, s)
	customer := uuid.New()
	id := helperCreateRideForCustomerService(t, s.Store(), customer)
	if _, err := s.Store().DB().Exec(ctx, `UPDATE rider_rides SET partner_id=$1,status='completed',otp_code='must-not-leak' WHERE id=$2`, partner.ID, id); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListPartnerRides(ctx, partner.UserID, 50)
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("history %#v %v", rows, err)
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), "must-not-leak") {
		t.Fatal("history leaked verification code")
	}
	empty, err := s.ListPartnerRides(ctx, other.UserID, 50)
	if err != nil || len(empty) != 0 {
		t.Fatalf("another partner history %#v %v", empty, err)
	}
	if _, err = s.CreatePartnerRideSupport(ctx, other.UserID, id, "other", "Need assistance"); err == nil {
		t.Fatal("another partner submitted support")
	}
	support, err := s.CreatePartnerRideSupport(ctx, partner.UserID, id, "fare_dispute", "Please check the fare")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetComplaint(ctx, support.ID, customer, false); err == nil {
		t.Fatal("customer read private partner support")
	}
	if _, err = s.GetComplaint(ctx, support.ID, partner.UserID, false); err != nil {
		t.Fatal(err)
	}
	customerTickets, err := s.ListMyComplaints(ctx, customer, 50)
	if err != nil || len(customerTickets) != 0 {
		t.Fatalf("customer listing leaked support %#v %v", customerTickets, err)
	}
	adminTickets, err := s.ListComplaintsForAdmin(ctx, "open", 50, 0)
	if err != nil || len(adminTickets) != 1 {
		t.Fatalf("admin cannot see support %#v %v", adminTickets, err)
	}
	if _, err = s.UpdateComplaintStatus(ctx, support.ID, uuid.New(), UpdateComplaintStatusRequest{Status: "resolved", Note: "Reviewed"}); err != nil {
		t.Fatal(err)
	}
	var events int
	if err = s.Store().DB().QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type IN ('rider.partner.support.raised','rider.partner.support.updated') AND payload->>'complaint_id'=$1`, support.ID.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("durable support events=%d", events)
	}
}

func TestPartnerRideReadbackOwnershipAndRecovery(t *testing.T) {
	s, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	partner, _ := makeApprovedPartnerWithVehicle(t, s)
	other, _ := makeApprovedPartnerWithVehicle(t, s)
	customer := uuid.New()
	id := helperCreateRideForCustomerService(t, s.Store(), customer)
	_, err := s.Store().DB().Exec(ctx, `UPDATE rider_rides SET partner_id=$1,status='in_progress',revision=9,otp_code='secret-code' WHERE id=$2`, partner.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	// Losing approval must not hide a ride already accepted.
	_, err = s.Store().DB().Exec(ctx, `UPDATE rider_partners SET status='suspended' WHERE id=$1`, partner.ID)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.GetPartnerActiveRide(ctx, partner.UserID)
	if err != nil || active == nil || active.ID != id || active.Revision != 9 {
		t.Fatalf("active: %#v %v", active, err)
	}
	got, err := s.GetPartnerRide(ctx, partner.UserID, id)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), "secret-code") || strings.Contains(string(body), "otp") {
		t.Fatal("verification secret leaked")
	}
	if _, err = s.GetPartnerRide(ctx, other.UserID, id); err == nil {
		t.Fatal("other partner read ride")
	}
	if _, err = s.GetPartnerRide(ctx, customer, id); err == nil {
		t.Fatal("customer read partner-only ride")
	}
	empty, err := s.GetPartnerActiveRide(ctx, other.UserID)
	if err != nil || empty != nil {
		t.Fatalf("other active: %#v %v", empty, err)
	}
}
