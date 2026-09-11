package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Removed in plan Phase 3B: FraudRiskScore and computeRiskScore (a score
// computed from Redis keys nothing wrote), enforceMinimumPayout (a second
// copy of EnforceMinimumPayout in payout.go) and calculateTDS (a second
// copy of the TDS rule, with a different threshold comparison from the
// one DeductTDS used). The live rules are EnforceMinimumPayout and
// ComputeTDS; the holds a withdrawal can land in are written by
// RequestPayout itself.

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
//
// Left as found: it reads a Redis key nothing writes, so it always answers
// false, and nothing calls it. The withdrawal hold that replaced it reads
// creator_ledger.created_at (payout.go).
func (s *Service) DelayedEarningsCheck(ctx context.Context, creatorID uuid.UUID) bool {
	ageKey := fmt.Sprintf("account_age_days:%s", creatorID.String())
	ageDays := 365 // default: assume established
	if val, err := s.rdb.Get(ctx, ageKey).Int(); err == nil {
		ageDays = val
	}
	return ageDays < 30
}
