package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The renewal state machine (charge, retry, past_due → grace → cancelled)
// has ONE implementation: internal/workers (renewSubscriptions and
// handleRenewalFailure). The copy that used to live here, never called,
// was removed on 23 Oct 2026.

func logEventWriteFailure(err error) {
	slog.Warn("failed to log subscription event", "error", err)
}

// ownedSubscription loads a subscription and checks it belongs to subscriberID.
func (s *Service) ownedSubscription(ctx context.Context, subscriberID, subscriptionID uuid.UUID) (*postgres.Subscription, error) {
	sub, err := s.members.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	if sub.SubscriberID != subscriberID {
		return nil, fmt.Errorf("SUBSCRIPTION_NOT_OWNED")
	}
	return sub, nil
}

// ErrPauseUntilInvalid: pause_until is not in the future.
var ErrPauseUntilInvalid = errors.New("PAUSE_UNTIL_INVALID")

// PauseSubscription pauses a subscription until pauseUntil, with ownership
// verification. A pause does not move the paid period: access continues
// only until current_period_end, and nothing renews while paused, so a
// pause never buys free access past what was paid for.
func (s *Service) PauseSubscription(ctx context.Context, subscriberID, subscriptionID uuid.UUID, pauseUntil time.Time) error {
	sub, err := s.ownedSubscription(ctx, subscriberID, subscriptionID)
	if err != nil {
		return err
	}
	if sub.Status != "active" {
		return fmt.Errorf("SUBSCRIPTION_NOT_ACTIVE")
	}
	if !pauseUntil.After(s.clock()) {
		return ErrPauseUntilInvalid
	}

	if err := s.members.PauseSubscription(ctx, subscriptionID, pauseUntil); err != nil {
		if errors.Is(err, postgres.ErrSubscriptionStateChanged) {
			return fmt.Errorf("SUBSCRIPTION_NOT_ACTIVE")
		}
		return fmt.Errorf("pause subscription: %w", err)
	}
	if err := s.members.InsertSubscriptionEvent(ctx, subscriptionID, "paused", sub.Status, "paused", nil); err != nil {
		logEventWriteFailure(err)
	}
	s.publishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, EntitlementPaused)
	return nil
}

// ResumeSubscription reactivates a paused subscription, with ownership verification.
func (s *Service) ResumeSubscription(ctx context.Context, subscriberID, subscriptionID uuid.UUID) error {
	sub, err := s.ownedSubscription(ctx, subscriberID, subscriptionID)
	if err != nil {
		return err
	}
	if sub.Status != "paused" {
		return fmt.Errorf("SUBSCRIPTION_NOT_PAUSED")
	}

	if err := s.members.ResumeSubscription(ctx, subscriptionID); err != nil {
		if errors.Is(err, postgres.ErrSubscriptionStateChanged) {
			return fmt.Errorf("SUBSCRIPTION_NOT_PAUSED")
		}
		return fmt.Errorf("resume subscription: %w", err)
	}
	if err := s.members.InsertSubscriptionEvent(ctx, subscriptionID, "resumed", sub.Status, "active", nil); err != nil {
		logEventWriteFailure(err)
	}
	s.publishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, EntitlementResumed)
	return nil
}

// CancelAtPeriodEnd marks a subscription to be cancelled at the end of the
// current billing period. Access continues until then; the expiry worker
// moves it to expired at period end and publishes the revocation.
func (s *Service) CancelAtPeriodEnd(ctx context.Context, subscriberID, subscriptionID uuid.UUID, reason string) error {
	sub, err := s.ownedSubscription(ctx, subscriberID, subscriptionID)
	if err != nil {
		return err
	}
	if sub.Status != "active" && sub.Status != "past_due" && sub.Status != "grace" {
		return fmt.Errorf("SUBSCRIPTION_CANNOT_CANCEL")
	}

	if err := s.members.CancelAtPeriodEnd(ctx, subscriptionID, reason); err != nil {
		return fmt.Errorf("cancel at period end: %w", err)
	}
	meta, _ := json.Marshal(map[string]string{"reason": reason})
	if err := s.members.InsertSubscriptionEvent(ctx, subscriptionID, "cancel_at_period_end", sub.Status, "cancelled_at_period_end", meta); err != nil {
		logEventWriteFailure(err)
	}
	// A grace subscription's access ran to grace_period_end; scheduled for
	// cancellation it runs to current_period_end. Tell the caches either way.
	s.publishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, EntitlementCancelPlanned)
	return nil
}

// CancelImmediately cancels a subscription right away and publishes an entitlement change event.
func (s *Service) CancelImmediately(ctx context.Context, subscriberID, subscriptionID uuid.UUID, reason string) error {
	sub, err := s.ownedSubscription(ctx, subscriberID, subscriptionID)
	if err != nil {
		return err
	}
	if sub.Status == "cancelled" || sub.Status == "expired" {
		return fmt.Errorf("SUBSCRIPTION_ALREADY_CANCELLED")
	}

	if err := s.members.SetSubscriptionStatus(ctx, subscriptionID, "cancelled"); err != nil {
		return fmt.Errorf("cancel subscription: %w", err)
	}
	meta, _ := json.Marshal(map[string]string{"reason": reason})
	if err := s.members.InsertSubscriptionEvent(ctx, subscriptionID, "cancelled_immediately", sub.Status, "cancelled", meta); err != nil {
		logEventWriteFailure(err)
	}
	s.publishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, EntitlementRevoked)
	return nil
}

// ---------------------------------------------------------------------------
// Tier change: upgrade now (pro-rata), downgrade at the next period
// ---------------------------------------------------------------------------

// Tier-change outcomes.
const (
	TierChangeUpgraded           = "upgraded"
	TierChangeDowngradeScheduled = "downgrade_scheduled"
	TierChangeUnchanged          = "unchanged"
)

// TierChange is what a tier change did.
type TierChange struct {
	Result       string    `json:"status"`
	ChargedPaise int64     `json:"charged_paise"`
	TierID       uuid.UUID `json:"tier_id"`
	// EffectiveAt is when the subscription is on TierID: now for an
	// upgrade, the next period start for a scheduled downgrade.
	EffectiveAt time.Time `json:"effective_at"`
}

// ProrateUpgradePaise is what moving up costs mid-period: the difference
// between the new tier's price and what was paid for this period, for the
// fraction of the period still to run. Integer paise, rounded DOWN (in the
// subscriber's favour). Zero when the new price is not higher than what was
// paid, or the period has ended. Exact: computed on nanoseconds in big
// integers, so no float or overflow rounding creeps in.
func ProrateUpgradePaise(paidPaise, newPricePaise int64, periodStart, periodEnd, now time.Time) int64 {
	diff := newPricePaise - paidPaise
	if diff <= 0 {
		return 0
	}
	total := periodEnd.Sub(periodStart)
	if total <= 0 {
		return 0
	}
	remaining := periodEnd.Sub(now)
	if remaining <= 0 {
		return 0
	}
	if remaining > total {
		remaining = total
	}
	n := new(big.Int).Mul(big.NewInt(diff), big.NewInt(int64(remaining)))
	n.Quo(n, big.NewInt(int64(total))) // both positive: truncation is floor
	return n.Int64()
}

// UpgradeSubscription moves a subscription to another tier of the same
// creator. Whether that is up or down is decided by tier LEVEL — the two
// tiers' current prices — the same rule entitlement uses.
//
//   - Up: charged now, pro-rata (ProrateUpgradePaise against what was paid
//     for this period), period end kept; the charge and the tier change are
//     one transaction. The subscription's locked price becomes the new
//     tier's price from the next renewal on.
//   - Down (or level): nothing charged or refunded; the new tier and its
//     current price are stored as pending and applied at the next renewal.
//   - Same tier: clears any scheduled downgrade.
func (s *Service) UpgradeSubscription(ctx context.Context, subscriberID, subscriptionID, newTierID uuid.UUID) (*TierChange, error) {
	sub, err := s.ownedSubscription(ctx, subscriberID, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub.Status != "active" {
		return nil, fmt.Errorf("SUBSCRIPTION_NOT_ACTIVE")
	}

	if newTierID == sub.TierID {
		if sub.PendingTierID != nil {
			if err := s.members.SetPendingTier(ctx, sub.ID, sub.TierID, nil, nil); err != nil {
				return nil, tierChangeErr(err)
			}
			if err := s.members.InsertSubscriptionEvent(ctx, sub.ID, "downgrade_cancelled", sub.Status, sub.Status, nil); err != nil {
				logEventWriteFailure(err)
			}
		}
		return &TierChange{Result: TierChangeUnchanged, TierID: sub.TierID, EffectiveAt: s.clock()}, nil
	}

	newTier, err := s.members.GetCreatorTier(ctx, newTierID)
	if err != nil {
		return nil, err
	}
	if newTier == nil {
		return nil, fmt.Errorf("TIER_NOT_FOUND")
	}
	if !newTier.IsActive {
		return nil, fmt.Errorf("TIER_INACTIVE")
	}
	if newTier.CreatorID != sub.CreatorID {
		return nil, fmt.Errorf("TIER_CREATOR_MISMATCH")
	}

	// The current tier's level. If it has gone, what was paid stands in.
	currentLevel := sub.PricePaise
	if cur, err := s.members.GetCreatorTier(ctx, sub.TierID); err != nil {
		return nil, err
	} else if cur != nil {
		currentLevel = cur.PricePaise
	}

	if newTier.PricePaise <= currentLevel {
		price := newTier.PricePaise
		if err := s.members.SetPendingTier(ctx, sub.ID, sub.TierID, &newTier.ID, &price); err != nil {
			return nil, tierChangeErr(err)
		}
		meta, _ := json.Marshal(map[string]any{
			"old_tier_id":     sub.TierID.String(),
			"pending_tier_id": newTier.ID.String(),
			"pending_price":   price,
			"effective_at":    sub.CurrentPeriodEnd.Format(time.RFC3339),
		})
		if err := s.members.InsertSubscriptionEvent(ctx, sub.ID, "downgrade_scheduled", sub.Status, sub.Status, meta); err != nil {
			logEventWriteFailure(err)
		}
		// Access is unchanged until the renewal applies it; the renewal
		// publishes then.
		return &TierChange{Result: TierChangeDowngradeScheduled, TierID: newTier.ID, EffectiveAt: sub.CurrentPeriodEnd}, nil
	}

	now := s.clock()
	charge := ProrateUpgradePaise(sub.PricePaise, newTier.PricePaise, sub.CurrentPeriodStart, sub.CurrentPeriodEnd, now)
	if err := s.members.UpgradeSubscription(ctx, postgres.UpgradeInput{
		SubscriptionID:    sub.ID,
		SubscriberID:      sub.SubscriberID,
		CreatorID:         sub.CreatorID,
		ExpectedTierID:    sub.TierID,
		ExpectedPeriodEnd: sub.CurrentPeriodEnd,
		NewTierID:         newTier.ID,
		NewTierName:       newTier.Name,
		NewPricePaise:     newTier.PricePaise,
		ChargePaise:       charge,
		Currency:          sub.Currency,
		Description:       fmt.Sprintf("Subscription upgrade: %s → %s (pro-rata)", sub.TierName, newTier.Name),
	}); err != nil {
		return nil, tierChangeErr(err)
	}

	meta, _ := json.Marshal(map[string]any{
		"old_tier_id":  sub.TierID.String(),
		"new_tier_id":  newTier.ID.String(),
		"paid_paise":   sub.PricePaise,
		"new_price":    newTier.PricePaise,
		"charge_paise": charge,
	})
	if err := s.members.InsertSubscriptionEvent(ctx, sub.ID, "upgraded", sub.Status, sub.Status, meta); err != nil {
		logEventWriteFailure(err)
	}
	s.publishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, EntitlementUpgraded)
	return &TierChange{Result: TierChangeUpgraded, ChargedPaise: charge, TierID: newTier.ID, EffectiveAt: now}, nil
}

func tierChangeErr(err error) error {
	switch {
	case errors.Is(err, postgres.ErrInsufficientFunds):
		return fmt.Errorf("INSUFFICIENT_BALANCE_FOR_UPGRADE")
	case errors.Is(err, postgres.ErrSubscriptionStateChanged):
		return fmt.Errorf("SUBSCRIPTION_STATE_CHANGED")
	default:
		return fmt.Errorf("change subscription tier: %w", err)
	}
}

// GetSubscriptionEvents returns the audit trail for a subscription.
// Only the subscriber and the creator may read a subscription's history;
// anyone else gets SUBSCRIPTION_NOT_FOUND, so an id never reveals that a
// membership exists.
func (s *Service) GetSubscriptionEvents(ctx context.Context, viewerID, subscriptionID uuid.UUID, limit, offset int) ([]postgres.SubscriptionEvent, error) {
	sub, err := s.members.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub == nil || (sub.SubscriberID != viewerID && sub.CreatorID != viewerID) {
		return nil, fmt.Errorf("SUBSCRIPTION_NOT_FOUND")
	}
	return s.members.GetSubscriptionEvents(ctx, subscriptionID, limit, offset)
}
