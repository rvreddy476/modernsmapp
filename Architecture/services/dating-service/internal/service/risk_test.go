// Pure-function tests for the §P0-7 Phase A risk-scoring helpers.
// No DB needed — these cover the threshold table + each signal's
// normaliser so the formula stays anchored as Phase B fills in the
// deferred device-reuse + IP/ASN signals.
package service

import (
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
)

func TestRiskLevelForScore_Bands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		score int
		want  string
	}{
		{0, store.RiskLevelAllow},
		{30, store.RiskLevelAllow},
		{31, store.RiskLevelReduceReach},
		{50, store.RiskLevelReduceReach},
		{51, store.RiskLevelRequireRecheck},
		{65, store.RiskLevelRequireRecheck},
		{66, store.RiskLevelHideFromDiscovery},
		{75, store.RiskLevelHideFromDiscovery},
		{76, store.RiskLevelChatHold},
		{85, store.RiskLevelChatHold},
		{86, store.RiskLevelAdminReview},
		{95, store.RiskLevelAdminReview},
		{96, store.RiskLevelSuspend},
		{100, store.RiskLevelSuspend},
	}
	for _, tc := range cases {
		got := riskLevelForScore(tc.score)
		if got != tc.want {
			t.Errorf("score=%d got %q want %q", tc.score, got, tc.want)
		}
	}
}

func TestPhotoApprovalContrib_Caps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		approved, rejected   int
		wantApp, wantRej     float64
	}{
		{0, 0, 0, 0},
		{1, 0, 1.0 / 3, 0},
		{3, 0, 1.0, 0},
		{10, 0, 1.0, 0}, // saturates
		{0, 3, 0, 1.0},
		{0, 10, 0, 1.0},
		{1, 1, 1.0 / 3, 1.0 / 3},
	}
	for _, tc := range cases {
		a, r := photoApprovalContrib(tc.approved, tc.rejected, 0)
		if !nearly(a, tc.wantApp) || !nearly(r, tc.wantRej) {
			t.Errorf("photoApproval approved=%d rejected=%d -> (%.3f,%.3f) want (%.3f,%.3f)",
				tc.approved, tc.rejected, a, r, tc.wantApp, tc.wantRej)
		}
	}
}

func TestReportQualityContrib(t *testing.T) {
	t.Parallel()
	// No reports → 0.
	if got := reportQualityContrib(0, 0); got != 0 {
		t.Errorf("zero reports: got %f want 0", got)
	}
	// One raw report (no moderator action) → 1/15.
	if got := reportQualityContrib(1, 0); !nearly(got, 1.0/15) {
		t.Errorf("1 raw report: got %f want %f", got, 1.0/15)
	}
	// One actioned report counts 3x → 3/15.
	if got := reportQualityContrib(1, 1); !nearly(got, 3.0/15) {
		t.Errorf("1 actioned report: got %f want %f", got, 3.0/15)
	}
	// 5 actioned reports saturate.
	if got := reportQualityContrib(5, 5); got != 1.0 {
		t.Errorf("5 actioned: got %f want 1.0", got)
	}
	// 100 unactioned reports also saturate.
	if got := reportQualityContrib(100, 0); got != 1.0 {
		t.Errorf("100 raw: got %f want 1.0", got)
	}
}

func TestBlockRateContrib(t *testing.T) {
	t.Parallel()
	if got := blockRateContrib(0); got != 0 {
		t.Errorf("zero blocks: got %f want 0", got)
	}
	if got := blockRateContrib(25); !nearly(got, 0.5) {
		t.Errorf("25 blocks: got %f want 0.5", got)
	}
	if got := blockRateContrib(50); got != 1.0 {
		t.Errorf("50 blocks: got %f want 1.0", got)
	}
	if got := blockRateContrib(500); got != 1.0 {
		t.Errorf("500 blocks: got %f want 1.0", got)
	}
}

func TestSparkVelocityContrib(t *testing.T) {
	t.Parallel()
	if got := sparkVelocityContrib(0); got != 0 {
		t.Errorf("zero sparks: got %f want 0", got)
	}
	if got := sparkVelocityContrib(5); !nearly(got, 0.2) {
		t.Errorf("5 sparks: got %f want 0.2", got)
	}
	if got := sparkVelocityContrib(25); got != 1.0 {
		t.Errorf("25 sparks: got %f want 1.0", got)
	}
	if got := sparkVelocityContrib(1000); got != 1.0 {
		t.Errorf("1000 sparks: got %f want 1.0", got)
	}
}

func TestProfileCompleteness(t *testing.T) {
	t.Parallel()
	// nil profile = 0.
	if got := profileCompleteness(nil); got != 0 {
		t.Errorf("nil profile: got %f want 0", got)
	}
	// All eight onboarding fields populated → 1.0.
	bd := time.Date(1995, 6, 15, 0, 0, 0, 0, time.UTC)
	g := "f"
	city := "Bengaluru"
	country := "IN"
	occ := "eng"
	edu := "btech"
	p := &store.Profile{
		Bio:           "hi",
		Gender:        &g,
		BirthDate:     &bd,
		City:          &city,
		Country:       &country,
		Occupation:    &occ,
		Education:     &edu,
		LanguagePrefs: []string{"en", "hi"},
	}
	if got := profileCompleteness(p); got != 1.0 {
		t.Errorf("full profile: got %f want 1.0", got)
	}
	// Just bio → 1/8.
	p2 := &store.Profile{Bio: "hi"}
	if got := profileCompleteness(p2); !nearly(got, 1.0/8) {
		t.Errorf("just bio: got %f want %f", got, 1.0/8)
	}
}

// nearly is a small float epsilon comparator for the normalisers.
func nearly(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
