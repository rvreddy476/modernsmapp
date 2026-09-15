// Legacy premium tables (Sprint 5, Razorpay) and the consent registry.
//
// Lane P2 retired the Razorpay path: dating_premium_plans,
// dating_payment_intents and dating_payment_events are READ-ONLY. Nothing here
// writes them; the account purge (profiles.go) redacts raw payloads and
// removes the user's intents. The data export still reads the intents.
//
// dating_premium_subscriptions is the pass entitlement written by the payment
// consumer (premium.go); GetSubscription reads it for the export.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PaymentIntent is one row of the read-only dating_payment_intents.
type PaymentIntent struct {
	ID                     uuid.UUID  `json:"id"`
	UserID                 uuid.UUID  `json:"user_id"`
	PlanID                 string     `json:"plan_id"`
	AmountINRPaise         int64      `json:"amount_inr_paise"`
	RazorpayOrderID        string     `json:"razorpay_order_id"`
	RazorpaySubscriptionID *string    `json:"razorpay_subscription_id,omitempty"`
	Status                 string     `json:"status"`
	Source                 string     `json:"source"`
	CreatedAt              time.Time  `json:"created_at"`
	PaidAt                 *time.Time `json:"paid_at,omitempty"`
}

// PremiumSubscription is the user's pass entitlement row. ExpiresAt is never
// NULL (lane P2): a pass is active only while expires_at > now().
type PremiumSubscription struct {
	UserID                 uuid.UUID  `json:"user_id"`
	Plan                   string     `json:"plan"`
	PlanID                 *string    `json:"plan_id,omitempty"`
	RazorpaySubscriptionID *string    `json:"razorpay_subscription_id,omitempty"`
	StartedAt              time.Time  `json:"started_at"`
	ExpiresAt              time.Time  `json:"expires_at"`
	Source                 *string    `json:"source,omitempty"`
	AutoRenew              bool       `json:"auto_renew"`
	CancelledAt            *time.Time `json:"cancelled_at,omitempty"`
}

// ErrSubscriptionNotFound mirrors the package's not_found-prefix convention.
var ErrSubscriptionNotFound = errors.New("not_found: subscription not found")

// GetSubscription returns the user's pass entitlement row or ErrSubscriptionNotFound.
func (s *Store) GetSubscription(ctx context.Context, userID uuid.UUID) (*PremiumSubscription, error) {
	row := s.db.QueryRow(ctx, `
        SELECT user_id, plan, plan_id, razorpay_subscription_id, started_at, expires_at,
               source, auto_renew, cancelled_at
        FROM dating_premium_subscriptions
        WHERE user_id = $1`, userID)
	out := &PremiumSubscription{}
	if err := row.Scan(&out.UserID, &out.Plan, &out.PlanID, &out.RazorpaySubscriptionID,
		&out.StartedAt, &out.ExpiresAt, &out.Source, &out.AutoRenew, &out.CancelledAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return out, nil
}

// ConsentEntry is one row of dating_consent_log.
type ConsentEntry struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	ConsentType   string    `json:"consent_type"`
	Granted       bool      `json:"granted"`
	PolicyVersion string    `json:"policy_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// RecordConsent appends a row to the consent registry. Append-only: a user
// who toggles Echoes off then on again has two rows. The DPDP audit trail
// requires both entries.
func (s *Store) RecordConsent(ctx context.Context, userID uuid.UUID, consentType string, granted bool, policyVersion string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	if consentType == "" {
		return fmt.Errorf("invalid: consent_type required")
	}
	if policyVersion == "" {
		return fmt.Errorf("invalid: policy_version required")
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_consent_log (user_id, consent_type, granted, policy_version)
        VALUES ($1, $2, $3, $4)`, userID, consentType, granted, policyVersion); err != nil {
		return fmt.Errorf("record consent: %w", err)
	}
	return nil
}

// ListConsentForUser returns the user's consent history, newest first.
func (s *Store) ListConsentForUser(ctx context.Context, userID uuid.UUID) ([]*ConsentEntry, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, consent_type, granted, policy_version, created_at
        FROM dating_consent_log
        WHERE user_id = $1
        ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list consent: %w", err)
	}
	defer rows.Close()
	out := make([]*ConsentEntry, 0, 4)
	for rows.Next() {
		e := &ConsentEntry{}
		if err := rows.Scan(&e.ID, &e.UserID, &e.ConsentType, &e.Granted, &e.PolicyVersion, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan consent: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListPaymentIntentsForUser returns the user's legacy Razorpay payment
// history, newest first. Read by the data-exporter (DPDP §15.8).
func (s *Store) ListPaymentIntentsForUser(ctx context.Context, userID uuid.UUID) ([]*PaymentIntent, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, plan_id, amount_inr_paise, razorpay_order_id,
               razorpay_subscription_id, status, source, created_at, paid_at
        FROM dating_payment_intents
        WHERE user_id = $1
        ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list payment intents: %w", err)
	}
	defer rows.Close()
	out := make([]*PaymentIntent, 0, 4)
	for rows.Next() {
		p := &PaymentIntent{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.PlanID, &p.AmountINRPaise, &p.RazorpayOrderID,
			&p.RazorpaySubscriptionID, &p.Status, &p.Source, &p.CreatedAt, &p.PaidAt); err != nil {
			return nil, fmt.Errorf("scan intent: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
