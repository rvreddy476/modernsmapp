package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Tier 3c — Entitlement check.
//
// "Is user X allowed to read this members-only post that requires
// tier T?" The answer is yes iff X has an *active* subscription to
// the post's author whose tier price meets-or-exceeds T's price.
// (A more permissive subscription unlocks lower tiers — same shape
// YouTube/Patreon use.)
//
// The check is exposed both as a single-user lookup (for the read
// path of a detail screen) and as a bulk check (for feed/list
// rendering, so we don't fan out to N synchronous calls).

// Entitlement is the shape an entitlement check returns.
type Entitlement struct {
	SubscriberID    uuid.UUID  `json:"subscriber_id"`
	CreatorID       uuid.UUID  `json:"creator_id"`
	Allowed         bool       `json:"allowed"`
	ActiveTierID    *uuid.UUID `json:"active_tier_id,omitempty"`
	ActivePricePaise int64     `json:"active_price_paise,omitempty"`
	RequiredTierID  *uuid.UUID `json:"required_tier_id,omitempty"`
	RequiredPricePaise int64   `json:"required_price_paise,omitempty"`
	Reason          string     `json:"reason,omitempty"`
}

// EntitlementCheckRequest is one row of a bulk entitlement check.
// RequiredTierID is optional; when nil, the call only verifies the
// caller has *any* active subscription to the creator (price > 0).
type EntitlementCheckRequest struct {
	SubscriberID   uuid.UUID  `json:"subscriber_id"`
	CreatorID      uuid.UUID  `json:"creator_id"`
	RequiredTierID *uuid.UUID `json:"required_tier_id,omitempty"`
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

	// Required-tier price floor. NULL required → any price > 0 unlocks
	// (i.e. the creator just wants paying members, no tier preference).
	requiredPrice := int64(0)
	if req.RequiredTierID != nil {
		tier, err := s.store.GetCreatorTier(ctx, *req.RequiredTierID)
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
		requiredPrice = tier.PricePaise
	}
	out.RequiredPricePaise = requiredPrice

	// Caller's active subscription with this creator (if any).
	sub, err := s.store.GetSubscription(ctx, req.SubscriberID, req.CreatorID)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	if sub == nil {
		out.Reason = "no_active_subscription"
		return out, nil
	}
	out.ActiveTierID = &sub.TierID
	out.ActivePricePaise = sub.PricePaise

	if sub.PricePaise >= requiredPrice {
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
				Reason:         "check_failed: " + err.Error(),
			})
			continue
		}
		out = append(out, *ent)
	}
	return out, nil
}
