package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Outstanding statuses.
const (
	OutstandingPending = "pending"
	OutstandingSettled = "settled"
	OutstandingWaived  = "waived"
)

// ErrOutstandingNotFound is returned when a row does not exist or is no
// longer pending.
var ErrOutstandingNotFound = errors.New("outstanding: not found or not pending")

// ErrOutstandingChanged is returned when a ride tries to reserve outstanding
// lines that are no longer pending and free (paid, waived or reserved by
// another ride since the quote).
var ErrOutstandingChanged = errors.New("outstanding: changed since the quote")

const outstandingColumns = `
        id, customer_user_id, ride_id, amount_paise, reason, status, settled_by_ride_id,
        waived_by, waive_reason, created_at, settled_at`

func scanOutstanding(row pgx.Row) (*CustomerOutstanding, error) {
	var o CustomerOutstanding
	if err := row.Scan(&o.ID, &o.CustomerUserID, &o.RideID, &o.AmountPaise, &o.Reason, &o.Status, &o.SettledByRideID,
		&o.WaivedBy, &o.WaiveReason, &o.CreatedAt, &o.SettledAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// ListPendingOutstanding returns the customer's pending fees that no active
// ride has reserved, oldest first. This is what the next quote charges.
func (s *Store) ListPendingOutstanding(ctx context.Context, customerID uuid.UUID) ([]CustomerOutstanding, error) {
	const q = `
        SELECT ` + outstandingColumns + `
        FROM rider_customer_outstanding
        WHERE customer_user_id = $1 AND status = 'pending' AND settled_by_ride_id IS NULL
        ORDER BY created_at ASC`
	rows, err := s.db.Query(ctx, q, customerID)
	if err != nil {
		return nil, fmt.Errorf("list pending outstanding: %w", err)
	}
	defer rows.Close()
	var out []CustomerOutstanding
	for rows.Next() {
		o, err := scanOutstanding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// CreateOutstandingTx records a fee the customer owes for rideID. One row
// per ride (ux_rider_outstanding_ride); a second call is a no-op.
func CreateOutstandingTx(ctx context.Context, tx pgx.Tx, customerID, rideID uuid.UUID, amountPaise int64, reason string) error {
	if amountPaise <= 0 {
		return nil
	}
	const q = `
        INSERT INTO rider_customer_outstanding (customer_user_id, ride_id, amount_paise, reason)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (ride_id) DO NOTHING`
	if _, err := tx.Exec(ctx, q, customerID, rideID, amountPaise, reason); err != nil {
		return fmt.Errorf("create outstanding: %w", err)
	}
	return nil
}

// ReserveOutstandingTx binds the pending rows a quote charged to the ride
// being created. Every id must still be pending and unreserved, or the
// customer's balance changed since the quote and the booking is refused.
func ReserveOutstandingTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, customerID, rideID uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	const q = `
        UPDATE rider_customer_outstanding
        SET settled_by_ride_id = $3
        WHERE id = ANY($1) AND customer_user_id = $2 AND status = 'pending' AND settled_by_ride_id IS NULL`
	tag, err := tx.Exec(ctx, q, ids, customerID, rideID)
	if err != nil {
		return fmt.Errorf("reserve outstanding: %w", err)
	}
	if int(tag.RowsAffected()) != len(ids) {
		return ErrOutstandingChanged
	}
	return nil
}

// SettleOutstandingByRideTx marks the rows the completed ride charged as
// settled.
func SettleOutstandingByRideTx(ctx context.Context, tx pgx.Tx, rideID uuid.UUID) error {
	const q = `
        UPDATE rider_customer_outstanding
        SET status = 'settled', settled_at = NOW()
        WHERE settled_by_ride_id = $1 AND status = 'pending'`
	if _, err := tx.Exec(ctx, q, rideID); err != nil {
		return fmt.Errorf("settle outstanding: %w", err)
	}
	return nil
}

// ReleaseOutstandingByRideTx frees the rows a cancelled ride had reserved so
// the next quote charges them again.
func ReleaseOutstandingByRideTx(ctx context.Context, tx pgx.Tx, rideID uuid.UUID) error {
	const q = `
        UPDATE rider_customer_outstanding
        SET settled_by_ride_id = NULL
        WHERE settled_by_ride_id = $1 AND status = 'pending'`
	if _, err := tx.Exec(ctx, q, rideID); err != nil {
		return fmt.Errorf("release outstanding: %w", err)
	}
	return nil
}

// WaiveOutstanding is the admin waiver. Only a pending row can be waived.
func (s *Store) WaiveOutstanding(ctx context.Context, id, adminID uuid.UUID, reason string) (*CustomerOutstanding, error) {
	const q = `
        UPDATE rider_customer_outstanding
        SET status = 'waived', waived_by = $2, waive_reason = $3, settled_at = NOW(), settled_by_ride_id = NULL
        WHERE id = $1 AND status = 'pending'
        RETURNING ` + outstandingColumns
	o, err := scanOutstanding(s.db.QueryRow(ctx, q, id, adminID, reason))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOutstandingNotFound
		}
		return nil, fmt.Errorf("waive outstanding: %w", err)
	}
	return o, nil
}

// GetOutstanding returns one row by id.
func (s *Store) GetOutstanding(ctx context.Context, id uuid.UUID) (*CustomerOutstanding, error) {
	const q = `SELECT ` + outstandingColumns + ` FROM rider_customer_outstanding WHERE id = $1`
	o, err := scanOutstanding(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOutstandingNotFound
		}
		return nil, err
	}
	return o, nil
}

// ListOutstanding is the admin list: optional customer and status filters.
func (s *Store) ListOutstanding(ctx context.Context, customerID *uuid.UUID, status string, limit, offset int) ([]CustomerOutstanding, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        SELECT ` + outstandingColumns + `
        FROM rider_customer_outstanding
        WHERE ($1::uuid IS NULL OR customer_user_id = $1)
          AND ($2 = '' OR status = $2)
        ORDER BY created_at DESC
        LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, q, customerID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list outstanding: %w", err)
	}
	defer rows.Close()
	out := []CustomerOutstanding{}
	for rows.Next() {
		o, err := scanOutstanding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// unusedTime keeps the time import honest for future settled_at helpers.
var _ = time.Now
