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
	PricePaise       int64
	Currency         string
	CurrentPeriodEnd time.Time
}

// GetSubscriptionsDueForRenewal returns active, auto-renew subscriptions whose
// period ends before `before`.
func (s *Store) GetSubscriptionsDueForRenewal(ctx context.Context, before time.Time) ([]SubscriptionForRenewal, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, price, currency, current_period_end
		FROM subscriptions
		WHERE status = 'active'
		  AND auto_renew = TRUE
		  AND current_period_end < $1
		ORDER BY current_period_end ASC
		LIMIT 500
	`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []SubscriptionForRenewal
	for rows.Next() {
		var sub SubscriptionForRenewal
		if err := rows.Scan(
			&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID,
			&sub.PricePaise, &sub.Currency, &sub.CurrentPeriodEnd,
		); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// ExtendSubscriptionPeriod updates the current_period_end of a subscription.
func (s *Store) ExtendSubscriptionPeriod(ctx context.Context, subscriptionID uuid.UUID, newPeriodEnd time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET current_period_end = $2, payment_failed_at = NULL
		WHERE id = $1
	`, subscriptionID, newPeriodEnd)
	return err
}

// SetSubscriptionPaymentFailed marks a subscription as payment_failed and records the timestamp.
func (s *Store) SetSubscriptionPaymentFailed(ctx context.Context, subscriptionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET status = 'payment_failed', payment_failed_at = NOW()
		WHERE id = $1
	`, subscriptionID)
	return err
}

// ---------------------------------------------------------------------------
// BillingPeriod on CreatorTier
// ---------------------------------------------------------------------------

// CreatorTierWithBilling extends CreatorTier with a billing_period field.
// We store it on the existing CreatorTier struct via a separate query method.
type CreatorTierBilling struct {
	ID            uuid.UUID
	BillingPeriod string
}

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
	if err == nil {
		tier.BillingPeriod = bp
	} else {
		tier.BillingPeriod = "monthly"
	}
	return tier, nil
}

// ---------------------------------------------------------------------------
// Payout request worker queries
// ---------------------------------------------------------------------------

// PayoutRequest is used by the payout worker.
type PayoutRequest struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	TransactionID  uuid.UUID
	AmountPaise    int64
	Currency       string
	Status         string
	payoutMethodID *uuid.UUID
	RequestedAt    time.Time
}

// PayoutMethodID returns the payout method ID as string, or empty string if nil.
func (r PayoutRequest) PayoutMethodID() string {
	if r.payoutMethodID == nil {
		return ""
	}
	return r.payoutMethodID.String()
}

// GetPendingPayoutRequests returns payout requests in pending status that were
// requested before `before` (i.e., have passed the review window).
func (s *Store) GetPendingPayoutRequests(ctx context.Context, before time.Time) ([]PayoutRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, transaction_id, amount, currency, status, payout_method_id, requested_at
		FROM payout_requests
		WHERE status = 'pending' AND requested_at < $1
		ORDER BY requested_at ASC
		LIMIT 100
	`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []PayoutRequest
	for rows.Next() {
		var r PayoutRequest
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.TransactionID, &r.AmountPaise, &r.Currency, &r.Status,
			&r.payoutMethodID, &r.RequestedAt,
		); err != nil {
			return nil, err
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

// SetPayoutRequestStatus sets a payout_request row to the given status.
func (s *Store) SetPayoutRequestStatus(ctx context.Context, requestID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET status = $2 WHERE id = $1
	`, requestID, status)
	return err
}

// SetPayoutRequestPaid marks a payout_request as paid and records processed_at.
func (s *Store) SetPayoutRequestPaid(ctx context.Context, requestID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests
		SET status = 'paid', processed_at = NOW()
		WHERE id = $1
	`, requestID)
	return err
}

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
