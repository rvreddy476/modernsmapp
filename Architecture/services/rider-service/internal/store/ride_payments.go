package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrRidePaymentNotFound is returned when GetRidePayment finds no row.
var ErrRidePaymentNotFound = errors.New("ride_payment: not found")

// CreateRidePaymentInput is the input for CreateRidePayment.
type CreateRidePaymentInput struct {
	RideID        uuid.UUID
	PartnerID     uuid.UUID
	AmountPaise   int64
	PaymentMethod string // 'cash' | 'wallet' | 'upi'
	Status        string // defaults to 'pending'
}

// CreateRidePayment inserts one ride-payment row.
func (s *Store) CreateRidePayment(ctx context.Context, in CreateRidePaymentInput) (*RidePayment, error) {
	if in.Status == "" {
		in.Status = "pending"
	}
	const q = `
        INSERT INTO rider_ride_payments (ride_id, partner_id, amount_paise, payment_method, status)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id, ride_id, partner_id, amount_paise, payment_method, status, wallet_txn_id, upi_txn_ref, created_at, settled_at`
	row := s.db.QueryRow(ctx, q, in.RideID, in.PartnerID, in.AmountPaise, in.PaymentMethod, in.Status)
	return scanRidePayment(row)
}

// MarkRidePaymentSucceeded flips the row to 'succeeded' + records the wallet
// txn (when method='wallet') or the UPI ref (when method='upi'). cash
// settlement is informal — the caller passes nil for both extra params.
func (s *Store) MarkRidePaymentSucceeded(ctx context.Context, paymentID uuid.UUID, walletTxnID *uuid.UUID, upiRef *string) (*RidePayment, error) {
	const q = `
        UPDATE rider_ride_payments
        SET status        = 'succeeded',
            wallet_txn_id = COALESCE($2, wallet_txn_id),
            upi_txn_ref   = COALESCE($3, upi_txn_ref),
            settled_at    = NOW()
        WHERE id = $1
        RETURNING id, ride_id, partner_id, amount_paise, payment_method, status, wallet_txn_id, upi_txn_ref, created_at, settled_at`
	row := s.db.QueryRow(ctx, q, paymentID, walletTxnID, upiRef)
	p, err := scanRidePayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRidePaymentNotFound
		}
		return nil, err
	}
	return p, nil
}

// MarkRidePaymentFailed flips the row to 'failed'.
func (s *Store) MarkRidePaymentFailed(ctx context.Context, paymentID uuid.UUID) error {
	const q = `UPDATE rider_ride_payments SET status = 'failed', settled_at = NOW() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, paymentID)
	if err != nil {
		return fmt.Errorf("mark ride payment failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRidePaymentNotFound
	}
	return nil
}

// GetRidePayment returns one payment row by id.
func (s *Store) GetRidePayment(ctx context.Context, id uuid.UUID) (*RidePayment, error) {
	const q = `
        SELECT id, ride_id, partner_id, amount_paise, payment_method, status, wallet_txn_id, upi_txn_ref, created_at, settled_at
        FROM rider_ride_payments
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	p, err := scanRidePayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRidePaymentNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetRidePaymentByRide returns the latest payment row for a ride. Most rides
// have exactly one; cancellation / re-bill paths can produce multiple, so we
// take the most recent.
func (s *Store) GetRidePaymentByRide(ctx context.Context, rideID uuid.UUID) (*RidePayment, error) {
	const q = `
        SELECT id, ride_id, partner_id, amount_paise, payment_method, status, wallet_txn_id, upi_txn_ref, created_at, settled_at
        FROM rider_ride_payments
        WHERE ride_id = $1
        ORDER BY created_at DESC
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, rideID)
	p, err := scanRidePayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRidePaymentNotFound
		}
		return nil, err
	}
	return p, nil
}

// IncrementSubscriptionLeadsUsed bumps leads_used by 1 on the partner's
// active subscription. Returns the new value (zero when no active row).
func (s *Store) IncrementSubscriptionLeadsUsed(ctx context.Context, partnerID uuid.UUID) (int, error) {
	const q = `
        UPDATE rider_partner_subscriptions
        SET leads_used = leads_used + 1, updated_at = NOW()
        WHERE id = (
            SELECT id FROM rider_partner_subscriptions
            WHERE partner_id = $1 AND status IN ('trial','active','grace_period')
            ORDER BY expires_at DESC LIMIT 1
        )
        RETURNING leads_used`
	var newCount int
	if err := s.db.QueryRow(ctx, q, partnerID).Scan(&newCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("increment leads_used: %w", err)
	}
	return newCount, nil
}

// IncrementPartnerCancelled bumps total_rides_cancelled and recomputes the
// cancellation_rate (cancellations / (cancellations + completions)).
func (s *Store) IncrementPartnerCancelled(ctx context.Context, partnerID uuid.UUID) error {
	const q = `
        UPDATE rider_partners
        SET total_rides_cancelled = total_rides_cancelled + 1,
            cancellation_rate     = CASE
                WHEN (total_rides_cancelled + 1 + total_rides_completed) > 0
                THEN ROUND(((total_rides_cancelled + 1)::numeric / (total_rides_cancelled + 1 + total_rides_completed)) * 100, 2)
                ELSE 0
            END,
            updated_at = NOW()
        WHERE id = $1`
	_, err := s.db.Exec(ctx, q, partnerID)
	return err
}

// IncrementPartnerCompleted bumps total_rides_completed and recomputes the
// rolling cancellation/acceptance rates.
func (s *Store) IncrementPartnerCompleted(ctx context.Context, partnerID uuid.UUID) error {
	const q = `
        UPDATE rider_partners
        SET total_rides_completed = total_rides_completed + 1,
            cancellation_rate     = CASE
                WHEN (total_rides_completed + 1 + total_rides_cancelled) > 0
                THEN ROUND((total_rides_cancelled::numeric / (total_rides_completed + 1 + total_rides_cancelled)) * 100, 2)
                ELSE 0
            END,
            updated_at = NOW()
        WHERE id = $1`
	_, err := s.db.Exec(ctx, q, partnerID)
	return err
}

// UpdatePartnerRating recomputes the rolling 30-day rating from completed
// rides and writes the new average to rider_partners.rating.
func (s *Store) UpdatePartnerRating(ctx context.Context, partnerID uuid.UUID) error {
	const q = `
        UPDATE rider_partners
        SET rating = COALESCE((
                SELECT ROUND(AVG(rating)::numeric, 2)
                FROM rider_rides
                WHERE partner_id = $1
                  AND rating IS NOT NULL
                  AND completed_at >= NOW() - INTERVAL '30 days'
            ), 0),
            updated_at = NOW()
        WHERE id = $1`
	_, err := s.db.Exec(ctx, q, partnerID)
	return err
}

func scanRidePayment(row pgx.Row) (*RidePayment, error) {
	var p RidePayment
	if err := row.Scan(&p.ID, &p.RideID, &p.PartnerID, &p.AmountPaise, &p.PaymentMethod, &p.Status, &p.WalletTxnID, &p.UPITxnRef, &p.CreatedAt, &p.SettledAt); err != nil {
		return nil, err
	}
	return &p, nil
}
