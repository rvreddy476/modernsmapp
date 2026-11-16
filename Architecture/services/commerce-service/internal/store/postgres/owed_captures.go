package postgres

// Owed captures: every payment the buyer made that the order did not keep
// (migration 055, order_owed_captures).
//
// A payment the order cannot keep is never booked against the order and is
// never silently dropped. It is recorded here, in the transaction that
// decides it, with how it is refunded, and the buyer's payment_notice is read
// from here (payment_notice.go). The cases, and who returns the money:
//
//	reason                     refund_via    who refunds it
//	duplicate_order_capture    payments      payments-service, on
//	                                         commerce.order.duplicate_capture
//	duplicate_capture          payments      payments-service on its own (a
//	                                         second provider payment on one
//	                                         intent; payment.owed_capture)
//	late_capture_after_failed  payments      payments-service on its own (a
//	                                         capture on an intent it had
//	                                         finalised failed)
//	capture_mismatch           payments      payments-service on its own (a
//	                                         capture whose money did not match
//	                                         its intent)
//	late_capture               order         commerce's refund command, in the
//	                                         order's own ledger (p0.go)
//	capture_mismatch           owed_command  commerce's refund command,
//	                                         scope owed_capture (a capture
//	                                         payments accepted but whose
//	                                         amount, currency or payer the
//	                                         order refused)
//
// Refund progress comes back on the path that refunds it: payment.owed_capture
// for payments' own ledger (ApplyOwedCapture), payment.refunded for the two
// command paths (SettleRefund), payment.refund_failed or a refused command for
// a refund that stalled (ApplyRefundFailed, MarkRefundFailed).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Owed-capture reasons (order_owed_captures.reason). The payments-side ones
// are payments-service's refund_required reasons, kept verbatim.
const (
	OwedReasonDuplicateOrderCapture  = "duplicate_order_capture"
	OwedReasonRepeatCapture          = "duplicate_capture"
	OwedReasonLateCapture            = "late_capture"
	OwedReasonLateCaptureAfterFailed = "late_capture_after_failed"
	OwedReasonCaptureMismatch        = "capture_mismatch"
)

// How an owed payment is refunded (order_owed_captures.refund_via).
const (
	refundViaPayments    = "payments"
	refundViaOrder       = "order"
	refundViaOwedCommand = "owed_command"
)

// Refund progress of an owed payment, as the buyer is told it.
const (
	OwedRefundPending  = "pending"
	OwedRefundRefunded = "refunded"
	OwedRefundFailed   = "failed"
)

// Refund command scopes (order_refund_commands.scope).
const (
	refundScopeOrder       = "order"
	refundScopeOwedCapture = "owed_capture"
)

// EventPaymentOwedCapture is payments-service's payment.owed_capture: a
// payment it holds as owed back, and how far the refund has got.
const EventPaymentOwedCapture = "payment.owed_capture"

// owedCapture is one order_owed_captures row to record.
type owedCapture struct {
	OrderID        uuid.UUID
	Key            string
	IntentID       string
	Reason         string
	AmountMinor    int64 // money the buyer paid on this payment
	AmountExact    bool
	Currency       string
	RefundVia      string
	RefundDueMinor int64 // in the refund path's unit
	Detail         string
	DetectedAt     time.Time // zero = now
}

// normIntent is how an intent id is compared and stored: trimmed, lower case,
// as decideCapture compares it.
func normIntent(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// owedKeyForIntent is the key of a whole intent owed back.
func owedKeyForIntent(intent string) string { return "intent:" + normIntent(intent) }

// owedKeyForCapture is the key of a capture commerce holds as owed: its
// intent, or the order when the event stated none (one late capture per
// order then, as lateCaptureKey has always keyed it).
func owedKeyForCapture(orderID uuid.UUID, intent string) string {
	if normIntent(intent) == "" {
		return "late-capture:" + orderID.String()
	}
	return owedKeyForIntent(intent)
}

// owedKeyForPaymentsRow is the key of a payment payments-service recorded as
// owed. A duplicate order capture is the whole duplicate intent, the row
// commerce already wrote when it reported it; anything else is keyed by
// payments' own row.
func owedKeyForPaymentsRow(reason, intent string, rowID int64) string {
	if reason == OwedReasonDuplicateOrderCapture && normIntent(intent) != "" {
		return owedKeyForIntent(intent)
	}
	return "refund_required:" + strconv.FormatInt(rowID, 10)
}

// capturedMoney is what the buyer actually paid on a capture, and whether it
// is confirmed money.
//
//   - A bank-offer capture carries captured_minor (and the offer figures): the
//     money, below amount_minor. This is the case the notice used to get wrong.
//   - A capture with neither is a plain capture, and its amount_minor IS the
//     money: payments-service refuses (B2) any capture whose money differs
//     from the intent unless an offer explains it, and publishes the plain
//     payload byte for byte as before offers existed (its no-offer golden), so
//     no captured_minor is added there.
//   - Anything else (captured_minor zero or above the order value, offer
//     figures that do not reconcile) is not trusted: the stated amount stands
//     in, marked inexact, never presented as confirmed money.
//
// It has no I/O.
func capturedMoney(e PaymentEvent) (int64, bool) {
	stated := e.AmountMinor.Int64()
	if e.CapturedMinor != nil {
		if c := e.CapturedMinor.Int64(); c > 0 && (stated <= 0 || c <= stated) {
			return c, true
		}
	}
	if e.Offer != nil && e.Offer.consistentWith(e.AmountMinor) {
		return e.Offer.CapturedMinor.Int64(), true
	}
	if stated < 0 {
		return 0, false
	}
	return stated, e.CapturedMinor == nil && e.Offer == nil
}

// recordOwedCaptureTx writes an owed payment once (by key). It reports whether
// the row is new.
func recordOwedCaptureTx(ctx context.Context, tx pgx.Tx, w owedCapture) (bool, error) {
	if w.AmountMinor < 0 || w.RefundDueMinor < 0 {
		return false, fmt.Errorf("owed capture %s: negative amount", w.Key)
	}
	detected := w.DetectedAt
	if detected.IsZero() {
		detected = time.Now()
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO order_owed_captures
		     (order_id, owed_key, intent_id, reason, amount_minor, amount_exact, currency,
		      refund_via, refund_due_minor, detail, detected_at)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11)
		 ON CONFLICT (owed_key) DO NOTHING`,
		w.OrderID, w.Key, normIntent(w.IntentID), w.Reason, w.AmountMinor, w.AmountExact,
		coalesceCurrency(w.Currency), w.RefundVia, w.RefundDueMinor, boundDetail(w.Detail), detected)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func boundDetail(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 300 {
		return string(r[:300])
	}
	return s
}

// ─── payment.owed_capture ────────────────────────────────────────────

// OwedCaptureEvent is payment.owed_capture as received.
type OwedCaptureEvent struct {
	EventID          string
	OrderID          uuid.UUID
	IntentID         string
	RefundRequiredID int64
	Reason           string
	AmountMinor      int64 // money owed back, from payments' ledger
	Currency         string
	RefundedMinor    int64 // money returned so far
	Resolved         bool
	DetectedAt       time.Time
}

// owedState is the refund-tracking half of an order_owed_captures row.
type owedState struct {
	AmountMinor    int64
	AmountExact    bool
	RefundVia      string
	RefundDueMinor int64
	RefundedMinor  int64
	RefundStatus   string
	RefundedAt     *time.Time
	FailedAt       *time.Time
}

// mergeOwedFromPayments folds payments' account of an owed payment into the
// row commerce holds (nil when it holds none). payments-service's ledger is
// the authority on this money: its amount replaces a stated one, refunds only
// ever accumulate, and a refund that covers the payment ends any earlier
// failure. A row commerce refunds itself (order, owed_command) is not
// payments' to change and is returned as it was, with ok=false. It has no I/O.
func mergeOwedFromPayments(cur *owedState, ev OwedCaptureEvent, now time.Time) (next owedState, ok bool) {
	if cur != nil && cur.RefundVia != refundViaPayments {
		return *cur, false
	}
	next = owedState{
		AmountMinor: ev.AmountMinor, AmountExact: true, RefundVia: refundViaPayments,
		RefundDueMinor: ev.AmountMinor, RefundedMinor: ev.RefundedMinor, RefundStatus: OwedRefundPending,
	}
	if next.AmountMinor < 0 {
		next.AmountMinor, next.RefundDueMinor = 0, 0
	}
	if next.RefundedMinor < 0 {
		next.RefundedMinor = 0
	}
	if cur != nil {
		if cur.RefundedMinor > next.RefundedMinor {
			next.RefundedMinor = cur.RefundedMinor // events can arrive out of order
		}
		next.RefundedAt, next.FailedAt = cur.RefundedAt, cur.FailedAt
		if cur.RefundStatus == OwedRefundFailed {
			next.RefundStatus = OwedRefundFailed
		}
	}
	covered := ev.Resolved || (next.RefundDueMinor > 0 && next.RefundedMinor >= next.RefundDueMinor)
	switch {
	case covered:
		next.RefundStatus = OwedRefundRefunded
		if next.RefundedAt == nil {
			t := now
			next.RefundedAt = &t
		}
	case cur != nil && cur.RefundStatus == OwedRefundFailed && next.RefundedMinor > cur.RefundedMinor:
		// Money is moving again after a parked attempt: pending, not failed.
		next.RefundStatus = OwedRefundPending
	}
	return next, true
}

// ApplyOwedCapture records payment.owed_capture against its order, exactly
// once per event (payment_event_inbox).
//
// Errors: ErrDuplicatePaymentEvt for a redelivered event, ErrOrderNotFoundP0
// for an order commerce does not have (poison).
func (s *Store) ApplyOwedCapture(ctx context.Context, ev OwedCaptureEvent) error {
	if strings.TrimSpace(ev.EventID) == "" {
		return fmt.Errorf("owed-capture event has no id; refusing to process without a dedupe key")
	}
	if ev.RefundRequiredID <= 0 {
		return fmt.Errorf("%w: owed-capture event %s names no refund_required row", ErrAmountMismatch, ev.EventID)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type','system',true)`); err != nil {
		return err
	}
	pe := PaymentEvent{
		EventID: ev.EventID, EventType: EventPaymentOwedCapture, IntentID: ev.IntentID,
		OrderID: ev.OrderID, AmountMinor: money.Paise(ev.AmountMinor),
	}
	return applyPaymentOnce(ctx, tx, pe, func(ctx context.Context, tx pgx.Tx) error {
		var currency *string
		if err := tx.QueryRow(ctx, `SELECT currency_code FROM orders WHERE id = $1 FOR UPDATE`, ev.OrderID).
			Scan(&currency); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrOrderNotFoundP0
			}
			return err
		}
		key := owedKeyForPaymentsRow(ev.Reason, ev.IntentID, ev.RefundRequiredID)
		cur, err := readOwedStateTx(ctx, tx, key)
		if err != nil {
			return err
		}
		next, ok := mergeOwedFromPayments(cur, ev, time.Now())
		if !ok {
			slog.WarnContext(ctx, "commerce: payments announced an owed payment commerce refunds itself; left as recorded",
				"order_id", ev.OrderID, "intent_id", ev.IntentID, "reason", ev.Reason,
				"refund_required_id", ev.RefundRequiredID)
			return nil
		}
		cur2 := coalesceCurrency(ev.Currency)
		if strings.TrimSpace(ev.Currency) == "" && currency != nil {
			cur2 = coalesceCurrency(*currency)
		}
		detected := ev.DetectedAt
		if detected.IsZero() {
			detected = time.Now()
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_owed_captures
			     (order_id, owed_key, intent_id, payments_owed_id, reason, amount_minor, amount_exact, currency,
			      refund_via, refund_due_minor, refunded_minor, refund_status, refunded_at, failed_at, detected_at)
			 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			 ON CONFLICT (owed_key) DO UPDATE
			    SET payments_owed_id = EXCLUDED.payments_owed_id,
			        amount_minor     = EXCLUDED.amount_minor,
			        amount_exact     = EXCLUDED.amount_exact,
			        currency         = EXCLUDED.currency,
			        refund_due_minor = EXCLUDED.refund_due_minor,
			        refunded_minor   = EXCLUDED.refunded_minor,
			        refund_status    = EXCLUDED.refund_status,
			        refunded_at      = EXCLUDED.refunded_at,
			        updated_at       = NOW()`,
			ev.OrderID, key, normIntent(ev.IntentID), ev.RefundRequiredID, ev.Reason,
			next.AmountMinor, next.AmountExact, cur2, next.RefundVia, next.RefundDueMinor,
			next.RefundedMinor, next.RefundStatus, next.RefundedAt, next.FailedAt, detected); err != nil {
			return err
		}
		slog.InfoContext(ctx, "commerce: owed payment recorded from payments-service",
			"order_id", ev.OrderID, "intent_id", ev.IntentID, "reason", ev.Reason,
			"amount_minor", next.AmountMinor, "refunded_minor", next.RefundedMinor, "refund_status", next.RefundStatus)
		return nil
	})
}

func readOwedStateTx(ctx context.Context, tx pgx.Tx, key string) (*owedState, error) {
	var st owedState
	err := tx.QueryRow(ctx,
		`SELECT amount_minor, amount_exact, refund_via, refund_due_minor, refunded_minor, refund_status,
		        refunded_at, failed_at
		   FROM order_owed_captures WHERE owed_key = $1 FOR UPDATE`, key).
		Scan(&st.AmountMinor, &st.AmountExact, &st.RefundVia, &st.RefundDueMinor, &st.RefundedMinor,
			&st.RefundStatus, &st.RefundedAt, &st.FailedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// ─── payment.refund_failed ───────────────────────────────────────────

// RefundFailedEvent is payment.refund_failed as received: payments-service
// parked a refund it could not make, and a person must act.
type RefundFailedEvent struct {
	EventID       string
	ReferenceType string // "order" (a refund command) or "refund_required" (an owed payment)
	ReferenceID   string
	IntentID      string
	AmountMinor   int64
	ReasonCode    string
}

// ApplyRefundFailed records that a refund stalled, so the buyer is told it
// is delayed rather than that it is on its way. It changes no money and no
// command's own state: commerce's refund worker still owns its commands, and
// a refund that later lands settles as usual and clears the delay.
//
// An event for something commerce does not hold (another domain's refund, or
// an owed payment whose record has not reached commerce yet) is acknowledged
// and changes nothing.
func (s *Store) ApplyRefundFailed(ctx context.Context, ev RefundFailedEvent) error {
	if strings.TrimSpace(ev.EventID) == "" {
		return fmt.Errorf("refund-failed event has no id; refusing to process without a dedupe key")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type','system',true)`); err != nil {
		return err
	}

	var (
		orderID uuid.UUID
		rowID   int64
	)
	switch ev.ReferenceType {
	case "order":
		id, err := uuid.Parse(strings.TrimSpace(ev.ReferenceID))
		if err != nil {
			return nil // not an order reference commerce can act on
		}
		orderID = id
	case "refund_required":
		id, err := strconv.ParseInt(strings.TrimSpace(ev.ReferenceID), 10, 64)
		if err != nil || id <= 0 {
			return nil
		}
		rowID = id
		err = tx.QueryRow(ctx,
			`SELECT order_id FROM order_owed_captures WHERE payments_owed_id = $1 ORDER BY detected_at LIMIT 1`, id).
			Scan(&orderID)
		if errors.Is(err, pgx.ErrNoRows) {
			slog.InfoContext(ctx, "commerce: payment.refund_failed for an owed payment commerce holds no record of; ignored",
				"refund_required_id", id, "intent_id", ev.IntentID)
			return nil
		}
		if err != nil {
			return err
		}
	default:
		return nil
	}

	code := boundDetail(ev.ReasonCode)
	if code == "" {
		code = "refund_failed"
	}
	pe := PaymentEvent{
		EventID: ev.EventID, EventType: paymentevents.TypeRefundFailed, IntentID: ev.IntentID,
		OrderID: orderID, AmountMinor: money.Paise(ev.AmountMinor),
	}
	return applyPaymentOnce(ctx, tx, pe, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, orderID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return nil // another domain's reference that happens to parse; acknowledged
		}
		if rowID > 0 {
			_, err := tx.Exec(ctx,
				`UPDATE order_owed_captures
				    SET refund_status = 'failed', failed_at = NOW(), failure_code = $2, updated_at = NOW()
				  WHERE payments_owed_id = $1 AND refund_status = 'pending'`, rowID, code)
			return err
		}
		intent := normIntent(ev.IntentID)
		if _, err := tx.Exec(ctx,
			`UPDATE order_refund_commands
			    SET provider_failed_at = COALESCE(provider_failed_at, NOW()), provider_failure_code = $3, updated_at = NOW()
			  WHERE order_id = $1 AND lower(btrim(COALESCE(intent_id,''))) = $2 AND status <> 'succeeded'`,
			orderID, intent, code); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`UPDATE order_owed_captures
			    SET refund_status = 'failed', failed_at = NOW(), failure_code = $3, updated_at = NOW()
			  WHERE order_id = $1 AND intent_id = $2 AND refund_via IN ('order','owed_command')
			    AND refund_status = 'pending'`, orderID, intent, code)
		return err
	})
}

// ─── payment.refunded of an owed payment ─────────────────────────────

// owedRefundTarget is an owed payment a payment.refunded may settle.
type owedRefundTarget struct {
	ID       uuid.UUID
	IntentID string
	owedState
}

// routesToOwed reports whether a refund of intent settles the owed payment w
// rather than the order's own ledger. A refund of a capture the order refused
// (owed_command) always does: that intent never paid the order. A payment
// payments-service owes back does only when its intent is NOT the order's
// bound intent: a second provider payment on the order's own intent is owed
// on that same intent, and a payment.refunded naming it is the order's own
// refund. A late capture (order) is the order's own ledger by design. It has
// no I/O.
func routesToOwed(w owedRefundTarget, boundIntent string) bool {
	switch w.RefundVia {
	case refundViaOwedCommand:
		return true
	case refundViaPayments:
		return normIntent(w.IntentID) != "" && normIntent(w.IntentID) != normIntent(boundIntent)
	}
	return false
}

// owedRefundTargetTx finds the owed payment a refund of intent settles, if
// any, locked.
func owedRefundTargetTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, intent, boundIntent string) (*owedRefundTarget, error) {
	if normIntent(intent) == "" {
		return nil, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT id, COALESCE(intent_id,''), refund_via, amount_minor, amount_exact, refund_due_minor,
		        refunded_minor, refund_status, refunded_at, failed_at
		   FROM order_owed_captures
		  WHERE order_id = $1 AND intent_id = $2
		  ORDER BY detected_at, owed_key
		  FOR UPDATE`, orderID, normIntent(intent))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found *owedRefundTarget
	for rows.Next() {
		var w owedRefundTarget
		if err := rows.Scan(&w.ID, &w.IntentID, &w.RefundVia, &w.AmountMinor, &w.AmountExact, &w.RefundDueMinor,
			&w.RefundedMinor, &w.RefundStatus, &w.RefundedAt, &w.FailedAt); err != nil {
			return nil, err
		}
		if found == nil && routesToOwed(w, boundIntent) {
			c := w
			found = &c
		}
	}
	return found, rows.Err()
}

// owedRefundUnit is the amount a payment.refunded credits to an owed
// payment, in that payment's refund unit: the order value payments counted
// for a command-path refund, the money returned for a payments-path one
// (amount_returned_minor on a bank-offer intent, else amount_minor). It has no
// I/O.
func owedRefundUnit(via string, ev RefundEvent) int64 {
	if via == refundViaPayments && ev.ReturnedMinor != nil && *ev.ReturnedMinor > 0 {
		return *ev.ReturnedMinor
	}
	return ev.AmountMinor.Int64()
}

// creditOwedRefund adds one settled refund to an owed payment. A refund past
// what is due is refused (a reconciliation break, never clamped). It has no
// I/O.
func creditOwedRefund(st owedState, amount int64, now time.Time) (owedState, error) {
	if amount <= 0 {
		return st, &paymentevents.MismatchError{Field: paymentevents.FieldAmount,
			Detail: fmt.Sprintf("refund amount_minor %d of an owed payment", amount)}
	}
	total := st.RefundedMinor + amount
	if st.RefundDueMinor > 0 && total > st.RefundDueMinor {
		return st, &paymentevents.MismatchError{Field: paymentevents.FieldAmount,
			Detail: fmt.Sprintf("refunds total %d on an owed payment of %d", total, st.RefundDueMinor)}
	}
	st.RefundedMinor = total
	if st.RefundDueMinor > 0 && total >= st.RefundDueMinor {
		st.RefundStatus = OwedRefundRefunded
		if st.RefundedAt == nil {
			t := now
			st.RefundedAt = &t
		}
	} else if st.RefundStatus == OwedRefundFailed {
		st.RefundStatus = OwedRefundPending // money is moving again
	}
	return st, nil
}

// settleOwedRefundTx books a payment.refunded to the owed payment it returns,
// never to the order's refund ledger. The caller has claimed the inbox row.
func settleOwedRefundTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, w owedRefundTarget, ev RefundEvent,
	refundID string, out *RefundSettlement) error {
	var seen bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM order_owed_capture_refunds WHERE refund_id = $1)`, refundID).Scan(&seen); err != nil {
		return err
	}
	if seen {
		out.Duplicate = true
		return nil
	}
	amount := owedRefundUnit(w.RefundVia, ev)
	next, err := creditOwedRefund(w.owedState, amount, time.Now())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAmountMismatch, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_owed_capture_refunds (refund_id, owed_id, event_id, amount_minor) VALUES ($1,$2,$3,$4)`,
		refundID, w.ID, ev.EventID, amount); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE order_owed_captures
		    SET refunded_minor = $2, refund_status = $3, refunded_at = $4, updated_at = NOW()
		  WHERE id = $1`, w.ID, next.RefundedMinor, next.RefundStatus, next.RefundedAt); err != nil {
		return err
	}
	if w.RefundVia == refundViaOwedCommand && next.RefundStatus == OwedRefundRefunded {
		if _, err := tx.Exec(ctx,
			`UPDATE order_refund_commands SET status = 'succeeded', settled_at = NOW(), updated_at = NOW()
			  WHERE order_id = $1 AND scope = 'owed_capture' AND lower(btrim(COALESCE(intent_id,''))) = $2
			    AND status <> 'succeeded'`, orderID, normIntent(w.IntentID)); err != nil {
			return err
		}
	}
	slog.InfoContext(ctx, "commerce: refund of an owed payment settled; the order's own refund ledger is untouched",
		"order_id", orderID, "intent_id", w.IntentID, "refund_id", refundID, "amount_minor", amount,
		"refunded_minor", next.RefundedMinor, "refund_status", next.RefundStatus)
	return enqueueOutboxTx(ctx, tx, "commerce.order.owed_payment_refunded", orderID.String(), map[string]any{
		"order_id":       orderID,
		"intent_id":      w.IntentID,
		"refund_id":      refundID,
		"amount_minor":   amount,
		"refunded_minor": next.RefundedMinor,
		"refund_status":  next.RefundStatus,
	})
}

// advanceOrderPathOwedTx moves the owed payments the order refunds through
// its own ledger (a late capture) once a refund of their intent settled
// there: refund counted, and refunded once it covers them or the order is
// fully refunded.
func advanceOrderPathOwedTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, intent string, amount int64, full bool) error {
	_, err := tx.Exec(ctx,
		`UPDATE order_owed_captures
		    SET refunded_minor = LEAST(refund_due_minor, refunded_minor + $3),
		        refund_status  = CASE WHEN $4 OR refunded_minor + $3 >= refund_due_minor THEN 'refunded'
		                              WHEN refund_status = 'failed' THEN 'pending'
		                              ELSE refund_status END,
		        refunded_at    = CASE WHEN $4 OR refunded_minor + $3 >= refund_due_minor
		                              THEN COALESCE(refunded_at, NOW()) ELSE refunded_at END,
		        updated_at     = NOW()
		  WHERE order_id = $1 AND refund_via = 'order' AND refund_status <> 'refunded'
		    AND ($4 OR intent_id = $2 OR intent_id IS NULL)`,
		orderID, normIntent(intent), amount, full)
	return err
}

// ─── A capture the order refuses for its money ───────────────────────

// refusedCaptureRefundable decides whether a payment.succeeded that the
// order's capture check refused is refunded as an owed payment, rather than
// dead-lettered. Only the FIRST money of an intent is (applies, late): a
// redelivery of the order's own payment is never refunded here. The intent
// must be a payments intent and the amount positive, or there is nothing a
// refund command can name. It has no I/O.
func refusedCaptureRefundable(v captureVerdict, e PaymentEvent) bool {
	if v != captureApplies && v != captureLate {
		return false
	}
	if e.AmountMinor <= 0 {
		return false
	}
	_, err := uuid.Parse(strings.TrimSpace(e.IntentID))
	return err == nil
}

// mismatchField is the refused field of a capture check error ("" if none).
func mismatchField(err error) string {
	var m *paymentevents.MismatchError
	if errors.As(err, &m) {
		return m.Field
	}
	return ""
}

// refuseCaptureTx handles a payment payments-service accepted but whose
// amount, currency or payer this order refuses (paymentevents.CheckCapture):
// the money is the buyer's and goes back. Nothing of it touches the order —
// no paid status, no stock, no intent binding. It is recorded as an owed
// payment and refunded through a scope=owed_capture command for the amount
// payments counts on that intent, so neither the refund nor its settlement is
// ever counted as the order's.
//
// An intent that already pays ANOTHER order is not refunded here: that money
// is booked elsewhere, and the refusal stays a dead-lettered, paged mismatch.
func refuseCaptureTx(ctx context.Context, tx pgx.Tx, e PaymentEvent, mismatch error) error {
	intent := normIntent(e.IntentID)
	var elsewhere bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM orders WHERE payment_intent_id::text = $1 AND id <> $2)`,
		intent, e.OrderID).Scan(&elsewhere); err != nil {
		return err
	}
	if elsewhere {
		return fmt.Errorf("%w: %v (intent %s pays another order)", ErrAmountMismatch, mismatch, intent)
	}
	paid, exact := capturedMoney(e)
	field := mismatchField(mismatch)
	if _, err := recordOwedCaptureTx(ctx, tx, owedCapture{
		OrderID: e.OrderID, Key: owedKeyForIntent(intent), IntentID: intent,
		Reason: OwedReasonCaptureMismatch, AmountMinor: paid, AmountExact: exact, Currency: e.Currency,
		RefundVia: refundViaOwedCommand, RefundDueMinor: e.AmountMinor.Int64(),
		Detail: mismatch.Error(),
	}); err != nil {
		return err
	}
	if err := createScopedRefundCommandTx(ctx, tx, e.OrderID, intent, e.AmountMinor,
		"capture_mismatch:"+field, "capture-mismatch:"+e.OrderID.String()+":"+intent, refundScopeOwedCapture); err != nil {
		return err
	}
	slog.ErrorContext(ctx, "commerce: CAPTURE REFUSED — the payment does not match the order and is being refunded; "+
		"the order is unchanged",
		"order_id", e.OrderID, "intent_id", intent, "field", field, "event_id", e.EventID,
		"amount_minor", e.AmountMinor.Int64(), "captured_minor", paid, "currency", e.Currency, "error", mismatch)
	return enqueueOutboxTx(ctx, tx, "commerce.order.capture_refused", e.OrderID.String(), map[string]any{
		"order_id":       e.OrderID,
		"intent_id":      intent,
		"field":          field,
		"amount_minor":   e.AmountMinor.Int64(),
		"captured_minor": paid,
		"currency":       e.Currency,
		"refund_minor":   e.AmountMinor.Int64(),
	})
}
