// Order payment / status transitions.
//
// Before this file the paid path was two unguarded UPDATEs (payment_status
// then status) with the "is it already paid?" check done in Go on a row
// read moments earlier. A Razorpay webhook (via the payments consumer)
// and the customer's confirm call racing each other both passed the read
// check, both wrote, and both fired the side effects — two order.paid
// events, two fulfillment jobs, two stock deductions. Every transition
// now runs as ONE guarded UPDATE whose WHERE clause encodes the allowed
// source states, so exactly one concurrent caller observes Applied=true
// and the rest see the already-converged row.
package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrOrderNotFound is returned by the transition helpers when the order
// row does not exist at all (as opposed to "exists but not in an allowed
// source state", which is reported through Applied=false).
var ErrOrderNotFound = errors.New("order not found")

// paymentStatusTransitions is the payment_status state machine: target →
// allowed source states. Anything not listed is refused by the UPDATE's
// WHERE clause.
//
//	pending ──► processing ──► paid ──► refund_pending ──► refunded / partially_refunded
//	   │             │           ▲
//	   └──► failed ◄─┘           │  (a retry after a failed attempt may still succeed)
//	           └─────────────────┘
var paymentStatusTransitions = map[string][]string{
	"processing":         {"pending"},
	"paid":               {"pending", "processing", "failed"},
	"failed":             {"pending", "processing"},
	"refund_pending":     {"paid", "partially_refunded"},
	"refunded":           {"paid", "refund_pending", "partially_refunded"},
	"partially_refunded": {"paid", "refund_pending", "partially_refunded"},
}

// orderStatusPayable lists the order statuses in which money may still be
// applied. "confirmed" is included for COD and B2B credit-terms orders,
// which are confirmed at checkout and settle later — for them the paid
// transition changes payment_status only and leaves status alone.
var orderStatusPayable = []string{"created", "payment_pending", "confirmed"}

// orderStatusConfirmOnPaid lists the statuses that flip to "confirmed" as
// part of the paid transition.
var orderStatusConfirmOnPaid = []string{"created", "payment_pending"}

// PaymentStatusAllowedFrom returns the source states from which
// payment_status may move to `to` (nil for an unknown target).
func PaymentStatusAllowedFrom(to string) []string {
	src := paymentStatusTransitions[to]
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// PaymentStatusTransitionAllowed reports whether payment_status may move
// from → to under the table above.
func PaymentStatusTransitionAllowed(from, to string) bool {
	return contains(paymentStatusTransitions[to], from)
}

// OrderPayable reports whether an order in (status, paymentStatus) can
// accept the paid transition. This is exactly the predicate MarkOrderPaid
// encodes in SQL; keep the two in sync (the table test pins both).
func OrderPayable(status, paymentStatus string) bool {
	return contains(orderStatusPayable, status) && PaymentStatusTransitionAllowed(paymentStatus, "paid")
}

// OrderStatusAfterPaid is the status an order lands in once the paid
// transition applies from `status` ("confirmed" for pre-payment states,
// unchanged otherwise).
func OrderStatusAfterPaid(status string) string {
	if contains(orderStatusConfirmOnPaid, status) {
		return "confirmed"
	}
	return status
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// PaidTransition is what MarkOrderPaid observed. When Applied is false the
// From*/To* fields carry the row's current values so the caller can tell
// "already paid, idempotent no-op" from "cancelled — money arrived for a
// dead order".
type PaidTransition struct {
	Applied bool
	// FromStatus / FromPaymentStatus are the values before this call (or
	// the current values when Applied is false).
	FromStatus        string
	FromPaymentStatus string
	// Status / PaymentStatus are the values after this call.
	Status        string
	PaymentStatus string
}

// MarkOrderPaid applies the paid transition atomically:
//
//	payment_status ← 'paid'      iff payment_status ∈ allowed-from("paid")
//	status         ← 'confirmed' iff status ∈ {created, payment_pending}
//	                              (left unchanged for confirmed COD/credit orders)
//	WHERE status ∈ payable states
//
// plus an order_status_history row when status actually changed — all in
// one transaction. Concurrent callers (webhook consumer + customer
// confirm) serialise on the row; exactly one sees Applied=true.
func (s *Store) MarkOrderPaid(ctx context.Context, orderID uuid.UUID, paymentID, gateway string, actorID *uuid.UUID, actorType string) (PaidTransition, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return PaidTransition{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var t PaidTransition
	err = tx.QueryRow(ctx, `
		WITH prev AS (
			SELECT id, status, payment_status
			FROM orders
			WHERE id = $1
			FOR UPDATE
		)
		UPDATE orders o
		SET payment_status  = 'paid',
		    payment_id      = COALESCE(NULLIF($2, ''), o.payment_id),
		    payment_gateway = COALESCE(NULLIF($3, ''), o.payment_gateway),
		    status          = CASE WHEN o.status = ANY($4) THEN 'confirmed' ELSE o.status END,
		    updated_at      = NOW()
		FROM prev
		WHERE o.id = prev.id
		  AND prev.payment_status = ANY($5)
		  AND prev.status = ANY($6)
		RETURNING prev.status, prev.payment_status, o.status, o.payment_status`,
		orderID, paymentID, gateway,
		orderStatusConfirmOnPaid, PaymentStatusAllowedFrom("paid"), orderStatusPayable,
	).Scan(&t.FromStatus, &t.FromPaymentStatus, &t.Status, &t.PaymentStatus)
	switch {
	case err == nil:
		t.Applied = true
	case errors.Is(err, pgx.ErrNoRows):
		// Guard refused (or no such order). Report the current row so the
		// caller can decide between idempotent no-op and a real problem.
		if err := tx.QueryRow(ctx, `SELECT status, payment_status FROM orders WHERE id = $1`, orderID).
			Scan(&t.Status, &t.PaymentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return PaidTransition{}, ErrOrderNotFound
			}
			return PaidTransition{}, err
		}
		t.FromStatus, t.FromPaymentStatus = t.Status, t.PaymentStatus
		return t, tx.Commit(ctx)
	default:
		return PaidTransition{}, err
	}

	if t.FromStatus != t.Status {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_status_history (id,order_id,from_status,to_status,changed_by,actor_type,notes,created_at)
			VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,'payment confirmed',NOW())`,
			orderID, t.FromStatus, t.Status, actorID, actorType,
		); err != nil {
			return PaidTransition{}, err
		}
	}
	return t, tx.Commit(ctx)
}

// TransitionPaymentStatus moves payment_status to `to` when the row is in
// one of the table's allowed source states. Returns applied=false (no
// error) when the guard refuses — the caller decides whether that is an
// idempotent replay or a problem. paymentID / gateway are only written
// when non-empty.
func (s *Store) TransitionPaymentStatus(ctx context.Context, orderID uuid.UUID, to, paymentID, gateway string) (bool, error) {
	allowed := PaymentStatusAllowedFrom(to)
	if len(allowed) == 0 {
		return false, errors.New("unknown payment_status target: " + to)
	}
	cmd, err := s.db.Exec(ctx, `
		UPDATE orders
		SET payment_status  = $2,
		    payment_id      = COALESCE(NULLIF($3, ''), payment_id),
		    payment_gateway = COALESCE(NULLIF($4, ''), payment_gateway),
		    updated_at      = NOW()
		WHERE id = $1 AND payment_status = ANY($5)`,
		orderID, to, paymentID, gateway, allowed)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() == 1, nil
}
