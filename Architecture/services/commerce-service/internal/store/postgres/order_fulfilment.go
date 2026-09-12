// Seller-side order status transitions: pack and ship.
//
// Migration 010 put the D6 matrix in `order_status_transitions` and wrote a
// trigger that refuses anything absent from it. The trigger is attached only
// by the gated migration (998), so on a boot-migrated database nothing
// guards `UPDATE orders SET status`, and the one write that flipped an order
// to `shipped` did so as actor "system" (a pair the matrix does not hold) and
// discarded the error. On a gated database that write was silently refused
// and the order stayed `confirmed` behind a booked shipment.
//
// Every transition here consults the SAME table the trigger reads, in the
// same transaction, so the Go guard and the database guard cannot disagree,
// and the history row is written whether or not the trigger is present.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrTransitionNotPermitted is returned when (current status, target status,
// actor) is not a row of order_status_transitions. Wrapped with the triple so
// the log says which move was refused; callers match with errors.Is.
var ErrTransitionNotPermitted = errors.New("order status transition not permitted")

// StatusTransition reports what a guarded transition observed. Applied is
// false when the order was ALREADY in the target state, which the callers
// treat as an idempotent repeat rather than a refusal.
type StatusTransition struct {
	Applied bool
	From    string
	To      string
}

// transitionAllowedTx asks the matrix. The lookup is a query rather than a
// Go map so a migration that changes the matrix changes this too.
func transitionAllowedTx(ctx context.Context, tx pgx.Tx, from, to, actorType string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM order_status_transitions
			 WHERE from_status = $1 AND to_status = $2 AND actor_type = $3
		)`, from, to, actorOr(actorType)).Scan(&ok)
	return ok, err
}

// transitionOrderStatusTx moves one order to `to` as `actorType`, under the
// row lock, refusing anything the matrix does not permit.
//
// The history row: when trg_order_transition is attached the trigger writes
// it in the same statement as the UPDATE, and writing a second one here
// would double every audit line. When it is not attached nobody writes it.
// So the INSERT is conditional on the trigger's absence, and a follow-up
// UPDATE annotates whichever row exists with the actor id and note, which
// the trigger cannot know.
func transitionOrderStatusTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, to string, actorID *uuid.UUID, actorType, notes string) (StatusTransition, error) {
	actor := actorOr(actorType)
	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type', $1, true)`, actor); err != nil {
		return StatusTransition{}, err
	}

	var from string
	err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&from)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StatusTransition{}, ErrOrderNotFoundP0
		}
		return StatusTransition{}, err
	}
	if from == to {
		return StatusTransition{Applied: false, From: from, To: to}, nil
	}

	allowed, err := transitionAllowedTx(ctx, tx, from, to, actor)
	if err != nil {
		return StatusTransition{}, err
	}
	if !allowed {
		return StatusTransition{From: from, To: to},
			fmt.Errorf("%w: %s -> %s by %s", ErrTransitionNotPermitted, from, to, actor)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = $2, updated_at = NOW() WHERE id = $1`, orderID, to); err != nil {
		if isCheckViolation(err) {
			// The trigger disagreed with the table read a moment ago, which
			// can only happen if the matrix changed under us. Report it as
			// the same refusal rather than a 500.
			return StatusTransition{From: from, To: to},
				fmt.Errorf("%w: %s -> %s by %s", ErrTransitionNotPermitted, from, to, actor)
		}
		return StatusTransition{}, err
	}

	//
	// clock_timestamp(), not NOW(): a seller's ship is two steps in ONE
	// transaction, and NOW() is the transaction's start for both, which
	// leaves "packed then shipped" and "shipped then packed" tied on the
	// timeline. clock_timestamp() advances between the two statements.
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_status_history (id, order_id, from_status, to_status, changed_by, actor_type, notes, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, $4, $5, NULLIF($6, ''), clock_timestamp()
		 WHERE NOT EXISTS (
			SELECT 1 FROM pg_trigger
			 WHERE tgrelid = 'orders'::regclass
			   AND tgname = 'trg_order_transition'
			   AND tgenabled <> 'D'
		 )`, orderID, from, to, actorID, actor, notes); err != nil {
		return StatusTransition{}, err
	}
	// Annotate the row the trigger wrote (a no-op on the row we wrote,
	// because both values are already set). The trigger stamps NOW(), so
	// its rows get the same sequencing fix; the shift is the microseconds
	// between two statements of one transaction.
	if _, err := tx.Exec(ctx, `
		UPDATE order_status_history
		   SET changed_by = COALESCE(changed_by, $4),
		       notes      = COALESCE(notes, NULLIF($5, '')),
		       created_at = GREATEST(created_at, clock_timestamp())
		 WHERE id = (
			SELECT id FROM order_status_history
			 WHERE order_id = $1 AND from_status = $2 AND to_status = $3
			 ORDER BY created_at DESC LIMIT 1
		 )`, orderID, from, to, actorID, notes); err != nil {
		return StatusTransition{}, err
	}
	return StatusTransition{Applied: true, From: from, To: to}, nil
}

// PackOrder moves confirmed -> packed as the given actor (the matrix admits
// only a seller). A repeat on an already-packed order is Applied=false, nil.
func (s *Store) PackOrder(ctx context.Context, orderID uuid.UUID, actorID *uuid.UUID, actorType string) (StatusTransition, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return StatusTransition{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	t, err := transitionOrderStatusTx(ctx, tx, orderID, "packed", actorID, actorType, "packed by seller")
	if err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}

// shippedOrBeyond are the states in which a ship request is a repeat, not a
// refusal: the parcel is already with the courier.
var shippedOrBeyond = map[string]bool{"shipped": true, "out_for_delivery": true, "delivered": true}

// MarkOrderShipped moves the order to `shipped` as the given actor, going
// through `packed` first when the matrix requires it.
//
// A seller booking a shipment on a `confirmed` order has, by booking it,
// packed it; the matrix has no seller row for confirmed -> shipped, so the
// move is recorded as two audited steps rather than one unrecorded jump. The
// fulfilment worker books as "system", whose rows migration 033 adds. Which
// path applies is decided by the table, not by a Go switch, so the two
// actors' rules live in one place.
func (s *Store) MarkOrderShipped(ctx context.Context, orderID uuid.UUID, actorID *uuid.UUID, actorType, notes string) (StatusTransition, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return StatusTransition{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var from string
	if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StatusTransition{}, ErrOrderNotFoundP0
		}
		return StatusTransition{}, err
	}
	if shippedOrBeyond[from] {
		return StatusTransition{Applied: false, From: from, To: from}, tx.Commit(ctx)
	}

	actor := actorOr(actorType)
	// Candidate paths, most direct first. Every step of a path must be a
	// row of the matrix for that actor or the path is not taken.
	paths := [][]string{{"shipped"}, {"packed", "shipped"}}
	var chosen []string
	for _, path := range paths {
		cur, ok := from, true
		for _, step := range path {
			if ok, err = transitionAllowedTx(ctx, tx, cur, step, actor); err != nil {
				return StatusTransition{}, err
			}
			if !ok {
				break
			}
			cur = step
		}
		if ok {
			chosen = path
			break
		}
	}
	if chosen == nil {
		return StatusTransition{From: from, To: "shipped"},
			fmt.Errorf("%w: %s -> shipped by %s", ErrTransitionNotPermitted, from, actor)
	}

	for _, step := range chosen {
		stepNote := notes
		if step == "packed" {
			stepNote = "packed at shipment booking"
		}
		if _, err := transitionOrderStatusTx(ctx, tx, orderID, step, actorID, actor, stepNote); err != nil {
			return StatusTransition{From: from, To: "shipped"}, err
		}
	}
	return StatusTransition{Applied: true, From: from, To: "shipped"}, tx.Commit(ctx)
}

// ListOrderStatusHistory returns the audit trail oldest first, so a client
// renders it as a timeline without sorting.
func (s *Store) ListOrderStatusHistory(ctx context.Context, orderID uuid.UUID) ([]*OrderStatusHistory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, from_status, to_status, changed_by, actor_type, notes, created_at
		  FROM order_status_history
		 WHERE order_id = $1
		 ORDER BY created_at ASC, id ASC
		 LIMIT 200`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*OrderStatusHistory, 0, 8)
	for rows.Next() {
		h := &OrderStatusHistory{}
		if err := rows.Scan(&h.ID, &h.OrderID, &h.FromStatus, &h.ToStatus, &h.ChangedBy, &h.ActorType, &h.Notes, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
