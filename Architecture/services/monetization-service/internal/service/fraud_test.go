package service

import (
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
// Minimum payout tests
// ---------------------------------------------------------------------------

func TestMinimumPayout_BelowThreshold(t *testing.T) {
	err := enforceMinimumPayout(5000) // INR 50 = 5000 paise
	if err == nil {
		t.Fatal("expected error for amount below INR 100")
	}
}

func TestMinimumPayout_AtThreshold(t *testing.T) {
	err := enforceMinimumPayout(10000) // INR 100 = 10000 paise
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMinimumPayout_AboveThreshold(t *testing.T) {
	err := enforceMinimumPayout(50000) // INR 500 = 50000 paise
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TDS calculation tests
// ---------------------------------------------------------------------------

func TestTDSDeduction_BelowThreshold(t *testing.T) {
	// Yearly earnings < INR 30,000 (3000000 paise) -> no TDS
	net, tds := calculateTDS(100000, 0) // INR 1000 gross, 0 yearly so far
	if tds != 0 {
		t.Fatalf("expected no TDS, got %d", tds)
	}
	if net != 100000 {
		t.Fatalf("expected net=100000, got %d", net)
	}
}

func TestTDSDeduction_AboveThreshold(t *testing.T) {
	// Yearly earnings > INR 30,000 -> 10% TDS
	net, tds := calculateTDS(500000, 3000000) // INR 5000 gross, INR 30000 yearly
	if tds != 50000 {                         // 10% of 500000
		t.Fatalf("expected TDS=50000, got %d", tds)
	}
	if net != 450000 {
		t.Fatalf("expected net=450000, got %d", net)
	}
}

func TestTDSDeduction_ExactThreshold(t *testing.T) {
	// Yearly earnings exactly at threshold -> no TDS (must exceed)
	net, tds := calculateTDS(100000, 2900000) // yearly so far = 2900000, below 3000000
	if tds != 0 {
		t.Fatalf("expected no TDS at exact threshold boundary, got %d", tds)
	}
	if net != 100000 {
		t.Fatalf("expected net=100000, got %d", net)
	}
}

// ---------------------------------------------------------------------------
// Fraud risk score tests
// ---------------------------------------------------------------------------

func TestFraudRiskScore_NewAccount(t *testing.T) {
	// New account (< 30 days) should have elevated risk
	score := computeRiskScore(15, 5, false) // 15 days old, 5 transactions, not verified
	if score < 30 {
		t.Fatalf("expected risk >= 30 for new account, got %d", score)
	}
}

func TestFraudRiskScore_Established(t *testing.T) {
	score := computeRiskScore(365, 100, true) // 1 year old, 100 txns, verified
	if score > 20 {
		t.Fatalf("expected low risk for established account, got %d", score)
	}
}

func TestFraudRiskScore_NewAccountHighVolume(t *testing.T) {
	// New account with high volume should get maximum risk
	score := computeRiskScore(10, 60, false) // 10 days, 60 txns, unverified
	// 30 (new) + 20 (high vol) + 20 (unverified) = 70
	if score < 60 {
		t.Fatalf("expected high risk for new high-volume unverified account, got %d", score)
	}
}

func TestFraudRiskScore_VerifiedNewAccount(t *testing.T) {
	// Verified but new account
	score := computeRiskScore(20, 3, true) // 20 days, 3 txns, verified
	// 30 (new) + 0 (low vol) + 0 (verified) = 30
	if score != 30 {
		t.Fatalf("expected score=30 for new verified low-volume account, got %d", score)
	}
}

func TestFraudRiskScore_ZeroDays(t *testing.T) {
	score := computeRiskScore(0, 0, false) // brand new, no txns, unverified
	// 30 (new) + 0 (no txns) + 20 (unverified) = 50
	if score != 50 {
		t.Fatalf("expected score=50 for brand new unverified account, got %d", score)
	}
}
