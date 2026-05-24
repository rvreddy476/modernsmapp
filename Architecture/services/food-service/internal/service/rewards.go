package service

import (
	"context"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// PredictPrepTime wraps the store call.
func (s *Service) PredictPrepTime(ctx context.Context, restaurantID uuid.UUID) (*postgres.PrepTimePrediction, error) {
	return s.store.PredictPrepTime(ctx, restaurantID)
}

// ── Loyalty ───────────────────────────────────────────────────────────────

func (s *Service) GetLoyaltyBalance(ctx context.Context, userID uuid.UUID) (*postgres.LoyaltyBalance, error) {
	return s.store.GetLoyaltyBalance(ctx, userID)
}

// EarnPointsFromDelivery is the worker entry — call it once when an
// order moves to DELIVERED. v1 awards 1 point per ₹10 of final_amount,
// rounded down, cap 500 per order.
func (s *Service) EarnPointsFromDelivery(ctx context.Context, userID, orderID uuid.UUID, finalAmount float64) (*postgres.LoyaltyBalance, error) {
	pts := int(finalAmount) / 10
	if pts < 1 {
		pts = 1
	}
	if pts > 500 {
		pts = 500
	}
	return s.store.EarnPoints(ctx, userID, orderID, pts, "order_delivered")
}

func (s *Service) RedeemPoints(ctx context.Context, userID uuid.UUID, orderID *uuid.UUID, delta int) (*postgres.LoyaltyBalance, error) {
	return s.store.RedeemPoints(ctx, userID, orderID, delta, "redeemed_at_checkout")
}

func (s *Service) ListLoyaltyLedger(ctx context.Context, userID uuid.UUID, limit int) ([]postgres.LoyaltyLedgerRow, error) {
	return s.store.ListLoyaltyLedger(ctx, userID, limit)
}

// ── Referrals ─────────────────────────────────────────────────────────────

const referralRewardPoints = 200

func (s *Service) EnsureReferralCode(ctx context.Context, userID uuid.UUID) (string, error) {
	return s.store.EnsureReferralCode(ctx, userID)
}

func (s *Service) RecordReferral(ctx context.Context, refereeID uuid.UUID, code string) (*postgres.Referral, error) {
	return s.store.RecordReferral(ctx, refereeID, code)
}

// RewardReferralOnFirstDelivery is the worker entry — call it once per
// referee's first DELIVERED order. Awards referralRewardPoints to both
// sides. Idempotent.
func (s *Service) RewardReferralOnFirstDelivery(ctx context.Context, refereeID uuid.UUID) error {
	return s.store.MarkReferralRewarded(ctx, refereeID, referralRewardPoints)
}
