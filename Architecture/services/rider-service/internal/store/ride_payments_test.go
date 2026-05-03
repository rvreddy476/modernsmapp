package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateRidePayment_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	pay, err := s.CreateRidePayment(context.Background(), CreateRidePaymentInput{
		RideID: rid, PartnerID: pid, AmountPaise: 5500, PaymentMethod: "cash",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if pay.Status != "pending" {
		t.Fatalf("default status should be pending; got %s", pay.Status)
	}
	got, err := s.GetRidePaymentByRide(context.Background(), rid)
	if err != nil {
		t.Fatalf("get-by-ride: %v", err)
	}
	if got.AmountPaise != 5500 {
		t.Fatalf("amount round-trip: %d", got.AmountPaise)
	}
}

func TestMarkRidePaymentSucceeded_Cash(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	pay, _ := s.CreateRidePayment(context.Background(), CreateRidePaymentInput{
		RideID: rid, PartnerID: pid, AmountPaise: 5500, PaymentMethod: "cash",
	})
	out, err := s.MarkRidePaymentSucceeded(context.Background(), pay.ID, nil, nil)
	if err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	if out.Status != "succeeded" {
		t.Fatalf("status: %s", out.Status)
	}
	if out.SettledAt == nil {
		t.Fatalf("settled_at should be set")
	}
}

func TestMarkRidePaymentSucceeded_Wallet(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	pay, _ := s.CreateRidePayment(context.Background(), CreateRidePaymentInput{
		RideID: rid, PartnerID: pid, AmountPaise: 7700, PaymentMethod: "wallet",
	})
	walletTxn := uuid.New()
	out, err := s.MarkRidePaymentSucceeded(context.Background(), pay.ID, &walletTxn, nil)
	if err != nil {
		t.Fatalf("mark wallet succeeded: %v", err)
	}
	if out.WalletTxnID == nil || *out.WalletTxnID != walletTxn {
		t.Fatalf("wallet txn id not set: %+v", out.WalletTxnID)
	}
}

func TestIncrementSubscriptionLeadsUsed(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Lead Cap", Phone: "+919801000010", CityID: &cs[0].ID,
	})
	plan, _ := s.GetPlanByCode(ctx, "plus_299")
	now := time.Now().UTC()
	_, err := s.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: p.ID, PlanID: plan.ID, Status: "active",
		StartsAt: now, ExpiresAt: now.Add(720 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	got, err := s.IncrementSubscriptionLeadsUsed(ctx, p.ID)
	if err != nil {
		t.Fatalf("inc: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected leads_used=1; got %d", got)
	}
	got2, _ := s.IncrementSubscriptionLeadsUsed(ctx, p.ID)
	if got2 != 2 {
		t.Fatalf("expected leads_used=2; got %d", got2)
	}
}

func TestIncrementPartnerCancelled_RecomputesRate(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Rate Test", Phone: "+919801000011",
	})
	if err := s.IncrementPartnerCancelled(ctx, p.ID); err != nil {
		t.Fatalf("inc: %v", err)
	}
	got, _ := s.GetPartner(ctx, p.ID)
	if got.TotalRidesCancelled != 1 {
		t.Fatalf("cancellations: %d", got.TotalRidesCancelled)
	}
	if got.CancellationRate != 100 {
		t.Fatalf("rate should be 100%% with 0 completed; got %v", got.CancellationRate)
	}
}
