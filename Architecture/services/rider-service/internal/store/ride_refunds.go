package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Refund statuses (rider_ride_refunds.status).
const (
	RefundRequested = "requested"
	RefundAccepted  = "accepted"
	RefundRefunded  = "refunded"
	RefundFailed    = "failed"
)

var (
	// ErrRefundNotFound is returned when no refund row matches.
	ErrRefundNotFound = errors.New("ride_refund: not found")
	// ErrRefundCashPayment: the ride was paid in cash; a cash dispute is an
	// outstanding waiver or a manual matter, never a PSP refund.
	ErrRefundCashPayment = errors.New("ride_refund: cash payments cannot be refunded")
	// ErrRefundNotRefundable: the payment is not succeeded or
	// partially_refunded (nothing captured, or already fully refunded).
	ErrRefundNotRefundable = errors.New("ride_refund: payment is not refundable")
	// ErrRefundExceedsRemaining: amount > amount_paise - refunded_paise -
	// refunds still in flight.
	ErrRefundExceedsRemaining = errors.New("ride_refund: amount exceeds the remaining refundable amount")
)

const rideRefundColumns = `id, ride_id, payment_id, intent_id, amount_paise, reason, status, requested_by, provider_reference, failure_reason, created_at, updated_at`

func scanRideRefund(row pgx.Row) (*RideRefund, error) {
	var r RideRefund
	if err := row.Scan(&r.ID, &r.RideID, &r.PaymentID, &r.IntentID, &r.AmountPaise, &r.Reason, &r.Status, &r.RequestedBy,
		&r.ProviderReference, &r.FailureReason, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateRideRefundInput is the admin refund request. AmountPaise 0 means
// the full remaining amount.
type CreateRideRefundInput struct {
	ID          uuid.UUID
	RideID      uuid.UUID
	AmountPaise int64
	Reason      string
	RequestedBy uuid.UUID
}

// CreateRideRefund inserts a `requested` refund for the ride's latest
// payment, with the payment row locked: the payment must be an online one
// in succeeded / partially_refunded, and the amount no more than what is
// left after applied refunds and refunds still in flight. It returns the
// row and the payment it refunds.
func (s *Store) CreateRideRefund(ctx context.Context, in CreateRideRefundInput) (*RideRefund, *RidePayment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	p, err := scanRidePayment(tx.QueryRow(ctx, `
        SELECT `+ridePaymentColumns+`
        FROM rider_ride_payments WHERE ride_id = $1
        ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, in.RideID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrRidePaymentNotFound
		}
		return nil, nil, fmt.Errorf("lock ride payment: %w", err)
	}
	if p.PaymentMethod == "cash" {
		return nil, p, ErrRefundCashPayment
	}
	if (p.Status != "succeeded" && p.Status != "partially_refunded") || p.IntentID == nil {
		return nil, p, ErrRefundNotRefundable
	}
	var inFlight int64
	if err := tx.QueryRow(ctx, `
        SELECT COALESCE(SUM(amount_paise), 0) FROM rider_ride_refunds
        WHERE payment_id = $1 AND status IN ('requested','accepted')`, p.ID).Scan(&inFlight); err != nil {
		return nil, p, fmt.Errorf("sum in-flight refunds: %w", err)
	}
	remaining := p.AmountPaise - p.RefundedPaise - inFlight
	amount := in.AmountPaise
	if amount == 0 {
		amount = remaining
	}
	if amount <= 0 || amount > remaining {
		return nil, p, ErrRefundExceedsRemaining
	}
	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	r, err := scanRideRefund(tx.QueryRow(ctx, `
        INSERT INTO rider_ride_refunds (id, ride_id, payment_id, intent_id, amount_paise, reason, requested_by)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING `+rideRefundColumns, id, p.RideID, p.ID, *p.IntentID, amount, in.Reason, in.RequestedBy))
	if err != nil {
		return nil, p, fmt.Errorf("create ride refund: %w", err)
	}
	return r, p, tx.Commit(ctx)
}

// MarkRideRefundAccepted records that payments-service took the refund
// command (money has not moved).
func (s *Store) MarkRideRefundAccepted(ctx context.Context, id uuid.UUID, providerRef string) (*RideRefund, error) {
	r, err := scanRideRefund(s.db.QueryRow(ctx, `
        UPDATE rider_ride_refunds
        SET status = 'accepted', provider_reference = COALESCE(NULLIF($2, ''), provider_reference), updated_at = NOW()
        WHERE id = $1 AND status = 'requested'
        RETURNING `+rideRefundColumns, id, providerRef))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefundNotFound
		}
		return nil, fmt.Errorf("mark refund accepted: %w", err)
	}
	return r, nil
}

// MarkRideRefundFailed records a refused refund command.
func (s *Store) MarkRideRefundFailed(ctx context.Context, id uuid.UUID, reason string) (*RideRefund, error) {
	r, err := scanRideRefund(s.db.QueryRow(ctx, `
        UPDATE rider_ride_refunds
        SET status = 'failed', failure_reason = $2, updated_at = NOW()
        WHERE id = $1 AND status IN ('requested','accepted')
        RETURNING `+rideRefundColumns, id, reason))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefundNotFound
		}
		return nil, fmt.Errorf("mark refund failed: %w", err)
	}
	return r, nil
}

// GetRideRefund returns one refund row.
func (s *Store) GetRideRefund(ctx context.Context, id uuid.UUID) (*RideRefund, error) {
	r, err := scanRideRefund(s.db.QueryRow(ctx, `SELECT `+rideRefundColumns+` FROM rider_ride_refunds WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefundNotFound
		}
		return nil, err
	}
	return r, nil
}

// ListRideRefunds is the admin refund list: optional status and ride
// filters, newest first.
func (s *Store) ListRideRefunds(ctx context.Context, status string, rideID *uuid.UUID, limit, offset int) ([]RideRefund, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+rideRefundColumns+`
        FROM rider_ride_refunds
        WHERE ($1 = '' OR status = $1)
          AND ($2::uuid IS NULL OR ride_id = $2)
        ORDER BY created_at DESC
        LIMIT $3 OFFSET $4`, status, rideID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list ride refunds: %w", err)
	}
	defer rows.Close()
	out := []RideRefund{}
	for rows.Next() {
		r, err := scanRideRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListRefundsByRide returns a ride's refunds oldest first (the receipt).
func (s *Store) ListRefundsByRide(ctx context.Context, rideID uuid.UUID) ([]RideRefund, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+rideRefundColumns+` FROM rider_ride_refunds WHERE ride_id = $1 ORDER BY created_at ASC`, rideID)
	if err != nil {
		return nil, fmt.Errorf("list refunds by ride: %w", err)
	}
	defer rows.Close()
	out := []RideRefund{}
	for rows.Next() {
		r, err := scanRideRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
