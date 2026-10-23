package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tier 3c — Entitlement check.
//
// "Is user X allowed to read this members-only post that requires tier R?"
//
// Yes iff X holds a live subscription to the post's author that is still
// inside its paid access window, on a tier at or above R:
//
//   - Window: status active, cancelled_at_period_end, paused or past_due
//     passes while now < current_period_end; grace passes while
//     now < grace_period_end. cancelled and expired never pass.
//   - Level: the subscription's tier S unlocks R when S == R, or when S's
//     CURRENT price is at least R's CURRENT price (both tiers belong to the
//     creator). The price the subscription locked in at checkout is never
//     compared: a creator raising a tier's price must not lock that tier's
//     own members out, and a member's old discount must not decide which
//     tier they are on.
//
// The check is exposed both as a single-user lookup (for the read
// path of a detail screen) and as a bulk check (for feed/list
// rendering, so we don't fan out to N synchronous calls).

// Entitlement is the shape an entitlement check returns.
type Entitlement struct {
	SubscriberID uuid.UUID  `json:"subscriber_id"`
	CreatorID    uuid.UUID  `json:"creator_id"`
	Allowed      bool       `json:"allowed"`
	ActiveTierID *uuid.UUID `json:"active_tier_id,omitempty"`
	// ActivePricePaise is the subscribed tier's current price: its level.
	ActivePricePaise   int64      `json:"active_price_paise,omitempty"`
	RequiredTierID     *uuid.UUID `json:"required_tier_id,omitempty"`
	RequiredPricePaise int64      `json:"required_price_paise,omitempty"`
	// AccessUntil is when the subscription's access ends, if it has one.
	AccessUntil *time.Time `json:"access_until,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// EntitlementCheckRequest is one row of a bulk entitlement check.
// RequiredTierID is optional; when nil, the call only verifies the
// caller has *any* subscription to the creator inside its access window.
type EntitlementCheckRequest struct {
	SubscriberID   uuid.UUID  `json:"subscriber_id"`
	CreatorID      uuid.UUID  `json:"creator_id"`
	RequiredTierID *uuid.UUID `json:"required_tier_id,omitempty"`
}

// SubscriptionAccessUntil returns when sub's access ends, and false for a
// status that never grants access (cancelled, expired, anything unknown).
func SubscriptionAccessUntil(sub *postgres.Subscription) (time.Time, bool) {
	switch sub.Status {
	case "active", "cancelled_at_period_end", "paused", "past_due":
		return sub.CurrentPeriodEnd, true
	case "grace":
		if sub.GracePeriodEnd != nil {
			return *sub.GracePeriodEnd, true
		}
		return sub.CurrentPeriodEnd, true
	default:
		return time.Time{}, false
	}
}

// CheckEntitlement decides one (subscriber, creator, tier?) tuple.
// Authors are always considered entitled to their own content — the
// caller (post-service handler) shortcuts that case before calling
// here, but we mirror the rule so direct callers (admin tools, mobile
// preflight) get the same answer.
func (s *Service) CheckEntitlement(ctx context.Context, req EntitlementCheckRequest) (*Entitlement, error) {
	out := &Entitlement{
		SubscriberID:   req.SubscriberID,
		CreatorID:      req.CreatorID,
		RequiredTierID: req.RequiredTierID,
	}

	// Author always passes their own gate.
	if req.SubscriberID == req.CreatorID {
		out.Allowed = true
		out.Reason = "self"
		return out, nil
	}

	var required *postgres.CreatorTier
	if req.RequiredTierID != nil {
		tier, err := s.members.GetCreatorTier(ctx, *req.RequiredTierID)
		if err != nil {
			return nil, fmt.Errorf("get required tier: %w", err)
		}
		if tier == nil {
			out.Reason = "required_tier_not_found"
			return out, nil
		}
		if tier.CreatorID != req.CreatorID {
			// Defensive: a post can't gate on a tier owned by a different
			// creator. Treat as a configuration error — deny.
			out.Reason = "required_tier_creator_mismatch"
			return out, nil
		}
		required = tier
		out.RequiredPricePaise = tier.PricePaise
	}

	sub, err := s.members.GetLiveSubscription(ctx, req.SubscriberID, req.CreatorID)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	if sub == nil {
		out.Reason = "no_active_subscription"
		return out, nil
	}
	out.ActiveTierID = &sub.TierID

	until, ok := SubscriptionAccessUntil(sub)
	if !ok {
		out.Reason = "no_active_subscription"
		return out, nil
	}
	out.AccessUntil = &until
	if !s.clock().Before(until) {
		out.Reason = "access_period_ended"
		return out, nil
	}

	if required == nil || sub.TierID == required.ID {
		out.Allowed = true
		return out, nil
	}

	held, err := s.members.GetCreatorTier(ctx, sub.TierID)
	if err != nil {
		return nil, fmt.Errorf("get subscribed tier: %w", err)
	}
	if held == nil || held.CreatorID != req.CreatorID {
		out.Reason = "subscribed_tier_not_found"
		return out, nil
	}
	out.ActivePricePaise = held.PricePaise
	if held.PricePaise >= required.PricePaise {
		out.Allowed = true
		return out, nil
	}
	out.Reason = "tier_below_required"
	return out, nil
}

// CheckEntitlementsBulk runs CheckEntitlement on each request and
// returns answers in the same order. A failure on one row does not
// abort the batch — the row is returned with Allowed=false and Reason
// describing the error. Bounded so a single call can't be used to fan
// out unbounded subscription queries.
const maxBulkEntitlements = 100

func (s *Service) CheckEntitlementsBulk(ctx context.Context, reqs []EntitlementCheckRequest) ([]Entitlement, error) {
	if len(reqs) == 0 {
		return []Entitlement{}, nil
	}
	if len(reqs) > maxBulkEntitlements {
		return nil, fmt.Errorf("too many entitlement checks: %d > %d", len(reqs), maxBulkEntitlements)
	}
	out := make([]Entitlement, 0, len(reqs))
	for _, r := range reqs {
		ent, err := s.CheckEntitlement(ctx, r)
		if err != nil {
			out = append(out, Entitlement{
				SubscriberID:   r.SubscriberID,
				CreatorID:      r.CreatorID,
				RequiredTierID: r.RequiredTierID,
				Allowed:        false,
				Reason:         "check_failed",
			})
			continue
		}
		out = append(out, *ent)
	}
	return out, nil
}
