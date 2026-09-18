package store

// Captain subscription checkout through payments-service (migration 005).
//
// A checkout is a rider_partner_subscriptions row in status pending_payment
// with payment_status pending -> confirming (intent bound) -> paid | failed.
// ONLY ApplyRidePaymentEvent (payment_events.go) writes paid / failed and
// activates the row, from the signed payment.succeeded / payment.failed
// event. The free trial never touches payments: it activates here, once per
// partner ever (rider_partners.trial_used_at).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Subscription checkout row statuses.
const (
	SubscriptionPendingPayment = "pending_payment"
	SubscriptionTrial          = "trial"
	SubscriptionActive         = "active"
)

var (
	// ErrTrialAlreadyUsed: the partner already had their one free trial.
	ErrTrialAlreadyUsed = errors.New("subscription: the free trial was already used by this partner")
	// ErrCheckoutNotBindable: the checkout row is not awaiting payment.
	ErrCheckoutNotBindable = errors.New("subscription: checkout is not awaiting payment")
)

const subscriptionColumns = `id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
               leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at,
               intent_id, intent_method, amount_paise, payment_status, paid_at, renews_subscription_id`

// CreateCheckoutInput opens a checkout row.
type CreateCheckoutInput struct {
	PartnerID   uuid.UUID
	PlanID      uuid.UUID
	AmountPaise int64
	// RenewsSubscriptionID is the active row this checkout extends (nil for
	// a first purchase).
	RenewsSubscriptionID *uuid.UUID
}

// CreateCheckoutSubscription inserts a pending_payment row. starts_at /
// expires_at are placeholders (now) until the capture sets the real period.
func (s *Store) CreateCheckoutSubscription(ctx context.Context, in CreateCheckoutInput) (*PartnerSubscription, error) {
	if in.AmountPaise <= 0 {
		return nil, fmt.Errorf("subscription checkout: amount must be positive")
	}
	row := s.db.QueryRow(ctx, `
        INSERT INTO rider_partner_subscriptions (partner_id, plan_id, status, starts_at, expires_at, amount_paise, payment_status, renews_subscription_id)
        VALUES ($1, $2, 'pending_payment', NOW(), NOW(), $3, 'pending', $4)
        RETURNING `+subscriptionColumns, in.PartnerID, in.PlanID, in.AmountPaise, in.RenewsSubscriptionID)
	return scanSubscription(row)
}

// GetOpenCheckout returns the partner's open checkout for the plan (status
// pending_payment, payment not paid), newest first, or
// ErrSubscriptionNotFound.
func (s *Store) GetOpenCheckout(ctx context.Context, partnerID, planID uuid.UUID) (*PartnerSubscription, error) {
	row := s.db.QueryRow(ctx, `
        SELECT `+subscriptionColumns+`
        FROM rider_partner_subscriptions
        WHERE partner_id = $1 AND plan_id = $2 AND status = 'pending_payment'
          AND payment_status IN ('pending','confirming','failed')
        ORDER BY created_at DESC LIMIT 1`, partnerID, planID)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return sub, nil
}

// LatestCheckout returns the partner's most recent checkout row (any
// payment_status), for GET /subscriptions/me/payment.
func (s *Store) LatestCheckout(ctx context.Context, partnerID uuid.UUID) (*PartnerSubscription, error) {
	row := s.db.QueryRow(ctx, `
        SELECT `+subscriptionColumns+`
        FROM rider_partner_subscriptions
        WHERE partner_id = $1 AND payment_status IS NOT NULL
        ORDER BY created_at DESC LIMIT 1`, partnerID)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return sub, nil
}

// BindSubscriptionIntent records the payments intent opened for a checkout
// and moves payment_status to confirming. A row that is not awaiting
// payment is ErrCheckoutNotBindable.
func (s *Store) BindSubscriptionIntent(ctx context.Context, id, intentID uuid.UUID, providerRef, method string) (*PartnerSubscription, error) {
	row := s.db.QueryRow(ctx, `
        UPDATE rider_partner_subscriptions
        SET intent_id = $2, provider_reference = NULLIF($3, ''), intent_method = $4,
            payment_status = 'confirming', payment_failure_reason = NULL, updated_at = NOW()
        WHERE id = $1 AND status = 'pending_payment' AND payment_status IN ('pending','confirming','failed')
        RETURNING `+subscriptionColumns, id, intentID, providerRef, method)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCheckoutNotBindable
		}
		return nil, fmt.Errorf("bind subscription intent: %w", err)
	}
	return sub, nil
}

// ActivateTrialSubscription creates the partner's trial row (status trial,
// now .. now + days) and stamps rider_partners.trial_used_at in the same
// transaction; a partner whose stamp is already set gets
// ErrTrialAlreadyUsed and no row.
func (s *Store) ActivateTrialSubscription(ctx context.Context, partnerID, planID uuid.UUID, now time.Time, days int) (*PartnerSubscription, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
        UPDATE rider_partners SET trial_used_at = $2, updated_at = NOW()
        WHERE id = $1 AND deleted_at IS NULL AND trial_used_at IS NULL
        RETURNING id`, partnerID, now).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTrialAlreadyUsed
		}
		return nil, fmt.Errorf("stamp trial: %w", err)
	}
	sub, err := scanSubscription(tx.QueryRow(ctx, `
        INSERT INTO rider_partner_subscriptions (partner_id, plan_id, status, starts_at, expires_at, amount_paise)
        VALUES ($1, $2, 'trial', $3, $4, 0)
        RETURNING `+subscriptionColumns, partnerID, planID, now, now.AddDate(0, 0, days)))
	if err != nil {
		return nil, fmt.Errorf("create trial subscription: %w", err)
	}
	return sub, tx.Commit(ctx)
}

// TrialUsed reports whether the partner already used the free trial.
func (s *Store) TrialUsed(ctx context.Context, partnerID uuid.UUID) (bool, error) {
	var used bool
	err := s.db.QueryRow(ctx, `SELECT trial_used_at IS NOT NULL FROM rider_partners WHERE id = $1`, partnerID).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrPartnerNotFound
	}
	return used, err
}

// GetActivePaidSubscription returns the partner's current active / trial /
// grace row for the plan (what a renewal extends), or ErrSubscriptionNotFound.
func (s *Store) GetActivePaidSubscription(ctx context.Context, partnerID uuid.UUID) (*PartnerSubscription, error) {
	return s.GetActiveSubscription(ctx, partnerID)
}
