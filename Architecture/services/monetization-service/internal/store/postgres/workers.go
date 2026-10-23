package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Subscription worker queries
// ---------------------------------------------------------------------------

// SubscriptionForRenewal is a lightweight view of subscriptions used by the renewal worker.
type SubscriptionForRenewal struct {
	ID               uuid.UUID
	SubscriberID     uuid.UUID
	CreatorID        uuid.UUID
	TierID           uuid.UUID
	TierName         string
	PricePaise       int64
	Currency         string
	Status           string // active or past_due
	CurrentPeriodEnd time.Time
	RetryCount       int
	// A scheduled downgrade: the next period renews on this tier at this
	// (locked) price.
	PendingTierID     *uuid.UUID
	PendingPricePaise *int64
}

// GetSubscriptionsDueForRenewal returns auto-renew subscriptions whose
// period ends before `before` and that may be charged now: every active
// one, and every past_due one whose last failed attempt was at or before
// `retryBefore` (so a failed charge is retried, and its failures progress
// past_due → grace → cancelled instead of stopping at past_due).
func (s *Store) GetSubscriptionsDueForRenewal(ctx context.Context, before, retryBefore time.Time) ([]SubscriptionForRenewal, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status,
		       current_period_end, retry_count, pending_tier_id, pending_price
		FROM subscriptions
		WHERE auto_renew = TRUE
		  AND current_period_end < $1
		  AND (status = 'active'
		       OR (status = 'past_due' AND (payment_failed_at IS NULL OR payment_failed_at <= $2)))
		ORDER BY current_period_end ASC
		LIMIT 500
	`, before, retryBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []SubscriptionForRenewal
	for rows.Next() {
		var sub SubscriptionForRenewal
		if err := rows.Scan(
			&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
			&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodEnd, &sub.RetryCount,
			&sub.PendingTierID, &sub.PendingPricePaise,
		); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// The charge-then-extend pair (ChargeAndCreditRef + ExtendSubscriptionPeriod)
// the renewal worker used to call separately is gone: RenewSubscription in
// memberships.go does both in one transaction under a per-period key.

// ---------------------------------------------------------------------------
// BillingPeriod on CreatorTier
// ---------------------------------------------------------------------------

// GetCreatorTierBillingPeriod returns the billing_period for a tier.
// Falls back to "monthly" if the column does not exist yet.
func (s *Store) GetCreatorTierBillingPeriod(ctx context.Context, tierID uuid.UUID) (string, error) {
	var bp string
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(billing_period, 'monthly')
		FROM creator_tiers
		WHERE id = $1
	`, tierID).Scan(&bp)
	if err != nil {
		return "monthly", err
	}
	return bp, nil
}

// GetCreatorTierWithBillingPeriod returns tier with billing_period, adding it to CreatorTier.
// It delegates to GetCreatorTier and enriches with billing_period.
func (s *Store) GetCreatorTierWithBillingPeriod(ctx context.Context, tierID uuid.UUID) (*CreatorTier, error) {
	tier, err := s.GetCreatorTier(ctx, tierID)
	if err != nil || tier == nil {
		return tier, err
	}
	bp, err := s.GetCreatorTierBillingPeriod(ctx, tierID)
	if err != nil {
		return nil, err
	}
	tier.BillingPeriod = bp
	return tier, nil
}

// The payout worker's queries (GetPendingPayoutRequests,
// SetPayoutRequestStatus, SetPayoutRequestPaid) were removed in plan
// Phase 4A: the submitter and reconciler read through
// ListPayoutRequestsByStatus / ListPayoutRequestsToReconcile and write
// only through TransitionPayoutRequest, in payout_rail.go.

// ---------------------------------------------------------------------------
// Stale hold cleanup
// ---------------------------------------------------------------------------

// ReleaseStaleHolds releases balance holds (transactions of type 'hold') older
// than `before` that have no corresponding release. Returns the number of
// holds released.
func (s *Store) ReleaseStaleHolds(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE transactions
		SET status = 'failed', description = description || ' [auto-released: stale hold]'
		WHERE type = 'hold'
		  AND status = 'pending'
		  AND created_at < $1
	`, before)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Fundraiser expiry
// ---------------------------------------------------------------------------

// CloseExpiredFundraisers sets status='completed' for active fundraisers whose
// end_date is in the past. Returns the IDs of the closed fundraisers.
func (s *Store) CloseExpiredFundraisers(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE fundraisers
		SET status = 'completed'
		WHERE status = 'active' AND ends_at IS NOT NULL AND ends_at < $1
		RETURNING id
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
