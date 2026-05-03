package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestGetFareRule_BangaloreAuto(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	var blr *City
	for i := range cs {
		if cs[i].Name == "Bengaluru" {
			blr = &cs[i]
			break
		}
	}
	if blr == nil {
		t.Skip("Bengaluru seed missing")
	}
	rule, err := s.GetFareRule(ctx, blr.ID, "auto")
	if err != nil {
		t.Fatalf("get fare rule: %v", err)
	}
	if rule.BaseFare != 25 {
		t.Errorf("BLR auto base = %v, want 25", rule.BaseFare)
	}
	if rule.PerKMFare != 12 {
		t.Errorf("BLR auto per_km = %v, want 12", rule.PerKMFare)
	}
}

func TestGetFareRule_AllVehicleTypes(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seed cities")
	}
	want := map[string]struct{ base, perKM float64 }{
		"bike":     {15, 6},
		"auto":     {25, 12},
		"mini_cab": {50, 14},
		"sedan":    {70, 16},
		"suv":      {100, 20},
		"premium":  {150, 25},
	}
	for vt, expect := range want {
		rule, err := s.GetFareRule(ctx, cs[0].ID, vt)
		if err != nil {
			t.Errorf("get %s: %v", vt, err)
			continue
		}
		if rule.BaseFare != expect.base {
			t.Errorf("%s base = %v want %v", vt, rule.BaseFare, expect.base)
		}
		if rule.PerKMFare != expect.perKM {
			t.Errorf("%s per_km = %v want %v", vt, rule.PerKMFare, expect.perKM)
		}
	}
}

func TestGetFareRule_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.GetFareRule(context.Background(), uuid.New(), "auto")
	if !errors.Is(err, ErrFareRuleNotFound) {
		t.Fatalf("expected ErrFareRuleNotFound; got %v", err)
	}
}
