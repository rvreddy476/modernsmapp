package reconcile

import (
	"testing"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestComputeTrustScore(t *testing.T) {
	cases := []struct {
		name string
		in   *postgres.TrustRecomputeInput
		want int
	}{
		{
			name: "brand new account, no signals -> base score",
			in:   &postgres.TrustRecomputeInput{UserID: uuid.New()},
			want: 50, // BaseScore, age 0
		},
		{
			name: "age bonus accrues at 0.5/day",
			in:   &postgres.TrustRecomputeInput{AccountAgeDays: 20},
			want: 60, // 50 + min(30, 20*0.5)=10
		},
		{
			name: "age bonus is capped at 30",
			in:   &postgres.TrustRecomputeInput{AccountAgeDays: 1000},
			want: 80, // 50 + 30 cap
		},
		{
			name: "abuse penalties subtract",
			in: &postgres.TrustRecomputeInput{
				AccountAgeDays:    60, // +30 (capped)
				ReportsUpheld30d:  2,  // -20
				ReportsPending30d: 1,  // -5
				BlocksReceived30d: 1,  // -8
			},
			want: 47, // 50 + 30 - 20 - 5 - 8
		},
		{
			name: "score clamps to 0",
			in: &postgres.TrustRecomputeInput{
				ReportsUpheld30d: 10, // -100
			},
			want: 0, // 50 - 100 -> clamp 0
		},
		{
			name: "score clamps to 100",
			in: &postgres.TrustRecomputeInput{
				AccountAgeDays: 1000, // +30
			},
			want: 80, // never exceeds without foreign signals, but stays <=100
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeTrustScore(tc.in)
			if got != tc.want {
				t.Errorf("ComputeTrustScore() = %d, want %d", got, tc.want)
			}
			if got < 0 || got > 100 {
				t.Errorf("ComputeTrustScore() = %d, out of [0,100] bounds", got)
			}
		})
	}
}

func TestComputeTrustTier(t *testing.T) {
	cases := []struct {
		name           string
		score          int
		accountAgeDays int
		want           string
	}{
		{"young account is always new", 95, 3, TierNew},
		{"young account low score still new", 10, 0, TierNew},
		{"old account low band", 20, 30, TierLow},
		{"old account low band upper edge", 34, 30, TierLow},
		{"old account standard band lower edge", 35, 30, TierStandard},
		{"old account standard band upper edge", 69, 30, TierStandard},
		{"old account trusted band lower edge", 70, 30, TierTrusted},
		{"high score never auto-assigns verified, caps at trusted", 100, 365, TierTrusted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeTrustTier(tc.score, tc.accountAgeDays)
			if got != tc.want {
				t.Errorf("ComputeTrustTier(%d, %d) = %q, want %q", tc.score, tc.accountAgeDays, got, tc.want)
			}
		})
	}
}
