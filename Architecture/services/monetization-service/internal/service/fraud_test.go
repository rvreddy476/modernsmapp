package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Self-subscription tests
// ---------------------------------------------------------------------------

func TestSelfSubscription_Blocked(t *testing.T) {
	userID := uuid.New()
	err := BlockSelfSubscription(userID, userID)
	if err == nil {
		t.Fatal("expected error for self-subscription")
	}
}

func TestSelfSubscription_DifferentUsers_OK(t *testing.T) {
	err := BlockSelfSubscription(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Minimum payout — the one rule, the one the withdrawal path calls
// ---------------------------------------------------------------------------

func TestMinimumPayout(t *testing.T) {
	var svc Service
	cases := []struct {
		name   string
		amount int64
		want   error
	}{
		{"one paise under Rs 100", 9_999, ErrMinimumPayoutNotMet},
		{"Rs 50", 5_000, ErrMinimumPayoutNotMet},
		{"exactly Rs 100", 10_000, nil},
		{"Rs 500", 50_000, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := svc.EnforceMinimumPayout(tc.amount)
			if !errors.Is(got, tc.want) {
				t.Fatalf("EnforceMinimumPayout(%d) = %v, want %v", tc.amount, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TDS — pure rule, extracted from DeductTDS
// ---------------------------------------------------------------------------
//
// Section 194-O: 10% once cumulative GROSS for the financial year,
// including the payout being priced, exceeds Rs 30,000. The comparison is
// on gross including this payout, so the payout that crosses the line is
// the first one taxed, in full.

func TestComputeTDS(t *testing.T) {
	cases := []struct {
		name        string
		gross       int64
		yearlyGross int64
		want        int64
	}{
		{"first payout of the year", 100_000, 0, 0},
		{"lands exactly on the threshold", 100_000, 2_900_000, 0},
		{"one paise over the threshold", 100_001, 2_900_000, 10_000},
		{"already over the threshold", 500_000, 3_000_000, 50_000},
		{"far over the threshold", 12_345, 10_000_000, 1_234},
		{"zero gross", 0, 10_000_000, 0},
		{"negative gross", -1, 10_000_000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeTDS(tc.gross, tc.yearlyGross); got != tc.want {
				t.Fatalf("ComputeTDS(%d, %d) = %d, want %d", tc.gross, tc.yearlyGross, got, tc.want)
			}
		})
	}
}

func TestComputeTDSThresholdIsThirtyThousandRupees(t *testing.T) {
	if TDSThresholdPaise != 3_000_000 {
		t.Fatalf("TDSThresholdPaise = %d, want 3,000,000 (Rs 30,000)", TDSThresholdPaise)
	}
}
