package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPlanNotFound is returned when GetPlan finds no row.
var ErrPlanNotFound = errors.New("subscription_plan: not found")

// ErrSubscriptionNotFound is returned when GetSubscription / etc. find no row.
var ErrSubscriptionNotFound = errors.New("subscription: not found")

// ErrPaymentNotFound is returned when GetSubscriptionPayment finds no row.
var ErrPaymentNotFound = errors.New("subscription_payment: not found")

// ListActivePlans returns every active subscription plan ordered by price.
func (s *Store) ListActivePlans(ctx context.Context) ([]SubscriptionPlan, error) {
	const q = `
        SELECT id, code, name, description, price_amount, currency_code, billing_period_days,
               lead_limit, fair_use_limit, priority_weight, is_unlimited, is_fleet_plan,
               max_drivers, grace_period_days, is_active
        FROM rider_subscription_plans
        WHERE is_active = TRUE
        ORDER BY price_amount ASC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	var out []SubscriptionPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetPlan returns the plan by id.
func (s *Store) GetPlan(ctx context.Context, id uuid.UUID) (*SubscriptionPlan, error) {
	const q = `
        SELECT id, code, name, description, price_amount, currency_code, billing_period_days,
               lead_limit, fair_use_limit, priority_weight, is_unlimited, is_fleet_plan,
               max_drivers, grace_period_days, is_active
        FROM rider_subscription_plans
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetPlanByCode looks up by code (e.g. 'plus_299'). Used in tests + seeds.
func (s *Store) GetPlanByCode(ctx context.Context, code string) (*SubscriptionPlan, error) {
	const q = `
        SELECT id, code, name, description, price_amount, currency_code, billing_period_days,
               lead_limit, fair_use_limit, priority_weight, is_unlimited, is_fleet_plan,
               max_drivers, grace_period_days, is_active
        FROM rider_subscription_plans
        WHERE code = $1`
	row := s.db.QueryRow(ctx, q, code)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	return p, nil
}

// CreateSubscriptionPaymentInput is the input for CreateSubscriptionPayment.
type CreateSubscriptionPaymentInput struct {
	PartnerID        uuid.UUID
	PlanID           uuid.UUID
	Amount           float64
	CurrencyCode     string
	PaymentMethod    string
	PaymentReference *string
	Status           string
}

// CreateSubscriptionPayment inserts a new payment row (typically in `pending`
// status). The service layer flips it to `verified` once the wallet debit
// succeeds (or admin verification, post-Sprint 1).
func (s *Store) CreateSubscriptionPayment(ctx context.Context, in CreateSubscriptionPaymentInput) (*SubscriptionPayment, error) {
	if in.CurrencyCode == "" {
		in.CurrencyCode = "INR"
	}
	if in.Status == "" {
		in.Status = "pending"
	}
	const q = `
        INSERT INTO rider_subscription_payments (partner_id, plan_id, amount, currency_code, payment_method, payment_reference, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7::rider_payment_status)
        RETURNING id, partner_id, subscription_id, plan_id, amount, currency_code, payment_method,
                  payment_reference, payment_proof_url, wallet_txn_id, status, rejection_reason,
                  verified_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.PartnerID, in.PlanID, in.Amount, in.CurrencyCode, in.PaymentMethod, in.PaymentReference, in.Status)
	return scanPayment(row)
}

// MarkPaymentVerified flips the payment to `verified` and links the wallet
// transaction id (used for refunds later).
func (s *Store) MarkPaymentVerified(ctx context.Context, paymentID uuid.UUID, walletTxnID *uuid.UUID, subscriptionID *uuid.UUID) (*SubscriptionPayment, error) {
	const q = `
        UPDATE rider_subscription_payments SET
            status          = 'verified',
            wallet_txn_id   = COALESCE($2, wallet_txn_id),
            subscription_id = COALESCE($3, subscription_id),
            verified_at     = NOW(),
            updated_at      = NOW()
        WHERE id = $1
        RETURNING id, partner_id, subscription_id, plan_id, amount, currency_code, payment_method,
                  payment_reference, payment_proof_url, wallet_txn_id, status, rejection_reason,
                  verified_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, paymentID, walletTxnID, subscriptionID)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return p, nil
}

// MarkPaymentFailed flips the payment to `failed` with a reason.
func (s *Store) MarkPaymentFailed(ctx context.Context, paymentID uuid.UUID, reason string) error {
	const q = `
        UPDATE rider_subscription_payments SET
            status           = 'failed',
            rejection_reason = $2,
            updated_at       = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, paymentID, reason)
	if err != nil {
		return fmt.Errorf("mark payment failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPaymentNotFound
	}
	return nil
}

// AttachPaymentProof updates the proof url + flips status to `submitted`. Used
// by the manual-UPI path where the partner uploads a screenshot for admin
// verification (S3).
func (s *Store) AttachPaymentProof(ctx context.Context, paymentID uuid.UUID, proofURL string) (*SubscriptionPayment, error) {
	const q = `
        UPDATE rider_subscription_payments SET
            payment_proof_url = $2,
            status            = 'submitted',
            updated_at        = NOW()
        WHERE id = $1
        RETURNING id, partner_id, subscription_id, plan_id, amount, currency_code, payment_method,
                  payment_reference, payment_proof_url, wallet_txn_id, status, rejection_reason,
                  verified_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, paymentID, proofURL)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetSubscriptionPayment returns one payment by id.
func (s *Store) GetSubscriptionPayment(ctx context.Context, id uuid.UUID) (*SubscriptionPayment, error) {
	const q = `
        SELECT id, partner_id, subscription_id, plan_id, amount, currency_code, payment_method,
               payment_reference, payment_proof_url, wallet_txn_id, status, rejection_reason,
               verified_at, created_at, updated_at
        FROM rider_subscription_payments
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return p, nil
}

// CreateSubscriptionInput is the input for CreateSubscription.
type CreateSubscriptionInput struct {
	PartnerID uuid.UUID
	PlanID    uuid.UUID
	Status    string
	StartsAt  time.Time
	ExpiresAt time.Time
}

// CreateSubscription inserts a new subscription row.
func (s *Store) CreateSubscription(ctx context.Context, in CreateSubscriptionInput) (*PartnerSubscription, error) {
	if in.Status == "" {
		in.Status = "active"
	}
	const q = `
        INSERT INTO rider_partner_subscriptions (partner_id, plan_id, status, starts_at, expires_at)
        VALUES ($1, $2, $3::rider_subscription_status, $4, $5)
        RETURNING id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
                  leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.PartnerID, in.PlanID, in.Status, in.StartsAt, in.ExpiresAt)
	return scanSubscription(row)
}

// GetActiveSubscription returns the most recent active/trial/grace subscription
// for the partner.
func (s *Store) GetActiveSubscription(ctx context.Context, partnerID uuid.UUID) (*PartnerSubscription, error) {
	const q = `
        SELECT id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
               leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at
        FROM rider_partner_subscriptions
        WHERE partner_id = $1 AND status IN ('trial', 'active', 'grace_period')
        ORDER BY expires_at DESC
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, partnerID)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return sub, nil
}

// GetSubscription returns one subscription by id.
func (s *Store) GetSubscription(ctx context.Context, id uuid.UUID) (*PartnerSubscription, error) {
	const q = `
        SELECT id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
               leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at
        FROM rider_partner_subscriptions
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return sub, nil
}

func scanPlan(row pgx.Row) (*SubscriptionPlan, error) {
	var p SubscriptionPlan
	if err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.PriceAmount, &p.CurrencyCode, &p.BillingPeriodDays, &p.LeadLimit, &p.FairUseLimit, &p.PriorityWeight, &p.IsUnlimited, &p.IsFleetPlan, &p.MaxDrivers, &p.GracePeriodDays, &p.IsActive); err != nil {
		return nil, err
	}
	return &p, nil
}

func scanPayment(row pgx.Row) (*SubscriptionPayment, error) {
	var p SubscriptionPayment
	if err := row.Scan(&p.ID, &p.PartnerID, &p.SubscriptionID, &p.PlanID, &p.Amount, &p.CurrencyCode, &p.PaymentMethod, &p.PaymentReference, &p.PaymentProofURL, &p.WalletTxnID, &p.Status, &p.RejectionReason, &p.VerifiedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func scanSubscription(row pgx.Row) (*PartnerSubscription, error) {
	var s PartnerSubscription
	if err := row.Scan(&s.ID, &s.PartnerID, &s.PlanID, &s.Status, &s.StartsAt, &s.ExpiresAt, &s.GraceEndsAt, &s.LeadsUsed, &s.FairUseUsed, &s.AutoRenew, &s.CancelledAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}
