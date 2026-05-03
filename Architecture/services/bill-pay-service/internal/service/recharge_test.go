package service

import (
	"context"
	"testing"

	"github.com/atpost/bill-pay-service/internal/setu"
	"github.com/google/uuid"
)

func TestRechargeMobile_HappyPath(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	// The mock's DetectOperatorCircle returns ('airtel','KA') for phones
	// starting with '8'. We need a provider keyed on that operator.
	h.seedProvider(t, "airtel", "mobile_prepaid", "Airtel")

	res, err := h.svc.RechargeMobile(ctx, uid, RechargeMobileRequest{
		Phone:          "8123456789",
		AmountPaise:    24900,
		PaymentMethod:  "wallet",
		IdempotencyKey: "rch-1",
	})
	if err != nil {
		t.Fatalf("recharge: %v", err)
	}
	if res.Status != "submitted" {
		t.Fatalf("expected submitted; got %q", res.Status)
	}
	subs := h.setu.Submissions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 setu submission; got %d", len(subs))
	}
	if subs[0].Identifier != "8123456789" {
		t.Fatalf("expected phone forwarded to setu; got %q", subs[0].Identifier)
	}
}

func TestRechargeMobile_RejectsBadPhone(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	if _, err := h.svc.RechargeMobile(context.Background(), uuid.New(), RechargeMobileRequest{
		Phone: "123", AmountPaise: 100, PaymentMethod: "wallet", IdempotencyKey: "k",
	}); err == nil {
		t.Fatalf("expected validation error on bad phone")
	}
}

func TestListMobilePlans_FallsBackToLiveFetch(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	// Seed plans on the mock; cache is empty → service should call mock,
	// persist them, then return.
	h.setu.SeedPlans("jio", "MH", []setu.MobilePlan{
		{Operator: "jio", Circle: "MH", AmountPaise: 19900, ValidityDays: 28},
	})
	plans, err := h.svc.ListMobilePlans(context.Background(), "jio", "MH")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plans) != 1 || plans[0].PlanAmountPaise != 19900 {
		t.Fatalf("expected 1 plan; got %+v", plans)
	}
}

func TestDetectOperatorCircle_RejectsBadPhone(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	if _, _, err := h.svc.DetectOperatorCircle(context.Background(), ""); err == nil {
		t.Fatalf("expected validation error on empty phone")
	}
}
