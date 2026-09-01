package postgres

// Order lifecycle after checkout: payment application, cancellation,
// reservation release and expiry.
//
// Everything here shares one property the previous code did not have — the
// state change and its consequences commit together. Before this file:
//
//	CancelOrder      changed status, never released stock, and returned nil
//	                 after a failed refund (v1 §5.2, §5.13)
//	applyPaidStatus  marked the order paid, then deducted stock in separate
//	                 statements whose errors were logged (review M-5)
//	expiry sweeper   released reservations while the order stayed
//	                 payment_pending, so a late capture could still pay for
//	                 stock that had been given away (review M-5)
//	payments consumer marked a Redis dedupe key BEFORE the DB effect, so a
//	                 crash in between dropped a captured payment forever
//	                 (review R-1)

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrOrderNotFoundP0     = errors.New("order not found")
	ErrNotOrderOwnerP0     = errors.New("actor does not own this order")
	ErrCancelNotPermitted  = errors.New("order cannot be cancelled from this state by this actor")
	ErrAmountMismatch      = errors.New("payment amount does not match the order")
	ErrDuplicatePaymentEvt = errors.New("payment event already applied")
	ErrOrderExpired        = errors.New("order expired before payment")
)

// ─── Payment application (A3 / R-1 / LB-5) ───────────────────────────

// PaymentEvent is a payment lifecycle event as received from Kafka.
type PaymentEvent struct {
	// EventID is the envelope's id. It becomes the inbox key, and it must
	// not be empty — an empty key would let one event mask every later one.
	EventID     string
	EventType   string
	IntentID    string
	OrderID     uuid.UUID
	AmountMinor money.Paise
	Currency    string
	PayerID     uuid.UUID
	ProviderRef string
}

// ApplyPaymentSucceeded records the event, verifies the full tuple, marks the
// order paid and commits its stock — in ONE transaction.
//
// R-1 is the reason this exists rather than a sequence of calls. The shared
// Kafka consumer marks a Redis dedupe key before invoking the handler and
// commits the offset after; if commerce died between the SETNX and the DB
// write, the restart saw the key, skipped the event and committed the
// offset. Razorpay had captured ₹10,000 and the order stayed unpaid forever.
// The durable inbox row here is written in the same transaction as the
// effect, so either both happened or neither did, and Redis is downgraded to
// an advisory fast path.
//
// LB-5 is the other half: the amount, currency and payer are verified
// against the ORDER before anything is marked paid. Verifying only the
// amount would still let a payment for a different order settle this one.
func (s *Store) ApplyPaymentSucceeded(ctx context.Context, e PaymentEvent) error {
	if e.EventID == "" {
		return fmt.Errorf("payment event has no id; refusing to process without a dedupe key")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type', 'system', true)`); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO payment_event_inbox (event_id, event_type, intent_id, order_id, amount_minor)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5)
		 ON CONFLICT (event_id) DO NOTHING`,
		e.EventID, e.EventType, e.IntentID, e.OrderID, e.AmountMinor.Int64())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDuplicatePaymentEvt
	}

	var (
		status     string
		payStatus  string
		totalMinor money.Paise
		currency   string
		customerID uuid.UUID
		intentID   *string
		expiredAt  *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT status, payment_status, COALESCE(final_amount_minor,0), currency_code,
		        customer_user_id, payment_intent_id::text, reservation_expired_at
		   FROM orders WHERE id = $1 FOR UPDATE`, e.OrderID).
		Scan(&status, &payStatus, &totalMinor, &currency, &customerID, &intentID, &expiredAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFoundP0
		}
		return err
	}

	// LB-5 / C6: the full tuple, not just the amount.
	if e.AmountMinor != totalMinor {
		return fmt.Errorf("%w: event %s vs order %s", ErrAmountMismatch, e.AmountMinor, totalMinor)
	}
	if e.Currency != "" && !equalFoldState(e.Currency, currency) {
		return fmt.Errorf("%w: currency %s vs %s", ErrAmountMismatch, e.Currency, currency)
	}
	if e.PayerID != uuid.Nil && e.PayerID != customerID {
		return fmt.Errorf("%w: payer is not the order's customer", ErrAmountMismatch)
	}
	if intentID != nil && e.IntentID != "" && *intentID != e.IntentID {
		return fmt.Errorf("%w: intent does not belong to this order", ErrAmountMismatch)
	}

	// Already paid — idempotent, and the inbox row above already made this
	// a one-shot.
	if payStatus == "paid" {
		return tx.Commit(ctx)
	}

	// M-5: the reservation expired and the order was terminated, but the
	// capture still arrived. We must NOT fulfil — the stock is gone. Record
	// the money as owed back and let the refund worker return it.
	if expiredAt != nil || status == "expired" {
		if err := createRefundCommandTx(ctx, tx, e.OrderID, e.IntentID, e.AmountMinor,
			"late_capture_after_expiry", "late-capture:"+e.OrderID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET late_capture_refund_id = gen_random_uuid(), payment_status = 'refund_pending',
			        updated_at = NOW() WHERE id = $1`, e.OrderID); err != nil {
			return err
		}
		if err := enqueueOutboxTx(ctx, tx, "commerce.order.late_capture_refunded", e.OrderID.String(), map[string]any{
			"order_id": e.OrderID, "amount_minor": e.AmountMinor.Int64(),
			"reason": "reservation expired before capture",
		}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	// Commit the reservation into real stock. Failure aborts the whole
	// transaction — the old code logged it and marked the order paid anyway.
	if err := commitReservationsTx(ctx, tx, e.OrderID); err != nil {
		return fmt.Errorf("commit stock for order %s: %w", e.OrderID, err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE orders
		    SET payment_status = 'paid', status = 'confirmed',
		        payment_id = NULLIF($2,''), updated_at = NOW()
		  WHERE id = $1`, e.OrderID, e.ProviderRef); err != nil {
		return err
	}

	// M-8: the fulfilment job is created IN this transaction. It used to be
	// a separate best-effort write (jobs.go's EnqueueFulfillPaidOrder), so a
	// process exit right after the paid status left an order that nobody
	// would ever fulfil.
	//
	// The unique index from migration 013 gives the job an identity, so a
	// redelivered payment event or a reconciler resolving the same capture
	// cannot enqueue a second one. That matters because this job books a
	// courier shipment: a duplicate costs a second AWB and a second charge.
	if _, err := tx.Exec(ctx,
		`INSERT INTO fulfillment_jobs (kind, payload, status)
		 VALUES ('fulfill_paid_order', jsonb_build_object('order_id', $1::text), 'pending')
		 ON CONFLICT DO NOTHING`, e.OrderID); err != nil {
		return fmt.Errorf("enqueue fulfilment for order %s: %w", e.OrderID, err)
	}

	if err := enqueueOutboxTx(ctx, tx, "commerce.order.paid", e.OrderID.String(), map[string]any{
		"order_id":     e.OrderID,
		"user_id":      customerID,
		"amount_minor": totalMinor.Int64(),
		"currency":     currency,
		"intent_id":    e.IntentID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ApplyPaymentFailed releases the hold so the stock returns to sale.
func (s *Store) ApplyPaymentFailed(ctx context.Context, e PaymentEvent) error {
	if e.EventID == "" {
		return fmt.Errorf("payment event has no id")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type', 'system', true)`); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO payment_event_inbox (event_id, event_type, intent_id, order_id, amount_minor)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5) ON CONFLICT (event_id) DO NOTHING`,
		e.EventID, e.EventType, e.IntentID, e.OrderID, e.AmountMinor.Int64())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDuplicatePaymentEvt
	}

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM orders WHERE id = $1 FOR UPDATE`, e.OrderID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFoundP0
		}
		return err
	}
	if status != "payment_pending" {
		return tx.Commit(ctx) // nothing to do
	}
	if err := releaseReservationsTx(ctx, tx, e.OrderID, "checkout_release_payment_failed"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'payment_failed', payment_status = 'failed', updated_at = NOW()
		  WHERE id = $1`, e.OrderID); err != nil {
		return err
	}
	if err := enqueueOutboxTx(ctx, tx, "commerce.order.payment_failed", e.OrderID.String(), map[string]any{
		"order_id": e.OrderID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Reservation release and commit (LB-21, LB-23) ───────────────────

// releaseReservationsTx returns held stock and writes the ledger entries.
//
// LB-21 / v1 §5.9. The previous implementation was:
//
//	DELETE FROM inventory_reservations
//	 WHERE variant_id=$1 AND user_id=$2 AND order_id IS NULL LIMIT 1
//
// which is invalid PostgreSQL (DELETE takes no LIMIT) AND, even with the
// syntax fixed, matched nothing: a checkout reservation always carries an
// order_id, so `order_id IS NULL` could never be true. The release path had
// never worked, and nothing in CI ran it.
//
// Reservations are marked released rather than deleted, so a stock question
// can still be answered after the fact.
func releaseReservationsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, reason string) error {
	rows, err := tx.Query(ctx,
		`SELECT id, variant_id, quantity FROM inventory_reservations
		  WHERE order_id = $1 AND released_at IS NULL AND committed_at IS NULL
		  FOR UPDATE`, orderID)
	if err != nil {
		return err
	}
	type held struct {
		id      uuid.UUID
		variant uuid.UUID
		qty     int
	}
	var holds []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.variant, &h.qty); err != nil {
			rows.Close()
			return err
		}
		holds = append(holds, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, h := range holds {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items
			    SET reserved_qty = reserved_qty - $2, updated_at = NOW()
			  WHERE variant_id = $1`, h.variant, h.qty); err != nil {
			return fmt.Errorf("release %s: %w", h.variant, err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_reservations
			    SET released_at = NOW(), release_reason = $2 WHERE id = $1`, h.id, reason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO inventory_ledger
			     (variant_id, order_id, reservation_id, delta_reserved, reason, actor_type)
			 VALUES ($1,$2,$3,$4,$5,'system')`,
			h.variant, orderID, h.id, -h.qty, reason); err != nil {
			return err
		}
	}
	return nil
}

// commitReservationsTx converts a hold into a real stock decrement.
//
// LB-23: no GREATEST(0,…) anywhere. If this would take stock negative the
// CHECK constraint raises and the payment transaction rolls back, which
// surfaces a genuine inventory break instead of clamping it away.
func commitReservationsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	rows, err := tx.Query(ctx,
		`SELECT id, variant_id, quantity FROM inventory_reservations
		  WHERE order_id = $1 AND released_at IS NULL AND committed_at IS NULL
		  FOR UPDATE`, orderID)
	if err != nil {
		return err
	}
	type held struct {
		id      uuid.UUID
		variant uuid.UUID
		qty     int
	}
	var holds []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.variant, &h.qty); err != nil {
			rows.Close()
			return err
		}
		holds = append(holds, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(holds) == 0 {
		return fmt.Errorf("no live reservation for order %s; refusing to mark it paid without held stock", orderID)
	}

	for _, h := range holds {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items
			    SET total_qty = total_qty - $2,
			        reserved_qty = reserved_qty - $2,
			        updated_at = NOW()
			  WHERE variant_id = $1`, h.variant, h.qty); err != nil {
			return fmt.Errorf("commit %s: %w", h.variant, err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_reservations SET committed_at = NOW() WHERE id = $1`, h.id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO inventory_ledger
			     (variant_id, order_id, reservation_id, delta_total, delta_reserved, reason, actor_type)
			 VALUES ($1,$2,$3,$4,$5,'payment_commit','system')`,
			h.variant, orderID, h.id, -h.qty, -h.qty); err != nil {
			return err
		}
	}
	return nil
}

// ─── Cancellation (LB-10, D6, LB-8) ──────────────────────────────────

// CancelOrder cancels an order, releases its stock and, when money was
// captured, records a DURABLE refund command.
//
// M-2 is the headline fix: the previous CancelOrder never compared the actor
// to `orders.customer_user_id`, so knowing an order's UUID was enough to
// cancel a stranger's order. Combined with the (then absent) stock release
// and refund, that would have let an attacker release a victim's stock and
// trigger a refund on their behalf.
//
// The transition itself is checked by the migration-010 trigger against the
// D6 matrix, so the permitted (state, actor) pairs live in one place instead
// of in a Go map that could drift from the audit trail.
func (s *Store) CancelOrder(ctx context.Context, orderID, actorID uuid.UUID, actorType, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type', $1, true)`, actorOr(actorType)); err != nil {
		return err
	}

	var (
		customerID uuid.UUID
		status     string
		payStatus  string
		totalMinor money.Paise
		intentID   *string
	)
	err = tx.QueryRow(ctx,
		`SELECT customer_user_id, status, payment_status, COALESCE(final_amount_minor,0),
		        payment_intent_id::text
		   FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&customerID, &status, &payStatus, &totalMinor, &intentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFoundP0
		}
		return err
	}

	// M-2: ownership. A seller or admin acts under their own actor type and
	// is authorised by the transition matrix; a customer must own the order.
	if actorType == "customer" && customerID != actorID {
		return ErrNotOrderOwnerP0
	}

	if err := releaseReservationsTx(ctx, tx, orderID, "checkout_release_cancel"); err != nil {
		return err
	}

	// If stock was already committed (the order was paid), give it back.
	if payStatus == "paid" {
		if err := restockPaidOrderTx(ctx, tx, orderID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'cancelled', cancellation_reason = $2, cancelled_by = $3,
		        updated_at = NOW() WHERE id = $1`,
		orderID, reason, actorOr(actorType)); err != nil {
		if isCheckViolation(err) {
			return ErrCancelNotPermitted
		}
		return err
	}

	// LB-8 / v1 §5.13. The old code had three separate `slog.Warn` +
	// `return nil` branches here — no payments client, no intent, refund
	// call failed — each of which reported success while no money moved and
	// nothing remembered the debt. A refund is now a durable command in the
	// same transaction as the cancellation, and a worker owns delivering it.
	if payStatus == "paid" && totalMinor > 0 {
		intent := ""
		if intentID != nil {
			intent = *intentID
		}
		if err := createRefundCommandTx(ctx, tx, orderID, intent, totalMinor,
			"cancel:"+reason, "cancel:"+orderID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET payment_status = 'refund_pending', updated_at = NOW() WHERE id = $1`,
			orderID); err != nil {
			return err
		}
	}

	if err := enqueueOutboxTx(ctx, tx, "commerce.order.cancelled", orderID.String(), map[string]any{
		"order_id":     orderID,
		"user_id":      customerID,
		"actor_type":   actorOr(actorType),
		"reason":       reason,
		"was_paid":     payStatus == "paid",
		"refund_minor": totalMinor.Int64(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// restockPaidOrderTx returns committed stock for a paid order that is being
// cancelled before dispatch.
func restockPaidOrderTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	rows, err := tx.Query(ctx,
		`SELECT variant_id, quantity FROM order_items WHERE order_id = $1`, orderID)
	if err != nil {
		return err
	}
	type line struct {
		variant uuid.UUID
		qty     int
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.variant, &l.qty); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items SET total_qty = total_qty + $2, updated_at = NOW()
			  WHERE variant_id = $1`, l.variant, l.qty); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO inventory_ledger (variant_id, order_id, delta_total, reason, actor_type)
			 VALUES ($1,$2,$3,'checkout_release_cancel','system')`,
			l.variant, orderID, l.qty); err != nil {
			return err
		}
	}
	return nil
}

// ─── Durable refund commands, commerce side (LB-8) ───────────────────

func createRefundCommandTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, intentID string, amount money.Paise, reason, idemKey string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO order_refund_commands (order_id, intent_id, amount_minor, reason, idempotency_key)
		 VALUES ($1, NULLIF($2,''), $3, $4, $5)
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		orderID, intentID, amount.Int64(), reason, idemKey)
	return err
}

// RefundCommand is a refund commerce owes and has not yet confirmed.
type RefundCommand struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	IntentID       string
	AmountMinor    money.Paise
	Reason         string
	IdempotencyKey string
	Attempts       int
}

// ClaimDueRefundCommands takes work for the refund worker. SKIP LOCKED so
// replicas share the queue without duplicating a refund.
func (s *Store) ClaimDueRefundCommands(ctx context.Context, limit int) ([]RefundCommand, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE order_refund_commands
		   SET attempts = attempts + 1,
		       next_attempt_at = NOW() + (LEAST(attempts + 1, 8) * INTERVAL '30 seconds'),
		       updated_at = NOW()
		 WHERE id IN (
		       SELECT id FROM order_refund_commands
		        WHERE status IN ('pending','submitted') AND next_attempt_at <= NOW()
		        ORDER BY next_attempt_at
		        FOR UPDATE SKIP LOCKED LIMIT $1)
		RETURNING id, order_id, COALESCE(intent_id,''), amount_minor, reason, idempotency_key, attempts`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundCommand
	for rows.Next() {
		var c RefundCommand
		if err := rows.Scan(&c.ID, &c.OrderID, &c.IntentID, &c.AmountMinor,
			&c.Reason, &c.IdempotencyKey, &c.Attempts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkRefundSubmitted records that payments accepted the command.
func (s *Store) MarkRefundSubmitted(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE order_refund_commands SET status = 'submitted', last_error = NULL, updated_at = NOW()
		  WHERE id = $1 AND status IN ('pending','submitted')`, id)
	return err
}

// MarkRefundFailed keeps the command claimable, or parks it for a human.
// `needs_attention` rather than `failed`: money we could not return is still
// owed, and it must page someone rather than disappear from the queue.
func (s *Store) MarkRefundFailed(ctx context.Context, id uuid.UUID, reason string, terminal bool) error {
	status := "pending"
	if terminal {
		status = "needs_attention"
	}
	_, err := s.db.Exec(ctx,
		`UPDATE order_refund_commands SET status = $3, last_error = $2, updated_at = NOW()
		  WHERE id = $1 AND status IN ('pending','submitted')`, id, reason, status)
	return err
}

// SettleRefund marks a refund confirmed by the payments refund event.
func (s *Store) SettleRefund(ctx context.Context, orderID uuid.UUID, intentID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type','system',true)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE order_refund_commands SET status = 'succeeded', settled_at = NOW(), updated_at = NOW()
		  WHERE order_id = $1 AND status <> 'succeeded'`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET payment_status = 'refunded', updated_at = NOW() WHERE id = $1`, orderID); err != nil {
		return err
	}
	if err := enqueueOutboxTx(ctx, tx, "commerce.order.refunded", orderID.String(), map[string]any{
		"order_id": orderID, "intent_id": intentID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// OldestUnsettledRefundAge feeds the refund-pending-age alarm.
func (s *Store) OldestUnsettledRefundAge(ctx context.Context) (time.Duration, error) {
	var secs *float64
	if err := s.db.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))
		   FROM order_refund_commands WHERE status <> 'succeeded'`).Scan(&secs); err != nil {
		return 0, err
	}
	if secs == nil {
		return 0, nil
	}
	return time.Duration(*secs) * time.Second, nil
}

// ─── Reservation expiry (LB-22 / M-5) ────────────────────────────────

// ExpireStaleOrders terminally expires unpaid orders whose hold has lapsed.
//
// M-5: the old sweeper released the reservation and left the order
// `payment_pending`, so a capture arriving afterwards still applied — with
// its stock errors merely logged. A's hold expires, B buys the last unit,
// A's delayed capture lands, and both orders exist against one unit while A
// has been charged.
//
// Expiry is now terminal and durable. A late capture for an expired order is
// handled in ApplyPaymentSucceeded, which refunds it instead of fulfilling.
func (s *Store) ExpireStaleOrders(ctx context.Context, limit int) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT o.id
		  FROM orders o
		  JOIN inventory_reservations r ON r.order_id = o.id
		 WHERE o.status = 'payment_pending'
		   AND r.released_at IS NULL AND r.committed_at IS NULL
		   AND r.expires_at < NOW()
		 LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	expired := 0
	for _, id := range ids {
		if err := s.expireOne(ctx, id); err != nil {
			// One stuck order must not stop the sweep.
			continue
		}
		expired++
	}
	return expired, nil
}

func (s *Store) expireOne(ctx context.Context, orderID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type','system',true)`); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status); err != nil {
		return err
	}
	if status != "payment_pending" {
		return nil // paid or cancelled while we queued
	}
	if err := releaseReservationsTx(ctx, tx, orderID, "checkout_release_expiry"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'expired', reservation_expired_at = NOW(), updated_at = NOW()
		  WHERE id = $1`, orderID); err != nil {
		return err
	}
	if err := enqueueOutboxTx(ctx, tx, "commerce.inventory.released", orderID.String(), map[string]any{
		"order_id": orderID, "reason": "reservation_expiry",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// OldestLiveReservationAge feeds the reservation-age alarm: if this exceeds
// twice the TTL, the sweeper is stuck.
func (s *Store) OldestLiveReservationAge(ctx context.Context) (time.Duration, error) {
	var secs *float64
	if err := s.db.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))
		   FROM inventory_reservations
		  WHERE released_at IS NULL AND committed_at IS NULL`).Scan(&secs); err != nil {
		return 0, err
	}
	if secs == nil {
		return 0, nil
	}
	return time.Duration(*secs) * time.Second, nil
}

// OrderPaymentIntentID returns the payments-service intent bound to an order.
//
// LB-4: exactly one intent per order, authored by commerce. A nil UUID means
// no payment has been opened yet, which is a legitimate state between
// checkout and the client asking to pay.
func (s *Store) OrderPaymentIntentID(ctx context.Context, orderID uuid.UUID) (uuid.UUID, error) {
	var id *uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT payment_intent_id FROM orders WHERE id = $1`, orderID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrOrderNotFoundP0
		}
		return uuid.Nil, err
	}
	if id == nil {
		return uuid.Nil, nil
	}
	return *id, nil
}

// BindPaymentIntent records the intent commerce opened for an order.
//
// The unique index from migration 008 makes a second intent for the same
// order impossible, so a retry cannot create a second payable for the same
// goods.
func (s *Store) BindPaymentIntent(ctx context.Context, orderID, intentID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE orders SET payment_intent_id = $2, updated_at = NOW()
		  WHERE id = $1 AND (payment_intent_id IS NULL OR payment_intent_id = $2)`,
		orderID, intentID)
	return err
}
