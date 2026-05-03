package jobs

import (
	"testing"

	"github.com/atpost/rider-service/internal/store"
)

// TestComputeFraudScore_Clean verifies a partner with zero signal stays at 0.
func TestComputeFraudScore_Clean(t *testing.T) {
	in := store.FraudInputs{
		RidesAssigned: 100, RidesCompleted: 100,
	}
	score, reasons := ComputeFraudScore(in, FraudThresholds{})
	if score != 0 {
		t.Errorf("clean partner score = %v, want 0", score)
	}
	if len(reasons) != 0 {
		t.Errorf("expected no reasons, got %v", reasons)
	}
}

// TestComputeFraudScore_AcceptThenCancelOnly checks the +10 bonus when
// the ratio exceeds 15%.
func TestComputeFraudScore_AcceptThenCancelOnly(t *testing.T) {
	in := store.FraudInputs{
		RidesAssigned: 100, AcceptThenCancel: 16,
	}
	score, reasons := ComputeFraudScore(in, FraudThresholds{})
	if score != 10 {
		t.Errorf("score = %v, want 10", score)
	}
	if len(reasons) == 0 {
		t.Errorf("expected reason for accept-then-cancel")
	}
}

// TestComputeFraudScore_BelowThreshold verifies just-under thresholds
// don't fire.
func TestComputeFraudScore_BelowThreshold(t *testing.T) {
	in := store.FraudInputs{
		RidesAssigned: 100, AcceptThenCancel: 15,
	}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score != 0 {
		t.Errorf("score = %v, want 0 (15%% is exactly at threshold)", score)
	}
}

// TestComputeFraudScore_CancelRatioOnly checks the +15 bonus.
func TestComputeFraudScore_CancelRatioOnly(t *testing.T) {
	in := store.FraudInputs{
		RidesAssigned: 100, RidesCancelledByP: 26,
	}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score != 15 {
		t.Errorf("score = %v, want 15", score)
	}
}

// TestComputeFraudScore_Complaints checks +5 per complaint.
func TestComputeFraudScore_Complaints(t *testing.T) {
	in := store.FraudInputs{
		RidesAssigned: 50, ComplaintsCount: 4,
	}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score != 20 {
		t.Errorf("score = %v, want 20 (4 × 5)", score)
	}
}

// TestComputeFraudScore_Capped100 verifies the score is capped.
func TestComputeFraudScore_Capped100(t *testing.T) {
	in := store.FraudInputs{
		RidesAssigned:        100,
		AcceptThenCancel:     50,
		RidesCancelledByP:    50,
		ComplaintsCount:      30, // +150 alone
		SafetyIncidentsCount: 5,
		ExpiredDocsActive:    10,
	}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score != 100 {
		t.Errorf("score = %v, want 100 (cap)", score)
	}
}

// TestComputeFraudScore_AtAutoSuspendThreshold sanity-checks the boundary.
func TestComputeFraudScore_AtAutoSuspendThreshold(t *testing.T) {
	// 16 complaints × 5 = 80, plus 1 safety incident × 20 = 100; way past 90.
	in := store.FraudInputs{ComplaintsCount: 16, SafetyIncidentsCount: 1}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score < 90 {
		t.Errorf("score = %v, want >= 90 for auto-suspend", score)
	}
}

// TestComputeFraudScore_ZeroAssignedNoRatios verifies divide-by-zero guard.
func TestComputeFraudScore_ZeroAssignedNoRatios(t *testing.T) {
	in := store.FraudInputs{
		AcceptThenCancel: 5, // RidesAssigned=0 means no ratio bonus
		ComplaintsCount:  3, // +15
	}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score != 15 {
		t.Errorf("score = %v, want 15 (only complaints contribute)", score)
	}
}

// TestComputeFraudScore_CustomThresholds verifies the threshold knobs
// drive the math (the production wiring uses defaults).
func TestComputeFraudScore_CustomThresholds(t *testing.T) {
	in := store.FraudInputs{ComplaintsCount: 2}
	score, _ := ComputeFraudScore(in, FraudThresholds{PerComplaint: 25})
	if score != 50 {
		t.Errorf("score = %v, want 50 (custom 25 × 2)", score)
	}
}

// TestComputeFraudScore_ExpiredDocsContributes checks the +10 weight per
// expired-but-online document.
func TestComputeFraudScore_ExpiredDocsContributes(t *testing.T) {
	in := store.FraudInputs{ExpiredDocsActive: 3}
	score, _ := ComputeFraudScore(in, FraudThresholds{})
	if score != 30 {
		t.Errorf("score = %v, want 30 (3 × 10)", score)
	}
}
