package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// pro_unavailable and the customer's change of professional (B1, 4 Oct
// 2026). A booking whose professional is gone (declined, the offer lapsed,
// gave the job back, not on duty, no-show, ops) is never reassigned
// silently: it goes to pro_unavailable with its payment, its calendar block
// released, and a 30-minute choice deadline. The customer picks another
// professional and a time — the price difference is refunded at once or,
// when dearer, held and charged as a pro_change extras bill (payments
// reference doorstep_extras, key doorstep:extras:{bill}) — or cancels for a
// full refund; no choice by the deadline cancels with a full refund.

// Change sentinels.
var (
	// ErrChoiceClosed: the choice deadline passed (or the booking is not
	// pro_unavailable any more).
	ErrChoiceClosed = errors.New("store: the choice window is closed")
	// ErrExcluded: the professional let this booking go or ops excluded
	// them.
	ErrExcluded = errors.New("store: the professional is excluded from this booking")
)

// ---------------------------------------------------------------- pro_unavailable

// Unavailable is one booking losing its professional.
type Unavailable struct {
	BookingID uuid.UUID
	From      []string
	Version   int
	Close     *CloseAssignment
	Cause     string // events.Unavailable*
	ActorKind string
	ActorID   *uuid.UUID
	Reason    string
	Exclude   []uuid.UUID
	Deadline  time.Time
	Audit     *Actor
	At        time.Time
}

// UnavailableResult is what the transition did.
type UnavailableResult struct {
	ClosedOfferID     *uuid.UUID
	ClosedProUserID   *uuid.UUID
	ClosedOutcome     string
	PreviousProUserID *uuid.UUID
	// RemovedAccepted: the professional had accepted the job (they are told
	// it is no longer theirs).
	RemovedAccepted bool
	Customer        uuid.UUID
	FromStatus      string
	SlotStart       time.Time
	SlotEnd         time.Time
}

// MakeProUnavailable moves a booking to pro_unavailable in one transaction:
// the closing assignment (its penalty and counters), every other live
// assignment released, every calendar block released, the professional
// excluded from this booking (with ops' exclusions), the choice deadline,
// history, doorstep.pro.offer_closed (when an offer closed) and
// doorstep.booking.pro_unavailable, and the audit row for ops. ErrStale when
// the booking or the assignment moved on.
func (s *Store) MakeProUnavailable(ctx context.Context, in Unavailable) (*UnavailableResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	at := in.At
	var status string
	var version int
	var customer uuid.UUID
	var slotStart, slotEnd time.Time
	if err := tx.QueryRow(ctx, `SELECT status, version, customer_user_id, slot_start, slot_end FROM doorstep.bookings
		WHERE id = $1 FOR UPDATE`, in.BookingID).Scan(&status, &version, &customer, &slotStart, &slotEnd); err != nil {
		return nil, mapErr(err)
	}
	if !contains(in.From, status) || (in.Version > 0 && version != in.Version) {
		return nil, ErrStale
	}
	res := &UnavailableResult{Customer: customer, FromStatus: status, SlotStart: slotStart.UTC(), SlotEnd: slotEnd.UTC()}
	exclude := append([]uuid.UUID(nil), in.Exclude...)
	if in.Close != nil {
		var aStatus string
		var proUser uuid.UUID
		err := tx.QueryRow(ctx, `SELECT a.status, p.user_id FROM doorstep.booking_assignments a
			JOIN doorstep.professionals p ON p.id = a.pro_id
			WHERE a.id = $1 AND a.booking_id = $2 AND a.pro_id = $3 FOR UPDATE OF a`, in.Close.ID, in.BookingID, in.Close.ProID).
			Scan(&aStatus, &proUser)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !contains(in.Close.From, aStatus)) {
			return nil, ErrStale
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = $2, responded_at = COALESCE(responded_at, $3),
			decline_reason = COALESCE($4, decline_reason), release_cause = NULLIF($5, ''), updated_at = $3 WHERE id = $1`,
			in.Close.ID, in.Close.To, at, in.Close.DeclineReason, in.Close.Cause); err != nil {
			return nil, err
		}
		if err := penaltyTx(ctx, tx, in.Close, in.BookingID, at); err != nil {
			return nil, err
		}
		id, u := in.Close.ID, proUser
		res.ClosedOfferID, res.ClosedProUserID, res.PreviousProUserID = &id, &u, &u
		res.RemovedAccepted = aStatus == "accepted"
		exclude = append(exclude, in.Close.ProID)
		if aStatus == "offered" {
			res.ClosedOutcome = offerOutcome(in.Close.To)
			if err := s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, in.BookingID, customer, proUser, at,
				events.ProOfferClosedData{OfferID: id, BookingID: in.BookingID, ProUserID: proUser, CustomerUserID: customer,
					Outcome: res.ClosedOutcome}); err != nil {
				return nil, err
			}
		}
	}
	// Any other live assignment goes too (none in the normal flow).
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'released', release_cause = $2, updated_at = $3
		WHERE booking_id = $1 AND status IN ('offered', 'accepted')`, in.BookingID, in.Cause, at); err != nil {
		return nil, err
	}
	if err := releaseBlocksTx(ctx, tx, in.BookingID, at, "pro_unavailable"); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE doorstep.bookings SET status = 'pro_unavailable', reserved_pro_id = NULL, assigned_at = NULL, en_route_at = NULL,
		       arrived_at = NULL, pro_unavailable_at = $2, choice_deadline = $3, unavailable_cause = $4,
		       excluded_pro_ids = ARRAY(SELECT DISTINCT x FROM unnest(excluded_pro_ids || $5::uuid[]) x WHERE x IS NOT NULL),
		       rescue_until = NULL, rescue_cause = NULL, dispatch_exhausted_at = NULL, pro_late_at = NULL,
		       version = version + 1, updated_at = $2
		 WHERE id = $1`, in.BookingID, at, in.Deadline, in.Cause, exclude); err != nil {
		return nil, mapErr(err)
	}
	reason := in.Reason
	if err := historyTx(ctx, tx, in.BookingID, &status, "pro_unavailable", in.ActorKind, in.ActorID, &reason, at); err != nil {
		return nil, err
	}
	core, err := bookingCoreTx(ctx, tx, in.BookingID)
	if err != nil {
		return nil, err
	}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingProUnavailable, core, at, events.BookingProUnavailableData{
		BookingCore: core, PreviousProUserID: res.PreviousProUserID, Cause: in.Cause, ChoiceDeadline: in.Deadline.UTC()}); err != nil {
		return nil, err
	}
	if in.Audit != nil {
		if err := auditTx(ctx, tx, *in.Audit, "booking.redispatch", "booking", in.BookingID.String(), map[string]any{
			"from_status": status, "reason": in.Reason, "outcome": "pro_unavailable", "excluded": exclude}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return res, nil
}

// ChoiceTimeouts lists pro_unavailable bookings whose choice deadline
// passed with no change of professional still being paid for (a live
// pending change keeps the booking until its hold lapses).
func (s *Store) ChoiceTimeouts(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT b.id FROM doorstep.bookings b
		WHERE b.status = 'pro_unavailable' AND b.choice_deadline <= $1
		  AND NOT EXISTS (SELECT 1 FROM doorstep.booking_pro_changes c WHERE c.booking_id = b.id AND c.status = 'pending_payment'
		                   AND c.hold_expires_at > $1)
		ORDER BY b.choice_deadline LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}

// ---------------------------------------------------------------- changes

// ChangeInput is the customer's pick for a pro_unavailable booking, priced.
type ChangeInput struct {
	ID             uuid.UUID
	BookingID      uuid.UUID
	Customer       uuid.UUID
	IdempotencyKey string
	ToPro          uuid.UUID
	Asap           bool
	SlotStart      time.Time
	SlotEnd        time.Time
	BlockStart     time.Time
	BlockEnd       time.Time
	Duration       int
	Items          []model.QuoteLine
	TotalPaise     int64
	TaxablePaise   int64
	TaxPaise       int64
	// A dearer professional: held until HoldExpiresAt while the difference
	// bill (BillID, PaymentID, IntentKey) is paid.
	HoldExpiresAt time.Time
	BillID        uuid.UUID
	PaymentID     uuid.UUID
	IntentKey     string
	// A cheaper one: the difference is refunded under this cause and key.
	RefundCause string
	RefundKey   string
	At          time.Time
}

// ChangeResult is what a change did.
type ChangeResult struct {
	ChangeID uuid.UUID
	// Replay: the idempotency key was used before; nothing changed now.
	Replay    bool
	Applied   bool
	RefundIDs []uuid.UUID
	// PaymentID is the difference bill's payment row (pending_payment).
	PaymentID *uuid.UUID
	ToProUser uuid.UUID
}

type changeRow struct {
	id, booking, customer, toPro         uuid.UUID
	fromPro                              *uuid.UUID
	asap                                 bool
	slotStart, slotEnd, blockStart       time.Time
	blockEnd                             time.Time
	duration                             int
	prevTotal, total, taxable, tax, diff int64
	items                                []model.QuoteLine
	status                               string
	bill                                 *uuid.UUID
	holdUntil                            *time.Time
	key                                  string
}

const changeCols = `id, booking_id, customer_user_id, to_pro_id, from_pro_id, asap, slot_start, slot_end, block_start, block_end,
	duration_minutes, previous_total_paise, new_total_paise, new_taxable_paise, new_tax_paise, difference_paise, items, status,
	extras_bill_id, hold_expires_at, idempotency_key`

func scanChange(r pgx.Row) (*changeRow, error) {
	var c changeRow
	var raw []byte
	err := r.Scan(&c.id, &c.booking, &c.customer, &c.toPro, &c.fromPro, &c.asap, &c.slotStart, &c.slotEnd, &c.blockStart, &c.blockEnd,
		&c.duration, &c.prevTotal, &c.total, &c.taxable, &c.tax, &c.diff, &raw, &c.status, &c.bill, &c.holdUntil, &c.key)
	if err != nil {
		return nil, mapErr(err)
	}
	if err := json.Unmarshal(raw, &c.items); err != nil {
		return nil, fmt.Errorf("pro change %s items: %w", c.id, err)
	}
	return &c, nil
}

// ChangeProfessional applies the customer's pick in one transaction. decide
// sees the locked booking (status, deadline) and refuses with its own error.
// The professional must not be excluded (ErrExcluded). A previous pending
// change is abandoned first. Cheaper or the same price: the professional
// gets a booking block, the booking takes the new lines, slot and total, is
// confirmed again, and the difference is refunded (spread over the
// payments). Dearer: the professional is held (hold block), the difference
// bill and its payment row are opened and the change waits for the signed
// payment event (the booking stays pro_unavailable). The idempotency key
// replays the same change. ErrSlotTaken when the calendar refuses.
func (s *Store) ChangeProfessional(ctx context.Context, in ChangeInput, decide func(*LockedBooking) error) (*ChangeResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := lockBookingTx(ctx, tx, in.BookingID, &in.Customer)
	if err != nil {
		return nil, err
	}
	if prev, err := scanChange(tx.QueryRow(ctx, `SELECT `+changeCols+` FROM doorstep.booking_pro_changes
		WHERE booking_id = $1 AND idempotency_key = $2`, in.BookingID, in.IdempotencyKey)); err == nil {
		res := &ChangeResult{ChangeID: prev.id, Replay: true, Applied: prev.status == "applied"}
		return res, mapErr(tx.Commit(ctx))
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err := decide(b); err != nil {
		return nil, err
	}
	for _, x := range b.ExcludedProIDs {
		if x == in.ToPro {
			return nil, ErrExcluded
		}
	}
	if err := abandonChangesTx(ctx, tx, in.BookingID, in.At); err != nil {
		return nil, err
	}
	var toUser uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT user_id FROM doorstep.professionals WHERE id = $1`, in.ToPro).Scan(&toUser); err != nil {
		return nil, mapErr(err)
	}
	var fromPro *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT pro_id FROM doorstep.booking_assignments WHERE booking_id = $1 ORDER BY created_at DESC LIMIT 1`,
		in.BookingID).Scan(&fromPro); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	items, err := json.Marshal(in.Items)
	if err != nil {
		return nil, err
	}
	diff := in.TotalPaise - b.TotalPaise
	res := &ChangeResult{ChangeID: in.ID, ToProUser: toUser}
	c := &changeRow{id: in.ID, booking: in.BookingID, customer: in.Customer, toPro: in.ToPro, fromPro: fromPro, asap: in.Asap,
		slotStart: in.SlotStart, slotEnd: in.SlotEnd, blockStart: in.BlockStart, blockEnd: in.BlockEnd, duration: in.Duration,
		prevTotal: b.TotalPaise, total: in.TotalPaise, taxable: in.TaxablePaise, tax: in.TaxPaise, diff: diff, items: in.Items,
		key: in.IdempotencyKey}
	insert := func(status string, bill *uuid.UUID, hold *time.Time) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO doorstep.booking_pro_changes (id, booking_id, customer_user_id, idempotency_key, from_pro_id, to_pro_id, asap,
			    slot_start, slot_end, block_start, block_end, duration_minutes, previous_total_paise, new_total_paise, new_taxable_paise,
			    new_tax_paise, difference_paise, items, status, extras_bill_id, hold_expires_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $22)`,
			in.ID, in.BookingID, in.Customer, in.IdempotencyKey, fromPro, in.ToPro, in.Asap, in.SlotStart, in.SlotEnd, in.BlockStart,
			in.BlockEnd, in.Duration, b.TotalPaise, in.TotalPaise, in.TaxablePaise, in.TaxPaise, diff, items, status, bill, hold, in.At)
		return mapErr(err)
	}

	if diff > 0 {
		if _, err := holdFirstTx(ctx, tx, in.BookingID, []uuid.UUID{in.ToPro}, "hold", in.BlockStart, in.BlockEnd, &in.HoldExpiresAt); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO doorstep.extras_bills (id, booking_id, amount_paise, kind, status, due_at, created_at, updated_at)
			VALUES ($1, $2, $3, 'pro_change', 'payment_pending', $4, $5, $5)`, in.BillID, in.BookingID, diff, in.HoldExpiresAt, in.At); err != nil {
			return nil, mapErr(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO doorstep.payments (id, booking_id, extras_bill_id, reference_type, reference_id, intent_key, amount_paise, status,
			                               created_at, updated_at)
			VALUES ($1, $2, $3, 'doorstep_extras', $3, $4, $5, 'created', $6, $6)`,
			in.PaymentID, in.BookingID, in.BillID, in.IntentKey, diff, in.At); err != nil {
			return nil, mapErr(err)
		}
		bill, hold := in.BillID, in.HoldExpiresAt
		if err := insert("pending_payment", &bill, &hold); err != nil {
			return nil, err
		}
		pid := in.PaymentID
		res.PaymentID = &pid
		return res, mapErr(tx.Commit(ctx))
	}

	if _, err := holdFirstTx(ctx, tx, in.BookingID, []uuid.UUID{in.ToPro}, "booking", in.BlockStart, in.BlockEnd, nil); err != nil {
		return nil, err
	}
	if err := insert("applied", nil, nil); err != nil {
		return nil, err
	}
	if err := s.commitChangeTx(ctx, tx, c, b.Status, "customer", &in.Customer, toUser, in.At); err != nil {
		return nil, err
	}
	if diff < 0 {
		if res.RefundIDs, err = refundAcrossTx(ctx, tx, in.BookingID, -diff, in.RefundCause, in.RefundKey, &in.Customer, in.At); err != nil {
			return nil, err
		}
	}
	res.Applied = true
	return res, mapErr(tx.Commit(ctx))
}

// commitChangeTx makes the change the booking's: the new professional
// reserved (their block is already in place), the new slot, lines and
// total, confirmed again, the choice cleared; the change row applied;
// history and doorstep.booking.pro_changed. Lines are written after the
// professional is reserved (the database checks each line's price is an
// approved row of the reserved professional).
func (s *Store) commitChangeTx(ctx context.Context, tx pgx.Tx, c *changeRow, from, actorKind string, actorID *uuid.UUID,
	toUser uuid.UUID, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE doorstep.bookings SET status = 'confirmed', reserved_pro_id = $2, slot_start = $3, slot_end = $4, duration_minutes = $5,
		       asap = $6, total_paise = $7, taxable_paise = $8, tax_paise = $9, choice_deadline = NULL, unavailable_cause = NULL,
		       pro_unavailable_at = NULL, hold_expires_at = NULL, dispatch_attempted_at = NULL, dispatch_exhausted_at = NULL,
		       unassigned_alerted_at = NULL, pro_late_at = NULL, pro_change_count = pro_change_count + 1,
		       version = version + 1, updated_at = $10
		 WHERE id = $1`, c.booking, c.toPro, c.slotStart, c.slotEnd, c.duration, c.asap, c.total, c.taxable, c.tax, at); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM doorstep.booking_items WHERE booking_id = $1`, c.booking); err != nil {
		return err
	}
	if err := insertItemsTx(ctx, tx, c.booking, c.items); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_pro_changes SET status = 'applied', applied_at = $2, updated_at = $2
		WHERE id = $1`, c.id, at); err != nil {
		return err
	}
	reason := "the customer picked another professional"
	if err := historyTx(ctx, tx, c.booking, &from, "confirmed", actorKind, actorID, &reason, at); err != nil {
		return err
	}
	core, err := bookingCoreTx(ctx, tx, c.booking)
	if err != nil {
		return err
	}
	var prevUser *uuid.UUID
	if c.fromPro != nil {
		var u uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id FROM doorstep.professionals WHERE id = $1`, *c.fromPro).Scan(&u); err == nil {
			prevUser = &u
		}
	}
	return s.enqueueBookingEvent(ctx, tx, events.BookingProChanged, core, at, events.BookingProChangedData{
		BookingCore: core, ChangeID: c.id, PreviousProUserID: prevUser, NewProUserID: toUser, DifferencePaise: c.diff, Asap: c.asap})
}

// abandonChangesTx abandons a booking's pending change of professional: its
// hold released, its bill cancelled (a capture arriving later is refunded).
func abandonChangesTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, at time.Time) error {
	rows, err := tx.Query(ctx, `UPDATE doorstep.booking_pro_changes SET status = 'abandoned', abandoned_at = $2, updated_at = $2
		WHERE booking_id = $1 AND status = 'pending_payment' RETURNING to_pro_id, extras_bill_id`, bookingID, at)
	if err != nil {
		return err
	}
	type ab struct {
		pro  uuid.UUID
		bill *uuid.UUID
	}
	list, err := collect(rows, func(r pgx.Rows) (ab, error) {
		var v ab
		return v, r.Scan(&v.pro, &v.bill)
	})
	if err != nil {
		return err
	}
	for _, a := range list {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_calendar_blocks SET active = FALSE, released_at = $3, release_reason = 'change_abandoned'
			WHERE booking_id = $1 AND pro_id = $2 AND kind = 'hold' AND active`, bookingID, a.pro, at); err != nil {
			return err
		}
		if a.bill != nil {
			if _, err := tx.Exec(ctx, `UPDATE doorstep.extras_bills SET status = 'cancelled', updated_at = $2
				WHERE id = $1 AND status IN ('open', 'payment_pending')`, *a.bill, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// AbandonLapsedChanges abandons pending changes whose hold lapsed before the
// difference was paid (the customer may pick again until the deadline).
// Returns the bookings touched.
func (s *Store) AbandonLapsedChanges(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT b.id FROM doorstep.bookings b
		WHERE EXISTS (SELECT 1 FROM doorstep.booking_pro_changes c WHERE c.booking_id = b.id AND c.status = 'pending_payment'
		              AND c.hold_expires_at <= $1)
		ORDER BY b.id LIMIT $2 FOR UPDATE OF b SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	ids, err := collectIDs(rows)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := abandonChangesTx(ctx, tx, id, now); err != nil {
			return nil, err
		}
	}
	return ids, mapErr(tx.Commit(ctx))
}

// ChangeView is a change of professional with its difference payment row
// (if any) and the idempotency key it was made with.
type ChangeView struct {
	Change    model.ProChange
	PaymentID *uuid.UUID
	Key       string
}

// ProChanges lists a booking's changes of professional, oldest first, with
// the professional's first name (the payment intent is the service's).
func (s *Store) ProChanges(ctx context.Context, bookingID uuid.UUID) ([]ChangeView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.status, c.to_pro_id, p.display_name, c.asap, c.slot_start, c.slot_end, c.previous_total_paise, c.new_total_paise,
		       c.difference_paise, c.hold_expires_at, c.created_at, pay.id, c.idempotency_key
		FROM doorstep.booking_pro_changes c
		JOIN doorstep.professionals p ON p.id = c.to_pro_id
		LEFT JOIN doorstep.payments pay ON pay.extras_bill_id = c.extras_bill_id
		WHERE c.booking_id = $1 ORDER BY c.created_at, c.id`, bookingID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (ChangeView, error) {
		var cv ChangeView
		v := &cv.Change
		var name string
		err := r.Scan(&v.ID, &v.Status, &v.ProID, &name, &v.Asap, &v.SlotStart, &v.SlotEnd, &v.PreviousTotalPaise, &v.NewTotalPaise,
			&v.DifferencePaise, &v.HoldExpiresAt, &v.CreatedAt, &cv.PaymentID, &cv.Key)
		v.ProFirstName = firstName(name)
		v.SlotStart, v.SlotEnd, v.CreatedAt = v.SlotStart.UTC(), v.SlotEnd.UTC(), v.CreatedAt.UTC()
		utcPtr(&v.HoldExpiresAt)
		if v.Status == "applied" && v.DifferencePaise < 0 {
			v.RefundPaise = -v.DifferencePaise
		}
		return cv, err
	})
}

// PaymentByID is one payment row.
func (s *Store) PaymentByID(ctx context.Context, id uuid.UUID) (*PaymentRow, error) {
	p, err := scanPayment(s.db.QueryRow(ctx, `SELECT `+paymentCols+` FROM doorstep.payments WHERE id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// ---------------------------------------------------------------- refunds over payments

// refundAcrossTx writes the refund of amount for a booking spread over its
// payments: the booking payment first, then each paid change-of-professional
// difference, each up to what that payment still has (captured minus every
// refund that has not failed). The first row carries key; a further row
// key:<payment>. Returns the rows to submit.
func refundAcrossTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, amount int64, cause, key string, by *uuid.UUID,
	at time.Time) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.id, p.amount_paise - COALESCE((SELECT sum(r.amount_paise) FROM doorstep.refunds r
		                                         WHERE r.payment_id = p.id AND r.status <> 'failed'), 0)
		FROM doorstep.payments p
		LEFT JOIN doorstep.extras_bills eb ON eb.id = p.extras_bill_id
		WHERE p.booking_id = $1 AND p.status IN ('succeeded', 'partially_refunded', 'refunded')
		  AND (p.reference_type = 'doorstep_booking' OR eb.kind = 'pro_change')
		ORDER BY (p.reference_type = 'doorstep_booking') DESC, p.created_at, p.id`, bookingID)
	if err != nil {
		return nil, err
	}
	type pay struct {
		id   uuid.UUID
		left int64
	}
	pays, err := collect(rows, func(r pgx.Rows) (pay, error) {
		var v pay
		return v, r.Scan(&v.id, &v.left)
	})
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, p := range pays {
		if amount <= 0 {
			break
		}
		n := min(amount, p.left)
		if n <= 0 {
			continue
		}
		k := key
		if len(out) > 0 {
			k = key + ":" + shortHash(p.id.String())
		}
		rid, err := insertRefundTx(ctx, tx, p.id, bookingID, cause, k, n, by, at)
		if err != nil {
			return nil, err
		}
		out = append(out, rid)
		amount -= n
	}
	if amount > 0 {
		return nil, ErrExceedsPaid
	}
	return out, nil
}
