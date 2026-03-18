package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Self-subscription block
// ---------------------------------------------------------------------------

// BlockSelfSubscription returns an error if subscriberID and creatorID are the same.
func BlockSelfSubscription(subscriberID, creatorID uuid.UUID) error {
	if subscriberID == creatorID {
		return fmt.Errorf("cannot subscribe to yourself")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Subscription velocity check
// ---------------------------------------------------------------------------

// CheckSubscriptionVelocity checks if a subscriber has made more than 10
// subscriptions in the last hour. Uses Redis to track subscription counts.
func (s *Service) CheckSubscriptionVelocity(ctx context.Context, subscriberID uuid.UUID) error {
	key := fmt.Sprintf("sub_velocity:%s", subscriberID.String())

	count, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		slog.Warn("subscription velocity: redis INCR failed", "error", err)
		return nil // Fail open — don't block on Redis errors
	}

	// Set TTL on first increment
	if count == 1 {
		s.rdb.Expire(ctx, key, time.Hour)
	}

	if count > 10 {
		return fmt.Errorf("subscription velocity exceeded: %d subscriptions in the last hour", count)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Fraud risk score
// ---------------------------------------------------------------------------

// computeRiskScore calculates a fraud risk score (0-100) based on:
// - Account age in days (< 30 days = +30)
// - Transaction count in period (high volume with new account = +20)
// - Verification status (unverified = +20)
// This is a package-level function for testability.
func computeRiskScore(accountAgeDays int, transactionCount int, isVerified bool) int {
	score := 0

	// New account risk
	if accountAgeDays < 30 {
		score += 30
	} else if accountAgeDays < 90 {
		score += 10
	}

	// High volume risk (especially for new accounts)
	if transactionCount > 50 && accountAgeDays < 30 {
		score += 20
	} else if transactionCount > 100 && accountAgeDays < 90 {
		score += 15
	}

	// Unverified account risk
	if !isVerified {
		score += 20
	}

	// Cap at 100
	if score > 100 {
		score = 100
	}

	return score
}

// FraudRiskScore computes a risk score for a creator. In a real implementation,
// this would query account metadata. For now, it uses Redis-cached metadata.
func (s *Service) FraudRiskScore(ctx context.Context, creatorID uuid.UUID) int {
	// Check account age from Redis cache
	ageKey := fmt.Sprintf("account_age_days:%s", creatorID.String())
	ageDays := 365 // default: assume established
	if val, err := s.rdb.Get(ctx, ageKey).Int(); err == nil {
		ageDays = val
	}

	// Check transaction count
	txCountKey := fmt.Sprintf("tx_count_30d:%s", creatorID.String())
	txCount := 0
	if val, err := s.rdb.Get(ctx, txCountKey).Int(); err == nil {
		txCount = val
	}

	// Check verification
	verifiedKey := fmt.Sprintf("verified:%s", creatorID.String())
	isVerified := true
	if val, err := s.rdb.Get(ctx, verifiedKey).Result(); err == nil && val == "0" {
		isVerified = false
	}

	return computeRiskScore(ageDays, txCount, isVerified)
}

// ---------------------------------------------------------------------------
// Fraud review creation
// ---------------------------------------------------------------------------

// CreateFraudReview creates a fraud review in the store.
func (s *Service) CreateFraudReview(ctx context.Context, creatorID uuid.UUID, reviewType string, riskScore int) (*postgres.FraudReview, error) {
	review := &postgres.FraudReview{
		CreatorID:  creatorID,
		ReviewType: reviewType,
		RiskScore:  riskScore,
		Status:     "pending",
	}
	return s.store.CreateFraudReview(ctx, review)
}

// ---------------------------------------------------------------------------
// Delayed earnings check
// ---------------------------------------------------------------------------

// DelayedEarningsCheck returns true if a creator account is less than 30 days old,
// meaning earnings should be held for 7 days before becoming available.
func (s *Service) DelayedEarningsCheck(ctx context.Context, creatorID uuid.UUID) bool {
	ageKey := fmt.Sprintf("account_age_days:%s", creatorID.String())
	ageDays := 365 // default: assume established
	if val, err := s.rdb.Get(ctx, ageKey).Int(); err == nil {
		ageDays = val
	}
	return ageDays < 30
}

// ---------------------------------------------------------------------------
// Minimum payout enforcement
// ---------------------------------------------------------------------------

// enforceMinimumPayout returns an error if amountPaise is below the minimum
// payout threshold of 10000 paise (INR 100).
func enforceMinimumPayout(amountPaise int64) error {
	const minPayoutPaise int64 = 10000 // INR 100
	if amountPaise < minPayoutPaise {
		return fmt.Errorf("minimum payout is %d paise (INR 100), requested %d", minPayoutPaise, amountPaise)
	}
	return nil
}

// ---------------------------------------------------------------------------
// TDS calculation
// ---------------------------------------------------------------------------

// calculateTDS computes the net amount and TDS deduction for a payout.
// TDS of 10% is applied if yearly earnings exceed 3,000,000 paise (INR 30,000).
// All amounts are in paise (int64).
func calculateTDS(grossPaise int64, yearlyEarningsSoFarPaise int64) (netPaise, tdsPaise int64) {
	const tdsThresholdPaise int64 = 3_000_000 // INR 30,000
	const tdsRateBps int64 = 1000             // 10% = 1000 bps

	if yearlyEarningsSoFarPaise < tdsThresholdPaise {
		return grossPaise, 0
	}

	tdsPaise = grossPaise * tdsRateBps / 10000
	netPaise = grossPaise - tdsPaise
	return netPaise, tdsPaise
}
