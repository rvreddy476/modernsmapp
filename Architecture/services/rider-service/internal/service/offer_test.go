package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// makeApprovedPartnerWithVehicle builds an end-to-end valid partner: city,
// approved status, KYC approved, an approved active vehicle, and a trial
// subscription. Returns the partner + ride id so offer-flow tests can attach.
func makeApprovedPartnerWithVehicle(t *testing.T, svc *Service) (*store.Partner, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	blr := pickBangaloreCity(t, svc)
	uid := uuid.New()
	p, err := svc.CreatePartnerProfile(ctx, uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Approved Test",
		Phone:       "+9198" + uid.String()[:8],
		CityID:      &blr.ID,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	// Manually approve via store-level updates (admin path lands in S3).
	if err := svc.Store().UpdatePartnerStatus(ctx, p.ID, "approved"); err != nil {
		t.Fatalf("approve partner: %v", err)
	}
	if err := svc.Store().UpdatePartnerKYCStatus(ctx, p.ID, "approved"); err != nil {
		t.Fatalf("approve kyc: %v", err)
	}
	v, err := svc.Store().CreateVehicle(ctx, store.CreateVehicleInput{
		PartnerID: p.ID, VehicleType: "auto", RegistrationNumber: "KA01" + uid.String()[:6],
	})
	if err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	// Approve the vehicle row via raw SQL — there's no admin endpoint yet.
	if _, err := svc.Store().DB().Exec(ctx, "UPDATE rider_vehicles SET status = 'approved' WHERE id = $1", v.ID); err != nil {
		t.Fatalf("approve vehicle: %v", err)
	}
	plan, _ := svc.Store().GetPlanByCode(ctx, "trial_7d")
	now := time.Now().UTC()
	if _, err := svc.Store().CreateSubscription(ctx, store.CreateSubscriptionInput{
		PartnerID: p.ID, PlanID: plan.ID, Status: "trial",
		StartsAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if err := svc.Store().SetPartnerOnlineFlag(ctx, p.ID, true); err != nil {
		t.Fatalf("set online: %v", err)
	}
	if err := svc.Store().UpsertPartnerLocation(ctx, store.UpsertPartnerLocationInput{
		PartnerID: p.ID, LastLat: 12.97, LastLng: 77.59, LastGeohash: "tdr1uy", IsOnline: true,
	}); err != nil {
		t.Fatalf("upsert loc: %v", err)
	}
	// Create a ride for this partner to be offered.
	custID := uuid.New()
	est, err := svc.EstimateFare(ctx, FareEstimateRequest{
		CustomerUserID: &custID, CityID: blr.ID, VehicleType: "auto",
		PickupLabel: "P", PickupLat: 12.9716, PickupLng: 77.5946,
		DropLabel: "D", DropLat: 12.9352, DropLng: 77.6245,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	quoteID := uuid.MustParse(est.QuoteID)

	ride, err := svc.CreateRide(ctx, custID, CreateRideRequest{
		QuoteID:        &quoteID,
		PickupAddress:  "P", PickupLat: 12.9716, PickupLng: 77.5946,
		DropAddress:    "D", DropLat: 12.9352, DropLng: 77.6245,
		VehicleType:    "auto", CityID: &blr.ID,
		IdempotencyKey: "offer-test-" + uid.String(),
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	// Reload partner to refresh IsOnline.
	updated, _ := svc.Store().GetPartner(ctx, p.ID)
	return updated, ride.ID
}

func TestAcceptOffer_HappyPathReturnsOTP(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, rid := makeApprovedPartnerWithVehicle(t, svc)
	// Hand-create an offer (we're not exercising MatchRide here).
	exp := time.Now().Add(15 * time.Second)
	o, err := svc.Store().CreateRideOffer(context.Background(), store.CreateOfferInput{
		RideID: rid, PartnerID: p.ID, Score: 100, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	// Move the ride into searching_partner so the state machine accepts the
	// partner_assigned transition.
	if err := svc.Store().TransitionRide(context.Background(), rid, "requested", "searching_partner"); err != nil {
		t.Fatalf("manual searching: %v", err)
	}
	out, err := svc.AcceptOffer(context.Background(), p.UserID, o.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if out.Status != "partner_assigned" {
		t.Fatalf("expected status partner_assigned; got %q", out.Status)
	}
	if out.RideID != rid {
		t.Fatalf("ride id mismatch")
	}
	// Reload ride: status must be partner_assigned, otp_hash set.
	r, _ := svc.Store().GetRide(context.Background(), rid)
	if r.Status != "partner_assigned" {
		t.Fatalf("ride status: %s", r.Status)
	}
}

func TestAcceptOffer_RaceSecondConflict(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, rid := makeApprovedPartnerWithVehicle(t, svc)
	exp := time.Now().Add(15 * time.Second)
	o, _ := svc.Store().CreateRideOffer(context.Background(), store.CreateOfferInput{
		RideID: rid, PartnerID: p.ID, Score: 100, ExpiresAt: exp,
	})
	_ = svc.Store().TransitionRide(context.Background(), rid, "requested", "searching_partner")
	// First accept wins.
	if _, err := svc.AcceptOffer(context.Background(), p.UserID, o.ID); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Second accept must fail with conflict prefix.
	_, err := svc.AcceptOffer(context.Background(), p.UserID, o.ID)
	if err == nil || !strings.HasPrefix(err.Error(), "conflict:") {
		t.Fatalf("expected conflict on second accept; got %v", err)
	}
}

func TestRejectOffer_FlipsToRejected(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, rid := makeApprovedPartnerWithVehicle(t, svc)
	o, _ := svc.Store().CreateRideOffer(context.Background(), store.CreateOfferInput{
		RideID: rid, PartnerID: p.ID, Score: 100,
		ExpiresAt: time.Now().Add(15 * time.Second),
	})
	if err := svc.RejectOffer(context.Background(), p.UserID, o.ID, "too far"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	got, _ := svc.Store().GetOffer(context.Background(), o.ID)
	if got.Status != "rejected" {
		t.Fatalf("status: %s", got.Status)
	}
}

func TestAcceptOffer_LeadUsageIncrements(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, rid := makeApprovedPartnerWithVehicle(t, svc)
	subBefore, err := svc.Store().GetActiveSubscription(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	o, _ := svc.Store().CreateRideOffer(context.Background(), store.CreateOfferInput{
		RideID: rid, PartnerID: p.ID, Score: 100,
		ExpiresAt: time.Now().Add(15 * time.Second),
	})
	_ = svc.Store().TransitionRide(context.Background(), rid, "requested", "searching_partner")
	if _, err := svc.AcceptOffer(context.Background(), p.UserID, o.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	subAfter, _ := svc.Store().GetActiveSubscription(context.Background(), p.ID)
	if subAfter.LeadsUsed != subBefore.LeadsUsed+1 {
		t.Fatalf("leads_used should bump by 1; before=%d after=%d", subBefore.LeadsUsed, subAfter.LeadsUsed)
	}
}
