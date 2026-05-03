package store

import (
	"context"
	"testing"
)

func TestUpsertMobilePlan_Validation(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.UpsertMobilePlan(ctx, UpsertMobilePlanInput{}); err == nil {
		t.Fatalf("expected validation error on empty operator/circle")
	}
	if err := s.UpsertMobilePlan(ctx, UpsertMobilePlanInput{Operator: "airtel", Circle: "KA", PlanAmountPaise: 0}); err == nil {
		t.Fatalf("expected validation error on amount=0")
	}
}

func TestReplaceMobilePlans_Replaces(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()

	first := []UpsertMobilePlanInput{
		{Operator: "airtel", Circle: "KA", PlanAmountPaise: 24900},
		{Operator: "airtel", Circle: "KA", PlanAmountPaise: 49900},
	}
	if err := s.ReplaceMobilePlans(ctx, "airtel", "KA", first); err != nil {
		t.Fatalf("replace 1: %v", err)
	}
	out, _ := s.ListMobilePlans(ctx, "airtel", "KA")
	if len(out) != 2 {
		t.Fatalf("expected 2 plans after first replace; got %d", len(out))
	}

	// Now replace with one plan; old two should disappear.
	second := []UpsertMobilePlanInput{
		{Operator: "airtel", Circle: "KA", PlanAmountPaise: 99900},
	}
	if err := s.ReplaceMobilePlans(ctx, "airtel", "KA", second); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	out, _ = s.ListMobilePlans(ctx, "airtel", "KA")
	if len(out) != 1 || out[0].PlanAmountPaise != 99900 {
		t.Fatalf("replace did not actually replace: %+v", out)
	}
}
