package matcher

import (
	"math"
	"testing"
	"time"
)

// approxOK returns true if a and b are within 1e-6 of each other. Float
// arithmetic in Score has enough additions that exact equality is brittle.
func approxOK(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// happyCandidate is a fully-eligible candidate; tests start from it and
// mutate one field at a time to exercise rules.
func happyCandidate() PartnerCandidate {
	return PartnerCandidate{
		PartnerID:          "p1",
		VehicleID:          "v1",
		VehicleType:        "auto",
		CityID:             "c1",
		PartnerStatus:      "approved",
		KYCStatus:          "approved",
		VehicleStatus:      "approved",
		SubscriptionStatus: "active",
		IsOnline:           true,
		IsUnlimitedPlan:    false,
		LeadsUsedThisMonth: 10,
		LeadAllotment:      100,
		PlanPriorityWeight: 100,
		Rating:             4.5,
		AcceptanceRate:     90.0,
		CancellationRate:   5.0,
		FraudScore:         0.0,
		DistanceKM:         2.5,
		IdleSecs:           60,
		LastSeen:           time.Now(),
	}
}

func TestScore_FormulaMatchesSpec(t *testing.T) {
	c := happyCandidate()
	got, _ := Score(c)
	// plan: 100*100 = 10000
	// rating: 4.5*20 = 90
	// accept: 90*0.5 = 45
	// cancel: -5
	// fraud: 0
	// distance: -25
	// idle: 60*0.05 = 3
	want := 10000.0 + 90 + 45 - 5 - 0 - 25 + 3
	if !approxOK(got, want) {
		t.Fatalf("score = %f; want %f", got, want)
	}
}

func TestScore_PlanPriorityDominates(t *testing.T) {
	// Elite (160) on a 5km hop should beat Basic (80) on a 1km hop. The plan
	// component (×100) outweighs the distance penalty (×10).
	elite := happyCandidate()
	elite.PlanPriorityWeight = 160
	elite.DistanceKM = 5.0
	basic := happyCandidate()
	basic.PlanPriorityWeight = 80
	basic.DistanceKM = 1.0
	es, _ := Score(elite)
	bs, _ := Score(basic)
	if es <= bs {
		t.Fatalf("elite=%f should beat basic=%f", es, bs)
	}
}

func TestScore_DistancePenalty(t *testing.T) {
	near := happyCandidate()
	near.DistanceKM = 0.5
	far := happyCandidate()
	far.DistanceKM = 5.0
	ns, _ := Score(near)
	fs, _ := Score(far)
	if ns <= fs {
		t.Fatalf("near=%f should beat far=%f", ns, fs)
	}
	delta := (ns - fs)
	// Distance delta is exactly (5.0-0.5)*10 = 45 — same plan, identical
	// everything else.
	if !approxOK(delta, 45.0) {
		t.Fatalf("distance penalty delta = %f; want 45", delta)
	}
}

func TestScore_FraudPenalty(t *testing.T) {
	clean := happyCandidate()
	clean.FraudScore = 0
	dirty := happyCandidate()
	dirty.FraudScore = 25
	cs, _ := Score(clean)
	ds, _ := Score(dirty)
	if cs <= ds {
		t.Fatalf("clean=%f should beat dirty=%f", cs, ds)
	}
	if !approxOK(cs-ds, 50.0) {
		t.Fatalf("fraud delta = %f; want 50", cs-ds)
	}
}

func TestScore_IdleBonusCapped(t *testing.T) {
	short := happyCandidate()
	short.IdleSecs = 0
	long := happyCandidate()
	long.IdleSecs = 1_000_000
	ls, _ := Score(long)
	ss, _ := Score(short)
	delta := ls - ss
	if delta != 30 {
		t.Fatalf("idle bonus must cap at 30; got delta=%f", delta)
	}
}

func TestScore_BreakdownCount(t *testing.T) {
	_, reasons := Score(happyCandidate())
	if len(reasons) != 7 {
		t.Fatalf("expected 7 components in breakdown; got %d (%v)", len(reasons), reasons)
	}
}

func TestFilter_HappyPathPasses(t *testing.T) {
	kept, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{happyCandidate()})
	if len(kept) != 1 {
		t.Fatalf("expected 1 kept; got %d (rejected: %v)", len(kept), rej)
	}
}

func TestFilter_PartnerNotApproved(t *testing.T) {
	c := happyCandidate()
	c.PartnerStatus = "pending_verification"
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonPartnerNotApproved {
		t.Fatalf("expected partner_not_approved; got %v", rej)
	}
}

func TestFilter_KYCNotApproved(t *testing.T) {
	c := happyCandidate()
	c.KYCStatus = "pending"
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonKYCNotApproved {
		t.Fatalf("expected kyc_not_approved; got %v", rej)
	}
}

func TestFilter_VehicleNotApproved(t *testing.T) {
	c := happyCandidate()
	c.VehicleStatus = "pending"
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonVehicleNotApproved {
		t.Fatalf("expected vehicle_not_approved; got %v", rej)
	}
}

func TestFilter_SubscriptionExpired(t *testing.T) {
	c := happyCandidate()
	c.SubscriptionStatus = "expired"
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonNoActiveSubscription {
		t.Fatalf("expected no_active_subscription; got %v", rej)
	}
}

func TestFilter_Offline(t *testing.T) {
	c := happyCandidate()
	c.IsOnline = false
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonOffline {
		t.Fatalf("expected offline; got %v", rej)
	}
}

func TestFilter_VehicleTypeMismatch(t *testing.T) {
	c := happyCandidate()
	c.VehicleType = "bike"
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonVehicleTypeMismatch {
		t.Fatalf("expected vehicle_type_mismatch; got %v", rej)
	}
}

func TestFilter_CityMismatch(t *testing.T) {
	c := happyCandidate()
	c.CityID = "other-city"
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonCityMismatch {
		t.Fatalf("expected city_mismatch; got %v", rej)
	}
}

func TestFilter_LeadCapExhausted(t *testing.T) {
	c := happyCandidate()
	c.IsUnlimitedPlan = false
	c.LeadAllotment = 100
	c.LeadsUsedThisMonth = 100
	_, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(rej) != 1 || rej[0] != ReasonLeadCapExhausted {
		t.Fatalf("expected lead_cap_exhausted; got %v", rej)
	}
}

func TestFilter_UnlimitedPlanIgnoresCap(t *testing.T) {
	c := happyCandidate()
	c.IsUnlimitedPlan = true
	c.LeadAllotment = 50
	c.LeadsUsedThisMonth = 9999 // way over the cap, but unlimited
	kept, rej := FilterCandidates(RideRequest{VehicleType: "auto", CityID: "c1"}, []PartnerCandidate{c})
	if len(kept) != 1 || len(rej) != 0 {
		t.Fatalf("unlimited should keep; got kept=%d rej=%v", len(kept), rej)
	}
}

func TestRank_SortedHighestFirst(t *testing.T) {
	low := happyCandidate()
	low.PlanPriorityWeight = 50
	high := happyCandidate()
	high.PlanPriorityWeight = 160
	mid := happyCandidate()
	mid.PlanPriorityWeight = 100
	out := Rank([]PartnerCandidate{low, high, mid})
	if out[0].Candidate.PlanPriorityWeight != 160 {
		t.Fatalf("first should be high (160); got %d", out[0].Candidate.PlanPriorityWeight)
	}
	if out[2].Candidate.PlanPriorityWeight != 50 {
		t.Fatalf("last should be low (50); got %d", out[2].Candidate.PlanPriorityWeight)
	}
}

func TestBatchOffer_TopFiveSelected(t *testing.T) {
	cands := make([]ScoredCandidate, 8)
	for i := range cands {
		c := happyCandidate()
		c.PartnerID = "p" + string(rune('0'+i))
		cands[i] = ScoredCandidate{Candidate: c, Score: float64(100 - i)}
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	res := BatchOffer(cands, 5, 15*time.Second, now)
	if len(res.Selected) != 5 {
		t.Fatalf("expected 5 selected; got %d", len(res.Selected))
	}
	if len(res.Remaining) != 3 {
		t.Fatalf("expected 3 remaining; got %d", len(res.Remaining))
	}
	if res.ExpiresAt != now.Add(15*time.Second) {
		t.Fatalf("expires_at wrong: %v", res.ExpiresAt)
	}
}

func TestBatchOffer_FewerThanBatch(t *testing.T) {
	cands := []ScoredCandidate{
		{Candidate: happyCandidate(), Score: 100},
		{Candidate: happyCandidate(), Score: 90},
	}
	now := time.Now()
	res := BatchOffer(cands, 5, 15*time.Second, now)
	if len(res.Selected) != 2 {
		t.Fatalf("expected 2 selected; got %d", len(res.Selected))
	}
	if len(res.Remaining) != 0 {
		t.Fatalf("expected 0 remaining; got %d", len(res.Remaining))
	}
}

func TestBatchOffer_DefaultsApplied(t *testing.T) {
	cands := make([]ScoredCandidate, 7)
	for i := range cands {
		cands[i] = ScoredCandidate{Candidate: happyCandidate(), Score: float64(i)}
	}
	now := time.Now()
	// batchSize=0 -> default 5; timer=0 -> default 15s.
	res := BatchOffer(cands, 0, 0, now)
	if len(res.Selected) != 5 {
		t.Fatalf("default batch size should be 5; got %d", len(res.Selected))
	}
	if !res.ExpiresAt.After(now) || res.ExpiresAt.Sub(now) != 15*time.Second {
		t.Fatalf("default timer should be 15s; got %v", res.ExpiresAt.Sub(now))
	}
}
