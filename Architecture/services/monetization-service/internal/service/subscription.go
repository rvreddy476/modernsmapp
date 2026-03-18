package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// gracePeriodDuration is the length of the grace period after max retries.
const gracePeriodDuration = 7 * 24 * time.Hour

// maxRenewalRetries is the number of payment retries before entering grace.
const maxRenewalRetries = 3

// PauseSubscription pauses a subscription until the given duration, with ownership verification.
func (s *Service) PauseSubscription(ctx context.Context, subscriberID, subscriptionID uuid.UUID, pauseUntil time.Time) error {
	sub, err := s.store.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	if sub.SubscriberID != subscriberID {
		return fmt.Errorf("SUBSCRIPTION_NOT_OWNED")
	}
	if sub.Status != "active" {
		return fmt.Errorf("SUBSCRIPTION_NOT_ACTIVE")
	}

	oldStatus := sub.Status
	if err := s.store.PauseSubscription(ctx, subscriptionID, pauseUntil); err != nil {
		return fmt.Errorf("pause subscription: %w", err)
	}

	if err := s.store.InsertSubscriptionEvent(ctx, subscriptionID, "paused", oldStatus, "paused", nil); err != nil {
		slog.Warn("failed to log subscription event", "error", err)
	}

	return nil
}

// ResumeSubscription reactivates a paused subscription, with ownership verification.
func (s *Service) ResumeSubscription(ctx context.Context, subscriberID, subscriptionID uuid.UUID) error {
	sub, err := s.store.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	if sub.SubscriberID != subscriberID {
		return fmt.Errorf("SUBSCRIPTION_NOT_OWNED")
	}
	if sub.Status != "paused" {
		return fmt.Errorf("SUBSCRIPTION_NOT_PAUSED")
	}

	oldStatus := sub.Status
	if err := s.store.ResumeSubscription(ctx, subscriptionID); err != nil {
		return fmt.Errorf("resume subscription: %w", err)
	}

	if err := s.store.InsertSubscriptionEvent(ctx, subscriptionID, "resumed", oldStatus, "active", nil); err != nil {
		slog.Warn("failed to log subscription event", "error", err)
	}

	return nil
}

// CancelAtPeriodEnd marks a subscription to be cancelled at the end of the current billing period.
func (s *Service) CancelAtPeriodEnd(ctx context.Context, subscriberID, subscriptionID uuid.UUID, reason string) error {
	sub, err := s.store.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	if sub.SubscriberID != subscriberID {
		return fmt.Errorf("SUBSCRIPTION_NOT_OWNED")
	}
	if sub.Status != "active" && sub.Status != "past_due" && sub.Status != "grace" {
		return fmt.Errorf("SUBSCRIPTION_CANNOT_CANCEL")
	}

	oldStatus := sub.Status
	if err := s.store.CancelAtPeriodEnd(ctx, subscriptionID, reason); err != nil {
		return fmt.Errorf("cancel at period end: %w", err)
	}

	meta, _ := json.Marshal(map[string]string{"reason": reason})
	if err := s.store.InsertSubscriptionEvent(ctx, subscriptionID, "cancel_at_period_end", oldStatus, "cancelled_at_period_end", meta); err != nil {
		slog.Warn("failed to log subscription event", "error", err)
	}

	return nil
}

// CancelImmediately cancels a subscription right away and publishes an entitlement change event.
func (s *Service) CancelImmediately(ctx context.Context, subscriberID, subscriptionID uuid.UUID, reason string) error {
	sub, err := s.store.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	if sub.SubscriberID != subscriberID {
		return fmt.Errorf("SUBSCRIPTION_NOT_OWNED")
	}
	if sub.Status == "cancelled" || sub.Status == "expired" {
		return fmt.Errorf("SUBSCRIPTION_ALREADY_CANCELLED")
	}

	oldStatus := sub.Status
	if err := s.store.SetSubscriptionStatus(ctx, subscriptionID, "cancelled"); err != nil {
		return fmt.Errorf("cancel subscription: %w", err)
	}

	meta, _ := json.Marshal(map[string]string{"reason": reason})
	if err := s.store.InsertSubscriptionEvent(ctx, subscriptionID, "cancelled_immediately", oldStatus, "cancelled", meta); err != nil {
		slog.Warn("failed to log subscription event", "error", err)
	}

	return nil
}

// UpgradeSubscription changes a subscription to a new tier, prorating the difference.
// Credits the remaining days at the old price and charges the new tier price.
func (s *Service) UpgradeSubscription(ctx context.Context, subscriberID, subscriptionID, newTierID uuid.UUID) error {
	sub, err := s.store.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	if sub.SubscriberID != subscriberID {
		return fmt.Errorf("SUBSCRIPTION_NOT_OWNED")
	}
	if sub.Status != "active" {
		return fmt.Errorf("SUBSCRIPTION_NOT_ACTIVE")
	}

	newTier, err := s.store.GetCreatorTier(ctx, newTierID)
	if err != nil {
		return err
	}
	if newTier == nil {
		return fmt.Errorf("TIER_NOT_FOUND")
	}
	if !newTier.IsActive {
		return fmt.Errorf("TIER_INACTIVE")
	}
	if newTier.CreatorID != sub.CreatorID {
		return fmt.Errorf("TIER_CREATOR_MISMATCH")
	}

	// Calculate proration: credit for remaining days on old tier
	now := time.Now()
	totalDays := sub.CurrentPeriodEnd.Sub(sub.CurrentPeriodStart).Hours() / 24
	remainingDays := sub.CurrentPeriodEnd.Sub(now).Hours() / 24
	if remainingDays < 0 {
		remainingDays = 0
	}

	var creditPaise int64
	if totalDays > 0 {
		creditPaise = int64(float64(sub.PricePaise) * remainingDays / totalDays)
	}

	// New tier price (in paise) for the full period
	newPricePaise := newTier.PricePaise

	// Net charge = new price - prorated credit
	var netChargePaise int64
	if newPricePaise > creditPaise {
		netChargePaise = newPricePaise - creditPaise
	}

	// If there's a net charge, attempt to collect it
	if netChargePaise > 0 {
		chargeErr := s.store.ChargeAndCredit(
			ctx,
			sub.SubscriberID.String(),
			sub.CreatorID.String(),
			netChargePaise,
			fmt.Sprintf("Subscription upgrade: %s → %s (prorated)", sub.TierName, newTier.Name),
		)
		if chargeErr != nil {
			return fmt.Errorf("INSUFFICIENT_BALANCE_FOR_UPGRADE")
		}
	}

	// Update the subscription tier
	if err := s.store.UpdateSubscriptionTier(ctx, subscriptionID, newTierID, newTier.Name, newTier.PricePaise); err != nil {
		return fmt.Errorf("update subscription tier: %w", err)
	}

	meta, _ := json.Marshal(map[string]interface{}{
		"old_tier_id":     sub.TierID.String(),
		"new_tier_id":     newTierID.String(),
		"credit_paise":    creditPaise,
		"net_charge_paise": netChargePaise,
	})
	if err := s.store.InsertSubscriptionEvent(ctx, subscriptionID, "upgraded", "active", "active", meta); err != nil {
		slog.Warn("failed to log subscription event", "error", err)
	}

	return nil
}

// GetSubscriptionEvents returns the audit trail for a subscription.
func (s *Service) GetSubscriptionEvents(ctx context.Context, subscriptionID uuid.UUID, limit, offset int) ([]postgres.SubscriptionEvent, error) {
	return s.store.GetSubscriptionEvents(ctx, subscriptionID, limit, offset)
}

// HandleRenewalFailure implements the retry/grace/cancel state machine for a failed renewal.
func (s *Service) HandleRenewalFailure(ctx context.Context, sub postgres.SubscriptionForRenewal) error {
	newRetryCount, err := s.store.IncrementRetryCount(ctx, sub.ID)
	if err != nil {
		return fmt.Errorf("increment retry count: %w", err)
	}

	if newRetryCount < maxRenewalRetries {
		// Set to past_due
		if err := s.store.SetSubscriptionStatus(ctx, sub.ID, "past_due"); err != nil {
			return fmt.Errorf("set past_due: %w", err)
		}
		if err := s.store.InsertSubscriptionEvent(ctx, sub.ID, "payment_failed", "active", "past_due", nil); err != nil {
			slog.Warn("failed to log subscription event", "error", err)
		}
		return nil
	}

	// Max retries reached — enter grace period
	graceEnd := time.Now().Add(gracePeriodDuration)
	if err := s.store.SetSubscriptionGracePeriod(ctx, sub.ID, graceEnd); err != nil {
		return fmt.Errorf("set grace period: %w", err)
	}

	meta, _ := json.Marshal(map[string]interface{}{
		"grace_period_end": graceEnd.Format(time.RFC3339),
		"retry_count":      newRetryCount,
	})
	if err := s.store.InsertSubscriptionEvent(ctx, sub.ID, "grace_started", "past_due", "grace", meta); err != nil {
		slog.Warn("failed to log subscription event", "error", err)
	}

	return nil
}
