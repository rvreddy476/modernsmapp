package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LockedBooking is a booking read FOR UPDATE inside a changing transaction,
// handed to the service's decision so the rule is applied to the row that
// will be written.
type LockedBooking struct {
	ID            uuid.UUID
	Customer      uuid.UUID
	CategoryID    uuid.UUID
	CityCode      string
	Status        string
	SlotStart     time.Time
	SlotEnd       time.Time
	Duration      int
	PaidPaise     int64
	RefundedPaise int64
	// Refundable is what may still be refunded: captured minus every refund
	// that has not failed.
	Refundable      int64
	PaymentID       *uuid.UUID
	PaymentStatus   string
	RescheduleCount int
	ReservedProID   *uuid.UUID
	// ProLateAt: the professional had not arrived 15 min after the slot
	// (A4); the customer may cancel free of charge.
	ProLateAt *time.Time
}

func lockBookingTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, customer *uuid.UUID) (*LockedBooking, error) {
	var b LockedBooking
	var payStatus *string
	err := tx.QueryRow(ctx, `
		SELECT b.id, b.customer_user_id, b.category_id, b.city_code, b.status, b.slot_start, b.slot_end, b.duration_minutes,
		       b.paid_paise, b.refunded_paise, b.reschedule_count, b.reserved_pro_id, b.pro_late_at,
		       (SELECT p.id FROM doorstep.payments p WHERE p.booking_id = b.id AND p.reference_type = 'doorstep_booking'),
		       (SELECT p.status FROM doorstep.payments p WHERE p.booking_id = b.id AND p.reference_type = 'doorstep_booking')
		FROM doorstep.bookings b
		WHERE b.id = $1 AND ($2::uuid IS NULL OR b.customer_user_id = $2)
		FOR UPDATE OF b`, id, customer).Scan(&b.ID, &b.Customer, &b.CategoryID, &b.CityCode, &b.Status, &b.SlotStart, &b.SlotEnd,
		&b.Duration, &b.PaidPaise, &b.RefundedPaise, &b.RescheduleCount, &b.ReservedProID, &b.ProLateAt, &b.PaymentID, &payStatus)
	if err != nil {
		return nil, mapErr(err)
	}
	if payStatus != nil {
		b.PaymentStatus = *payStatus
	}
	var pending int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_paise), 0)::bigint FROM doorstep.refunds
		WHERE booking_id = $1 AND status <> 'failed'`, id).Scan(&pending); err != nil {
		return nil, err
	}
	b.Refundable = b.PaidPaise - pending
	if b.Refundable < 0 {
		b.Refundable = 0
	}
	return &b, nil
}

// CancelDecision is what the service decided for a locked booking.
type CancelDecision struct {
	ActorKind   string // customer | admin | system (history and cancelled_by_kind)
	ActorID     *uuid.UUID
	Reason      string
	FeePaise    int64
	RefundPaise int64
	RefundCause string
	RefundKey   string
}

// CancelBooking cancels a booking in one transaction: status, the calendar
// blocks released, open offers cancelled, history, the refund row (with its
// deterministic key) when money goes back, doorstep.booking.cancelled and —
// for an admin — the audit row. decide sees the locked row; its error
// aborts. It returns the refund row to submit, if any.
func (s *Store) CancelBooking(ctx context.Context, id uuid.UUID, customer *uuid.UUID, audit *Actor, at time.Time,
	decide func(*LockedBooking) (*CancelDecision, error)) (*uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := lockBookingTx(ctx, tx, id, customer)
	if err != nil {
		return nil, err
	}
	d, err := decide(b)
	if err != nil {
		return nil, err
	}
	reason := d.Reason
	if _, err := tx.Exec(ctx, `
		UPDATE doorstep.bookings SET status = 'cancelled', cancelled_at = $2, cancelled_by_kind = $3, cancel_reason = $4,
		       cancellation_fee_paise = $5, hold_expires_at = NULL, version = version + 1, updated_at = $2
		 WHERE id = $1`, id, at, d.ActorKind, reason, d.FeePaise); err != nil {
		return nil, mapErr(err)
	}
	if err := releaseBlocksTx(ctx, tx, id, at, d.ActorKind+"_cancel"); err != nil {
		return nil, err
	}
	// The event core names the accepted professional, so read it before the
	// assignment is cancelled below.
	core, err := bookingCoreTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'cancelled', release_cause = 'booking_cancelled', updated_at = $2
		WHERE booking_id = $1 AND status IN ('offered', 'accepted')`, id, at); err != nil {
		return nil, err
	}
	from := b.Status
	if err := historyTx(ctx, tx, id, &from, "cancelled", d.ActorKind, d.ActorID, &reason, at); err != nil {
		return nil, err
	}
	var refundID *uuid.UUID
	if d.RefundPaise > 0 {
		if b.PaymentID == nil {
			return nil, errors.New("store: a refund was decided for a booking with no payment")
		}
		rid, err := insertRefundTx(ctx, tx, *b.PaymentID, id, d.RefundCause, d.RefundKey, d.RefundPaise, d.ActorID, at)
		if err != nil {
			return nil, err
		}
		refundID = &rid
	}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingCancelled, core, at, events.BookingCancelledData{
		BookingCore: core, CancelledBy: d.ActorKind, Reason: reason, FeePaise: d.FeePaise, RefundPaise: d.RefundPaise}); err != nil {
		return nil, err
	}
	if audit != nil {
		if err := auditTx(ctx, tx, *audit, "booking.cancel", "booking", id.String(), map[string]any{
			"from_status": from, "fee_paise": d.FeePaise, "refund_paise": d.RefundPaise, "reason": reason}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return refundID, nil
}

func releaseBlocksTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, at time.Time, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE doorstep.pro_calendar_blocks SET active = FALSE, released_at = $2, release_reason = $3
		WHERE booking_id = $1 AND active`, bookingID, at, reason)
	return err
}

func insertRefundTx(ctx context.Context, tx pgx.Tx, paymentID, bookingID uuid.UUID, cause, key string, amount int64,
	by *uuid.UUID, at time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO doorstep.refunds (payment_id, booking_id, cause, idempotency_key, amount_paise, status, requested_by,
		                              next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'requested', $6, $7, $7, $7) RETURNING id`,
		paymentID, bookingID, cause, key, amount, by, at).Scan(&id)
	return id, mapErr(err)
}

func auditTx(ctx context.Context, tx pgx.Tx, a Actor, action, entity, entityID string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO doorstep.admin_audit_log (actor_user_id, permission, action, entity, entity_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)`, a.UserID, a.Permission, action, entity, entityID, raw)
	return err
}

// RescheduleMove is the new slot and its ranked candidates (the current
// professional first when still free).
type RescheduleMove struct {
	SlotStart  time.Time
	SlotEnd    time.Time
	BlockEnd   time.Time
	Candidates []uuid.UUID
}

// RescheduleBooking moves a booking in one transaction: the old blocks are
// released and the first candidate whose calendar admits the new slot gets a
// booking block (the exclusion constraint decides); ErrSlotTaken rolls
// everything back, the old block included. A different professional on an
// assigned booking sends it back to confirmed (A4 offers it again). decide
// re-checks the rule on the locked row.
func (s *Store) RescheduleBooking(ctx context.Context, id, customer uuid.UUID, mv RescheduleMove, at time.Time,
	decide func(*LockedBooking) error) (uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := lockBookingTx(ctx, tx, id, &customer)
	if err != nil {
		return uuid.Nil, err
	}
	if err := decide(b); err != nil {
		return uuid.Nil, err
	}
	if err := releaseBlocksTx(ctx, tx, id, at, "rescheduled"); err != nil {
		return uuid.Nil, err
	}
	pro, err := holdFirstTx(ctx, tx, id, mv.Candidates, "booking", mv.SlotStart, mv.BlockEnd, nil)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET slot_start = $2, slot_end = $3, reschedule_count = reschedule_count + 1,
		reserved_pro_id = $4, version = version + 1, updated_at = $5 WHERE id = $1`, id, mv.SlotStart, mv.SlotEnd, pro, at); err != nil {
		return uuid.Nil, mapErr(err)
	}
	// A different professional now holds the slot (A4): the previous one's
	// offer is withdrawn or their job taken back, an assigned booking goes
	// back to confirmed (dispatch offers it again after commit), and the
	// previous professional is named in doorstep.booking.reassigned (or
	// told their offer was withdrawn) so they hear of it.
	var previous *AssignmentRef
	if b.ReservedProID == nil || *b.ReservedProID != pro {
		if previous, err = liveAssignmentTx(ctx, tx, id); err != nil {
			return uuid.Nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'released', release_cause = 'rescheduled', updated_at = $2
			WHERE booking_id = $1 AND status IN ('offered', 'accepted')`, id, at); err != nil {
			return uuid.Nil, err
		}
		if previous != nil && previous.Status == "offered" {
			if err := s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, id, b.Customer, previous.ProUserID, at, events.ProOfferClosedData{
				OfferID: previous.ID, BookingID: id, ProUserID: previous.ProUserID, CustomerUserID: b.Customer,
				Outcome: "withdrawn"}); err != nil {
				return uuid.Nil, err
			}
		}
		if b.Status == "assigned" {
			if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'confirmed', assigned_at = NULL WHERE id = $1`, id); err != nil {
				return uuid.Nil, err
			}
			from, reason := "assigned", "rescheduled to a slot the assigned professional cannot take"
			if err := historyTx(ctx, tx, id, &from, "confirmed", "customer", &customer, &reason, at); err != nil {
				return uuid.Nil, err
			}
		}
	}
	core, err := bookingCoreTx(ctx, tx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingRescheduled, core, at, events.BookingRescheduledData{
		BookingCore: core, PreviousSlotStart: b.SlotStart.UTC()}); err != nil {
		return uuid.Nil, err
	}
	if previous != nil && previous.Status == "accepted" {
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingReassigned, core, at, events.BookingReassignedData{
			BookingCore: core, PreviousProUserID: previous.ProUserID, Cause: events.CauseRescheduled}); err != nil {
			return uuid.Nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, mapErr(err)
	}
	return pro, nil
}

// ExpireHolds expires pending bookings whose hold lapsed: status expired,
// the hold released, history and doorstep.booking.expired, one transaction
// per batch (FOR UPDATE SKIP LOCKED, so replicas and a payment event never
// fight over a row). A payment captured later is a late capture. It also
// releases any lapsed hold that no pending booking owns. Returns how many
// bookings expired.
func (s *Store) ExpireHolds(ctx context.Context, limit int) (int, error) {
	at := s.clock()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id FROM doorstep.bookings
		WHERE status = 'pending_payment' AND hold_expires_at <= $1
		ORDER BY hold_expires_at LIMIT $2 FOR UPDATE SKIP LOCKED`, at, limit)
	if err != nil {
		return 0, err
	}
	ids, err := collect(rows, func(r pgx.Rows) (uuid.UUID, error) {
		var id uuid.UUID
		return id, r.Scan(&id)
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'expired', version = version + 1, updated_at = $2 WHERE id = $1`, id, at); err != nil {
			return 0, err
		}
		if err := releaseBlocksTx(ctx, tx, id, at, "hold_expired"); err != nil {
			return 0, err
		}
		from, reason := "pending_payment", "the hold lapsed before payment"
		if err := historyTx(ctx, tx, id, &from, "expired", "system", nil, &reason, at); err != nil {
			return 0, err
		}
		core, err := bookingCoreTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingExpired, core, at, core); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE doorstep.pro_calendar_blocks k SET active = FALSE, released_at = $1, release_reason = 'hold_expired'
		 WHERE k.kind = 'hold' AND k.active AND k.expires_at <= $1
		   AND NOT EXISTS (SELECT 1 FROM doorstep.bookings b WHERE b.id = k.booking_id AND b.status = 'pending_payment')`, at); err != nil {
		return 0, err
	}
	return len(ids), mapErr(tx.Commit(ctx))
}
