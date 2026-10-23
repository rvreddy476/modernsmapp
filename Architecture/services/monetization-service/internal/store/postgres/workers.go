package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// The wallet-charging renewal query (GetSubscriptionsDueForRenewal) was
// removed with migration 025: renewal is by reminder and paid through
// payments-service (membership_payments.go).

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
