package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListActivePlans_SeedsHaveFivePlusOne(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	plans, err := s.ListActivePlans(context.Background())
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	// Seed has 5 active partner plans + 1 inactive fleet plan = 5 active.
	if len(plans) < 5 {
		t.Fatalf("expected >= 5 active plans; got %d", len(plans))
	}
	want := map[string]bool{"trial_7d": false, "basic_199": false, "plus_299": false, "pro_499": false, "elite_999": false}
	for _, p := range plans {
		if _, ok := want[p.Code]; ok {
			want[p.Code] = true
		}
		if p.Code == "fleet_starter_1999" {
			t.Fatalf("fleet_starter_1999 should be inactive in seed")
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("seed plan %q missing", code)
		}
	}
}

func TestGetPlanByCode_PriceMatchesSpec(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	cases := []struct {
		code  string
		price float64
	}{
		{"trial_7d", 0},
		{"basic_199", 199},
		{"plus_299", 299},
		{"pro_499", 499},
		{"elite_999", 999},
	}
	for _, c := range cases {
		got, err := s.GetPlanByCode(context.Background(), c.code)
		if err != nil {
			t.Errorf("%s: %v", c.code, err)
			continue
		}
		if got.PriceAmount != c.price {
			t.Errorf("%s: price = %v, want %v", c.code, got.PriceAmount, c.price)
		}
	}
}

func TestCreateSubscription_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	plan, err := s.GetPlanByCode(ctx, "plus_299")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "individual_driver",
		FullName:    "Sub Test",
		Phone:       "+919900001000",
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	now := time.Now().UTC()
	sub, err := s.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: p.ID,
		PlanID:    plan.ID,
		Status:    "active",
		StartsAt:  now,
		ExpiresAt: now.AddDate(0, 0, 30),
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if sub.Status != "active" {
		t.Fatalf("status: %s", sub.Status)
	}
	active, err := s.GetActiveSubscription(ctx, p.ID)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.ID != sub.ID {
		t.Fatalf("active mismatch")
	}
}

func TestCreateSubscriptionPayment_AndVerify(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	plan, _ := s.GetPlanByCode(ctx, "basic_199")
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "individual_driver",
		FullName:    "Pay Test",
		Phone:       "+919900001100",
	})
	pay, err := s.CreateSubscriptionPayment(ctx, CreateSubscriptionPaymentInput{
		PartnerID:     p.ID,
		PlanID:        plan.ID,
		Amount:        plan.PriceAmount,
		PaymentMethod: "wallet",
		Status:        "pending",
	})
	if err != nil {
		t.Fatalf("create payment: %v", err)
	}
	if pay.Status != "pending" {
		t.Fatalf("status: %s", pay.Status)
	}
	walletTxn := uuid.New()
	verified, err := s.MarkPaymentVerified(ctx, pay.ID, &walletTxn, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.Status != "verified" || verified.WalletTxnID == nil {
		t.Fatalf("verify did not flip state: %+v", verified)
	}
}

func TestAttachPaymentProof_FlipsToSubmitted(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	plan, _ := s.GetPlanByCode(ctx, "plus_299")
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "individual_driver",
		FullName:    "Proof Test",
		Phone:       "+919900001200",
	})
	pay, _ := s.CreateSubscriptionPayment(ctx, CreateSubscriptionPaymentInput{
		PartnerID:     p.ID,
		PlanID:        plan.ID,
		Amount:        plan.PriceAmount,
		PaymentMethod: "manual",
		Status:        "pending",
	})
	attached, err := s.AttachPaymentProof(ctx, pay.ID, "https://media.example/receipt.png")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if attached.Status != "submitted" {
		t.Fatalf("status not flipped: %s", attached.Status)
	}
	if attached.PaymentProofURL == nil || *attached.PaymentProofURL == "" {
		t.Fatalf("proof url empty")
	}
}
