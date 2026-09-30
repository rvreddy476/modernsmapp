package postgres

// Retrying the payment of an order whose first payment failed.
//
// A payment.failed event releases the order's stock and moves it to
// `payment_failed` (ApplyPaymentFailed). The cart was already emptied at
// checkout, so before this the buyer's only way forward was to rebuild the
// bag and place a second order. Migration 010's matrix has admitted
// `payment_failed -> payment_pending` for the customer since it was written;
// this is the first route that performs it.
//
// The move is ONE transaction: the order row is locked, every line's stock is
// locked in deterministic order and checked, every line is re-held for the
// checkout TTL, and the status moves through the same guarded helper the
// seller's pack/ship use. Stock gone on any line means nothing is reserved
// and the order stays `payment_failed`.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPaymentNotRetryable: the order is not in payment_failed.
var ErrPaymentNotRetryable = errors.New("this order's payment cannot be retried")

// RetryPaymentReservation re-holds the order's lines and moves it back to
// payment_pending as the customer. It returns the attempt number — how many
// times this order has entered payment_pending, counted from its history —
// which the caller folds into the payment intent's idempotency key so
// payments-service opens a FRESH intent rather than answering with the one
// that failed.
func (s *Store) RetryPaymentReservation(ctx context.Context, orderID, userID uuid.UUID) (attempt int, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type', 'customer', true)`); err != nil {
		return 0, err
	}

	var (
		customerID uuid.UUID
		status     string
	)
	err = tx.QueryRow(ctx,
		`SELECT customer_user_id, status FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&customerID, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrOrderNotFoundP0
		}
		return 0, err
	}
	if customerID != userID {
		return 0, ErrNotOrderOwnerP0
	}
	if status != "payment_failed" {
		return 0, fmt.Errorf("%w: order is %s", ErrPaymentNotRetryable, status)
	}

	// The lines, as the order recorded them. Quantity and identity only: the
	// price was fixed at checkout and is not re-read here.
	rows, err := tx.Query(ctx,
		`SELECT variant_id, product_id, product_title, quantity
		   FROM order_items WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		return 0, err
	}
	var lines []pricedLine
	for rows.Next() {
		var l pricedLine
		if err := rows.Scan(&l.VariantID, &l.ProductID, &l.Title, &l.Quantity); err != nil {
			rows.Close()
			return 0, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return 0, fmt.Errorf("%w: order has no lines", ErrPaymentNotRetryable)
	}

	// Stock, locked in deterministic order, exactly as checkout does it.
	oos, err := lockInventory(ctx, tx, lines)
	if err != nil {
		return 0, err
	}
	if len(oos) > 0 {
		return 0, &OutOfStockError{Lines: oos}
	}
	if err := reserveLinesTx(ctx, tx, orderID, userID, lines); err != nil {
		return 0, err
	}

	// payment_failed -> payment_pending, by the customer, with its history
	// row. The failed intent is unbound so the service opens a fresh one, and
	// payment_status returns to pending so the state pair is the same one a
	// freshly checked-out order carries.
	if _, err := transitionOrderStatusTx(ctx, tx, orderID, "payment_pending", &userID, "customer", "payment retried"); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET payment_status = 'pending', payment_intent_id = NULL, updated_at = NOW()
		  WHERE id = $1`, orderID); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM order_status_history WHERE order_id = $1 AND to_status = 'payment_pending'`,
		orderID).Scan(&attempt); err != nil {
		return 0, err
	}
	if attempt < 2 {
		// The initial row is written at checkout; a retry is at least the
		// second entry. A database without that row still gets a distinct key.
		attempt = 2
	}
	return attempt, tx.Commit(ctx)
}

// OrderPaymentAttempt is which payment attempt an order in payment_pending is
// on: the number of times it has entered payment_pending, at least 1. The
// same count RetryPaymentReservation returns, so an intent that failed to
// open right after a retry is re-requested under the retry's key — never
// under the first attempt's, which payments-service would answer with the
// intent that already failed.
func (s *Store) OrderPaymentAttempt(ctx context.Context, orderID uuid.UUID) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM order_status_history WHERE order_id = $1 AND to_status = 'payment_pending'`,
		orderID).Scan(&n); err != nil {
		return 0, err
	}
	if n < 1 {
		n = 1
	}
	return n, nil
}
