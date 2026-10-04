package cancelrules

import (
	"testing"

	"github.com/google/uuid"
)

func ip(v int) *int { return &v }

// The Hyderabad seed's placeholders.
var seed = []Rule{
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Stage: StageUnassigned, FeePaise: 0, Allowed: true},
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Stage: StageAssigned, MinutesBeforeLT: ip(60), FeePaise: 15000, Allowed: true},
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Stage: StageAssigned, MinutesBeforeLT: ip(180), FeePaise: 7500, Allowed: true, SortOrder: 1},
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Stage: StageAssigned, FeePaise: 0, Allowed: true, SortOrder: 2},
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000005"), Stage: StageEnRoute, FeePaise: 15000, Allowed: true},
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Stage: StageArrived, FeePaise: 20000, Allowed: true},
	{ID: uuid.MustParse("00000000-0000-0000-0000-000000000007"), Stage: StageInProgress, Allowed: false},
}

func TestFeeTiers(t *testing.T) {
	cat := uuid.New()
	const paid = 59900
	for _, c := range []struct {
		status  string
		left    int
		allowed bool
		fee     int64
		rule    string
	}{
		{"pending_payment", 10, true, 0, "not_paid"},
		{"confirmed", 10, true, 0, "free_before_assignment"},
		{"assigned", 181, true, 0, "free"},
		{"assigned", 180, true, 0, "free"}, // exactly 3 h out is still free
		{"assigned", 179, true, 7500, "lt_3h"},
		{"assigned", 61, true, 7500, "lt_3h"},
		{"assigned", 60, true, 7500, "lt_3h"},
		{"assigned", 59, true, 15000, "lt_1h"},
		{"assigned", -5, true, 15000, "lt_1h"},
		{"en_route", 500, true, 15000, "en_route"},
		{"arrived", 0, true, 20000, "arrived"},
		{"in_progress", 0, false, 0, "in_progress"},
	} {
		stage, ok := Stage(c.status)
		if !ok {
			t.Fatalf("%s has no stage", c.status)
		}
		v := Decide(seed, cat, stage, c.left, paid)
		if v.Allowed != c.allowed || v.FeePaise != c.fee || v.Rule != c.rule || (v.Allowed && c.status != "pending_payment" && v.RefundPaise != paid-c.fee) {
			t.Errorf("%s %d min: %+v, want allowed=%t fee=%d rule=%s", c.status, c.left, v, c.allowed, c.fee, c.rule)
		}
	}
	for _, s := range []string{"completed", "cancelled", "expired", "awaiting_extras_payment", "pro_no_show"} {
		if _, ok := Stage(s); ok {
			t.Errorf("%s is cancellable", s)
		}
	}
}

func TestFeeCappedAndCategoryFirst(t *testing.T) {
	cat := uuid.New()
	if v := Decide(seed, cat, StageArrived, 0, 10000); v.FeePaise != 10000 || v.RefundPaise != 0 {
		t.Fatalf("fee above paid: %+v", v)
	}
	special := append([]Rule{{ID: uuid.New(), CategoryID: &cat, Stage: StageArrived, FeePaise: 5000, Allowed: true, SortOrder: 9}}, seed...)
	if v := Decide(special, cat, StageArrived, 0, 59900); v.FeePaise != 5000 {
		t.Fatalf("category rule not first: %+v", v)
	}
	if v := Decide(special, uuid.New(), StageArrived, 0, 59900); v.FeePaise != 20000 {
		t.Fatalf("another category's rule applied: %+v", v)
	}
	if v := Decide(nil, cat, StageAssigned, 10, 59900); !v.Allowed || v.FeePaise != 0 || v.RefundPaise != 59900 {
		t.Fatalf("no rules must be free: %+v", v)
	}
}
