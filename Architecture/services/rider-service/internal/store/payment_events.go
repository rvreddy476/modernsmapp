package store

// Online ride payments: binding a payments-service intent to a ride payment
// or an outstanding fee, switching an unpaid online payment to cash, and
// applying the signed payment events.
//
// A ride payment is marked paid, failed or refunded, and an outstanding fee
// settled, ONLY in ApplyRidePaymentEvent, in the same transaction as the
// rider_payment_inbox row (shared/paymentevents.ApplyOnce). The intent echo
// and the advisory callback verdict bind an intent and nothing more.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrPaymentNotSwitchable: the ride's payment is not an unpaid online
	// payment (cash already, or paid / refunded).
	ErrPaymentNotSwitchable = errors.New("ride_payment: not an unpaid online payment")
	// ErrPaymentNotBindable: the payment row cannot take an intent (cash, or
	// money already taken).
	ErrPaymentNotBindable = errors.New("ride_payment: cannot bind an intent")
)

const ridePaymentColumns = `id, ride_id, partner_id, amount_paise, payment_method, status, wallet_txn_id, upi_txn_ref, intent_id, provider_reference, refunded_paise, failure_reason, created_at, updated_at, settled_at`

// BindRidePaymentIntent records the payments intent opened for an unpaid
// online ride payment and moves the row to confirming. The ride's
// payment_method follows the chosen instrument. A cash row, or one whose
// money is already taken, is ErrPaymentNotBindable.
func (s *Store) BindRidePaymentIntent(ctx context.Context, paymentID, intentID uuid.UUID, providerRef, method string) (*RidePayment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	const q = `
        UPDATE rider_ride_payments
        SET intent_id = $2, provider_reference = NULLIF($3, ''), payment_method = $4,
            status = 'confirming', failure_reason = NULL, updated_at = NOW()
        WHERE id = $1 AND payment_method IN ('upi','card') AND status IN ('pending','confirming','failed')
        RETURNING ` + ridePaymentColumns
	p, err := scanRidePayment(tx.QueryRow(ctx, q, paymentID, intentID, providerRef, method))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotBindable
		}
		return nil, fmt.Errorf("bind ride payment intent: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rider_rides SET payment_method = $2, updated_at = NOW() WHERE id = $1`, p.RideID, method); err != nil {
		return nil, fmt.Errorf("bind ride payment method: %w", err)
	}
	return p, tx.Commit(ctx)
}

// SwitchRidePaymentToCash turns the ride's latest unpaid online payment
// (pending, confirming or failed) into a cash payment awaiting the
// captain's confirmation. Never after paid: ErrPaymentNotSwitchable.
func (s *Store) SwitchRidePaymentToCash(ctx context.Context, rideID uuid.UUID) (*RidePayment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	const q = `
        UPDATE rider_ride_payments
        SET payment_method = 'cash', status = 'pending_cash_confirmation', failure_reason = NULL, updated_at = NOW()
        WHERE id = (SELECT id FROM rider_ride_payments WHERE ride_id = $1 ORDER BY created_at DESC LIMIT 1)
          AND payment_method IN ('upi','card') AND status IN ('pending','confirming','failed')
        RETURNING ` + ridePaymentColumns
	p, err := scanRidePayment(tx.QueryRow(ctx, q, rideID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotSwitchable
		}
		return nil, fmt.Errorf("switch ride payment to cash: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rider_rides SET payment_method = 'cash', updated_at = NOW() WHERE id = $1`, rideID); err != nil {
		return nil, fmt.Errorf("switch ride payment method: %w", err)
	}
	return p, tx.Commit(ctx)
}

// BindOutstandingIntent records the payments intent opened to pay a
// pending, unreserved outstanding fee directly.
func (s *Store) BindOutstandingIntent(ctx context.Context, id, customerID, intentID uuid.UUID, method string) (*CustomerOutstanding, error) {
	const q = `
        UPDATE rider_customer_outstanding
        SET intent_id = $3, intent_method = $4
        WHERE id = $1 AND customer_user_id = $2 AND status = 'pending' AND settled_by_ride_id IS NULL
        RETURNING ` + outstandingColumns
	o, err := scanOutstanding(s.db.QueryRow(ctx, q, id, customerID, intentID, method))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOutstandingNotFound
		}
		return nil, fmt.Errorf("bind outstanding intent: %w", err)
	}
	return o, nil
}

// RidePaymentFilter is the admin ride-payments list filter. Cursor is the
// opaque value the previous page returned.
type RidePaymentFilter struct {
	Status string
	Method string
	From   *time.Time
	To     *time.Time
	Cursor string
	Limit  int
}

// AdminRidePayment is a ride payment row with the ride's customer, for the
// console's Customer column.
type AdminRidePayment struct {
	RidePayment
	CustomerUserID uuid.UUID `json:"customer_user_id"`
}

// ListRidePaymentsAdmin lists ride payments newest first, keyset-paged on
// (created_at, id). It returns the next cursor, empty on the last page.
func (s *Store) ListRidePaymentsAdmin(ctx context.Context, f RidePaymentFilter) ([]AdminRidePayment, string, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var cursorAt *time.Time
	var cursorID *uuid.UUID
	if f.Cursor != "" {
		at, id, err := decodePaymentCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid: cursor")
		}
		cursorAt, cursorID = &at, &id
	}
	const q = `
        SELECT p.id, p.ride_id, p.partner_id, p.amount_paise, p.payment_method, p.status, p.wallet_txn_id, p.upi_txn_ref,
               p.intent_id, p.provider_reference, p.refunded_paise, p.failure_reason, p.created_at, p.updated_at, p.settled_at,
               r.customer_user_id
        FROM rider_ride_payments p
        JOIN rider_rides r ON r.id = p.ride_id
        WHERE ($1 = '' OR p.status = $1)
          AND ($2 = '' OR p.payment_method = $2)
          AND ($3::timestamptz IS NULL OR p.created_at >= $3)
          AND ($4::timestamptz IS NULL OR p.created_at < $4)
          AND ($5::timestamptz IS NULL OR (p.created_at, p.id) < ($5, $6::uuid))
        ORDER BY p.created_at DESC, p.id DESC
        LIMIT $7`
	rows, err := s.db.Query(ctx, q, f.Status, f.Method, f.From, f.To, cursorAt, cursorID, f.Limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list ride payments: %w", err)
	}
	defer rows.Close()
	out := []AdminRidePayment{}
	for rows.Next() {
		var p AdminRidePayment
		if err := rows.Scan(&p.ID, &p.RideID, &p.PartnerID, &p.AmountPaise, &p.PaymentMethod, &p.Status, &p.WalletTxnID, &p.UPITxnRef,
			&p.IntentID, &p.ProviderReference, &p.RefundedPaise, &p.FailureReason, &p.CreatedAt, &p.UpdatedAt, &p.SettledAt,
			&p.CustomerUserID); err != nil {
			return nil, "", fmt.Errorf("scan ride payment: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = encodePaymentCursor(last.CreatedAt, last.ID)
	}
	return out, next, nil
}

func encodePaymentCursor(at time.Time, id uuid.UUID) string {
	return at.UTC().Format(time.RFC3339Nano) + "|" + id.String()
}

func decodePaymentCursor(c string) (time.Time, uuid.UUID, error) {
	at, rest, ok := strings.Cut(c, "|")
	if !ok {
		return time.Time{}, uuid.Nil, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(rest)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, id, nil
}

// --- Signed payment events -------------------------------------------------

// ApplyRidePaymentEvent applies one payments-service event in ONE
// transaction:
//
//  1. INSERT the rider_payment_inbox row; a conflict on event_id means the
//     event was already applied -> OutcomeDuplicate, nothing else runs;
//  2. lock the target: the ride's latest payment row, or the outstanding
//     row, that the reference names;
//  3. payments.Decide;
//  4. apply the effect; on a mismatch, queue a reconciliation_required row
//     instead (nothing is marked paid);
//  5. record the outcome on the inbox row; COMMIT.
//
// Steps 1 and 2-5 are paymentevents.ApplyOnce over paymentInbox. A mismatch
// or an unknown target commits the inbox row with no effect, so it is
// recorded once and not retried. Any error rolls back the inbox row with the
// rest.
func (s *Store) ApplyRidePaymentEvent(ctx context.Context, ev payments.Event) (payments.Applied, error) {
	applied := payments.Applied{TargetID: ev.ReferenceID}
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, paymentevents.ErrNoEventID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer tx.Rollback(ctx)

	claim := paymentevents.Claim{
		EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.ReferenceID, AmountMinor: ev.AmountMinor, Currency: ev.Currency,
	}
	err = paymentevents.ApplyOnce(ctx, tx, paymentInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return applyRidePaymentEffectTx(ctx, tx, ev, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = payments.Decision{Outcome: payments.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, tx.Commit(ctx)
}

// paymentInbox is rider_payment_inbox, the dedupe half of ApplyOnce.
type paymentInbox struct{}

func (paymentInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
        INSERT INTO rider_payment_inbox (event_id, event_type, intent_id, reference_id, amount_minor, currency)
        VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
        ON CONFLICT (event_id) DO NOTHING`,
		c.EventID, c.EventType, c.IntentID, c.ReferenceID, c.AmountMinor, c.Currency)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// lockPaymentTarget resolves the reference by type: mopedu_subscription is
// the checkout row; mopedu_ride (or unstated) is the ride's latest payment
// row (FOR UPDATE), or the outstanding row. A ride with no payment row yet
// is reported as Kind ride with a nil ID (found, no row to pay).
// RuleRefundPending is set when a rule refund in requested/accepted exists
// for the event's intent on that target.
func lockPaymentTarget(ctx context.Context, tx pgx.Tx, ev payments.Event) (payments.Snapshot, bool, error) {
	ref := ev.ReferenceID
	if ev.ReferenceType == payments.RefTypeMopeduSubscription {
		return lockSubscriptionTarget(ctx, tx, ref)
	}
	snap := payments.Snapshot{Kind: payments.TargetRide, RideID: ref, Currency: payments.CurrencyINR}
	err := tx.QueryRow(ctx, `
        SELECT p.id, p.status, p.payment_method, p.amount_paise, p.refunded_paise, COALESCE(p.intent_id::text, ''), r.customer_user_id,
               EXISTS (SELECT 1 FROM rider_ride_refunds f
                       WHERE f.payment_id = p.id AND f.rule_code <> 'discretionary'
                         AND f.intent_id::text = $2 AND f.status IN ('requested','accepted'))
        FROM rider_ride_payments p
        JOIN rider_rides r ON r.id = p.ride_id
        WHERE p.ride_id = $1
        ORDER BY p.created_at DESC
        LIMIT 1
        FOR UPDATE OF p`, ref, ev.IntentID,
	).Scan(&snap.ID, &snap.Status, &snap.Method, &snap.AmountMinor, &snap.RefundedMinor, &snap.IntentID, &snap.PayerID, &snap.RuleRefundPending)
	if err == nil {
		return snap, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return snap, false, fmt.Errorf("lock ride payment: %w", err)
	}
	// A ride without a payment row (not completed yet).
	err = tx.QueryRow(ctx, `SELECT customer_user_id FROM rider_rides WHERE id = $1 FOR UPDATE`, ref).Scan(&snap.PayerID)
	if err == nil {
		snap.ID = uuid.Nil
		return snap, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return snap, false, fmt.Errorf("lock ride: %w", err)
	}
	snap = payments.Snapshot{Kind: payments.TargetOutstanding, ID: ref, Currency: payments.CurrencyINR}
	// For a settled fee the intent to compare is the one that settled it
	// (the refund event names that intent).
	err = tx.QueryRow(ctx, `
        SELECT o.ride_id, o.customer_user_id, o.amount_paise, o.status,
               COALESCE(CASE WHEN o.status = 'settled' THEN o.settled_intent_id ELSE o.intent_id END::text, ''),
               EXISTS (SELECT 1 FROM rider_ride_refunds f
                       WHERE f.outstanding_id = o.id AND f.rule_code <> 'discretionary'
                         AND f.intent_id::text = $2 AND f.status IN ('requested','accepted'))
        FROM rider_customer_outstanding o WHERE o.id = $1 FOR UPDATE`, ref, ev.IntentID,
	).Scan(&snap.RideID, &snap.PayerID, &snap.AmountMinor, &snap.Status, &snap.IntentID, &snap.RuleRefundPending)
	if err == nil {
		return snap, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return snap, false, nil
	}
	return snap, false, fmt.Errorf("lock outstanding: %w", err)
}

// lockSubscriptionTarget locks the checkout row. Status is the row's
// payment_status; the payer is the captain's user id; the amount the plan
// price the checkout was opened for.
func lockSubscriptionTarget(ctx context.Context, tx pgx.Tx, ref uuid.UUID) (payments.Snapshot, bool, error) {
	snap := payments.Snapshot{Kind: payments.TargetSubscription, ID: ref, Currency: payments.CurrencyINR}
	var status *string
	err := tx.QueryRow(ctx, `
        SELECT s.partner_id, p.user_id, s.amount_paise, s.payment_status, COALESCE(s.intent_id::text, '')
        FROM rider_partner_subscriptions s
        JOIN rider_partners p ON p.id = s.partner_id
        WHERE s.id = $1
        FOR UPDATE OF s`, ref,
	).Scan(&snap.RideID, &snap.PayerID, &snap.AmountMinor, &status, &snap.IntentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return snap, false, nil
		}
		return snap, false, fmt.Errorf("lock subscription: %w", err)
	}
	// RideID carries the partner id for a subscription target (Applied
	// copies it to PartnerID). A legacy row without a checkout has no
	// payment_status: a capture for it is a mismatch, never an activation.
	if status != nil {
		snap.Status = *status
	} else {
		snap.Status = "legacy"
	}
	return snap, true, nil
}

func applyRidePaymentEffectTx(ctx context.Context, tx pgx.Tx, ev payments.Event, applied *payments.Applied) error {
	snap, found, err := lockPaymentTarget(ctx, tx, ev)
	if err != nil {
		return err
	}
	if !found {
		applied.Decision = payments.Decision{Outcome: payments.OutcomeTargetNotFound, Detail: "no ride, outstanding fee or subscription " + ev.ReferenceID.String()}
		return recordInboxOutcome(ctx, tx, ev.EventID, applied.Decision)
	}
	applied.Target, applied.RideID, applied.TargetID, applied.CustomerID, applied.Status = snap.Kind, snap.RideID, snap.ID, snap.PayerID, snap.Status
	if snap.Kind == payments.TargetSubscription {
		applied.PartnerID, applied.RideID = snap.RideID, uuid.Nil
	}

	var d payments.Decision
	if snap.Kind == payments.TargetRide && snap.ID == uuid.Nil {
		d = payments.Decision{Outcome: payments.OutcomeMismatch, Detail: "ride has no payment row (not completed)"}
	} else {
		d = payments.Decide(snap, ev)
	}
	applied.Decision = d

	switch d.Effect {
	case payments.EffectMarkPaid:
		if _, err := tx.Exec(ctx, `
            UPDATE rider_ride_payments
            SET status = 'succeeded', settled_at = NOW(), updated_at = NOW(),
                provider_reference = COALESCE(NULLIF($2, ''), provider_reference),
                intent_id = COALESCE(intent_id, NULLIF($3, '')::uuid)
            WHERE id = $1`, snap.ID, ev.ProviderRef, ev.IntentID); err != nil {
			return fmt.Errorf("mark ride payment paid: %w", err)
		}
		applied.Status = payments.StatusSucceeded
		payload, _ := json.Marshal(map[string]any{"ride_id": snap.RideID.String(), "payment_id": snap.ID.String(),
			"amount": snap.AmountMinor, "method": snap.Method, "intent_id": ev.IntentID})
		if err := InsertOutboxEventTx(ctx, tx, "rider.ride.payment.reconciled", "ride", snap.RideID.String(), payload); err != nil {
			return fmt.Errorf("outbox payment reconciled: %w", err)
		}
	case payments.EffectMarkFailed:
		reason := strings.TrimSpace(ev.Reason)
		if reason == "" {
			reason = "payment failed"
		}
		if _, err := tx.Exec(ctx, `
            UPDATE rider_ride_payments SET status = 'failed', failure_reason = $2, updated_at = NOW() WHERE id = $1`,
			snap.ID, reason); err != nil {
			return fmt.Errorf("mark ride payment failed: %w", err)
		}
		applied.Status = payments.StatusFailed
	case payments.EffectRefund, payments.EffectPartialRefund:
		status := payments.StatusPartiallyRefunded
		if d.Effect == payments.EffectRefund {
			status = payments.StatusRefunded
		}
		if _, err := tx.Exec(ctx, `
            UPDATE rider_ride_payments
            SET refunded_paise = refunded_paise + $2, status = $3, updated_at = NOW()
            WHERE id = $1`, snap.ID, ev.AmountMinor, status); err != nil {
			return fmt.Errorf("apply ride refund: %w", err)
		}
		// The oldest in-flight refund of this amount for the intent is the
		// one the PSP settled (refunds are filed one at a time per ride).
		if _, err := tx.Exec(ctx, `
            UPDATE rider_ride_refunds
            SET status = 'refunded', provider_reference = COALESCE(NULLIF($3, ''), provider_reference), updated_at = NOW()
            WHERE id = (
                SELECT id FROM rider_ride_refunds
                WHERE payment_id = $1 AND status IN ('requested','accepted') AND amount_paise = $2
                ORDER BY created_at ASC LIMIT 1)`, snap.ID, ev.AmountMinor, ev.ProviderRef); err != nil {
			return fmt.Errorf("settle ride refund row: %w", err)
		}
		applied.Status = status
	case payments.EffectSettleOutstanding:
		if _, err := tx.Exec(ctx, `
            UPDATE rider_customer_outstanding
            SET status = 'settled', settled_at = NOW(), settled_intent_id = NULLIF($2, '')::uuid, settled_by_ride_id = NULL
            WHERE id = $1 AND status = 'pending'`, snap.ID, ev.IntentID); err != nil {
			return fmt.Errorf("settle outstanding: %w", err)
		}
		applied.Status = payments.OutstandingSettled
	case payments.EffectRefundDuplicate:
		// Rule (b): the second intent's full amount goes back, filed as a
		// rule refund the service sends to payments after commit; the paid
		// row is untouched and a reconciliation row records the double take.
		refundID := uuid.New()
		if _, err := tx.Exec(ctx, `
            INSERT INTO rider_ride_refunds (id, ride_id, payment_id, intent_id, amount_paise, reason, requested_by, rule_code)
            VALUES ($1, $2, $3, $4::uuid, $5, $6, $7, $8)
            ON CONFLICT DO NOTHING`,
			refundID, snap.RideID, snap.ID, ev.IntentID, ev.AmountMinor, payments.RuleDuplicateCapture, payments.SystemActorID, payments.RuleDuplicateCapture); err != nil {
			return fmt.Errorf("file duplicate-capture refund: %w", err)
		}
		// ON CONFLICT (the rule already filed for this intent) leaves the
		// earlier row; report that one so the service does not refund twice.
		if err := tx.QueryRow(ctx, `
            SELECT id FROM rider_ride_refunds
            WHERE payment_id = $1 AND intent_id = $2::uuid AND rule_code = $3`, snap.ID, ev.IntentID, payments.RuleDuplicateCapture,
		).Scan(&refundID); err != nil {
			return fmt.Errorf("read duplicate-capture refund: %w", err)
		}
		applied.RefundID = refundID
		if _, err := tx.Exec(ctx, `
            INSERT INTO rider_payment_reconciliation (ride_id, payment_id, observed_status, canonical_status, next_retry_at, terminal_reason, updated_at)
            VALUES ($1, $2, $3, 'duplicate_capture', NOW(), $4, NOW())`,
			snap.RideID, snap.ID, ev.EventType+":"+ev.Status, d.Detail+"; rule refund "+refundID.String()); err != nil {
			return fmt.Errorf("record duplicate reconciliation: %w", err)
		}
	case payments.EffectSettleDuplicateRefund:
		if _, err := tx.Exec(ctx, `
            UPDATE rider_ride_refunds
            SET status = 'refunded', provider_reference = COALESCE(NULLIF($3, ''), provider_reference), updated_at = NOW()
            WHERE payment_id = $1 AND intent_id = $2::uuid AND rule_code <> 'discretionary' AND status IN ('requested','accepted')`,
			snap.ID, ev.IntentID, ev.ProviderRef); err != nil {
			return fmt.Errorf("settle duplicate refund row: %w", err)
		}
	case payments.EffectRefundOutstanding:
		if _, err := tx.Exec(ctx, `
            UPDATE rider_customer_outstanding SET status = 'refunded', refunded_at = NOW() WHERE id = $1 AND status = 'settled'`, snap.ID); err != nil {
			return fmt.Errorf("mark outstanding refunded: %w", err)
		}
		if _, err := tx.Exec(ctx, `
            UPDATE rider_ride_refunds
            SET status = 'refunded', provider_reference = COALESCE(NULLIF($3, ''), provider_reference), updated_at = NOW()
            WHERE outstanding_id = $1 AND intent_id = $2::uuid AND status IN ('requested','accepted')`,
			snap.ID, ev.IntentID, ev.ProviderRef); err != nil {
			return fmt.Errorf("settle outstanding refund row: %w", err)
		}
		applied.Status = payments.OutstandingRefunded
	case payments.EffectActivateSubscription:
		if err := activateSubscriptionTx(ctx, tx, snap.ID, ev); err != nil {
			return err
		}
		applied.Status = SubscriptionActive
	case payments.EffectMarkSubscriptionFailed:
		reason := strings.TrimSpace(ev.Reason)
		if reason == "" {
			reason = "payment failed"
		}
		if _, err := tx.Exec(ctx, `
            UPDATE rider_partner_subscriptions
            SET payment_status = 'failed', payment_failure_reason = $2, updated_at = NOW()
            WHERE id = $1`, snap.ID, reason); err != nil {
			return fmt.Errorf("mark subscription payment failed: %w", err)
		}
		applied.Status = payments.SubscriptionPaymentFailed
	}

	if d.Outcome == payments.OutcomeMismatch {
		var paymentID, rideID, subscriptionID *uuid.UUID
		if snap.Kind == payments.TargetRide && snap.ID != uuid.Nil {
			id := snap.ID
			paymentID = &id
		}
		if snap.Kind == payments.TargetSubscription {
			id := snap.ID
			subscriptionID = &id
		} else {
			id := snap.RideID
			rideID = &id
		}
		observed := ev.EventType
		if ev.Status != "" {
			observed += ":" + ev.Status
		}
		detail := d.Detail
		if _, err := tx.Exec(ctx, `
            INSERT INTO rider_payment_reconciliation (ride_id, subscription_id, payment_id, observed_status, canonical_status, next_retry_at, terminal_reason, updated_at)
            VALUES ($1, $2, $3, $4, 'reconciliation_required', NOW(), $5, NOW())`,
			rideID, subscriptionID, paymentID, observed, detail); err != nil {
			return fmt.Errorf("record payment reconciliation: %w", err)
		}
	}
	return recordInboxOutcome(ctx, tx, ev.EventID, d)
}

// activateSubscriptionTx applies the signed capture to a checkout row: paid,
// status active, the period starting now for a first purchase, or from the
// renewed row's expires_at (never reset) for a renewal, whose old row is
// superseded (cancelled with a reason) so the expiry worker reminds about
// the new period only.
func activateSubscriptionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, ev payments.Event) error {
	var partnerID uuid.UUID
	var renews *uuid.UUID
	var days int
	if err := tx.QueryRow(ctx, `
        SELECT s.partner_id, s.renews_subscription_id, p.billing_period_days
        FROM rider_partner_subscriptions s JOIN rider_subscription_plans p ON p.id = s.plan_id
        WHERE s.id = $1`, id).Scan(&partnerID, &renews, &days); err != nil {
		return fmt.Errorf("read checkout: %w", err)
	}
	start := time.Now().UTC()
	if renews != nil {
		var oldExpiry time.Time
		var oldStatus string
		err := tx.QueryRow(ctx, `
            SELECT expires_at, status::text FROM rider_partner_subscriptions
            WHERE id = $1 AND partner_id = $2 FOR UPDATE`, *renews, partnerID).Scan(&oldExpiry, &oldStatus)
		switch {
		case err == nil:
			if oldExpiry.After(start) && (oldStatus == "active" || oldStatus == "trial" || oldStatus == "grace_period") {
				start = oldExpiry
			}
			if _, err := tx.Exec(ctx, `
                UPDATE rider_partner_subscriptions
                SET status = 'cancelled', cancelled_at = NOW(), cancellation_reason = $2, updated_at = NOW()
                WHERE id = $1 AND status IN ('active','trial','grace_period')`, *renews, "superseded_by_renewal:"+id.String()); err != nil {
				return fmt.Errorf("supersede renewed subscription: %w", err)
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("read renewed subscription: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
        UPDATE rider_partner_subscriptions
        SET status = 'active', starts_at = $2, expires_at = $3, grace_ends_at = NULL, payment_status = 'paid', paid_at = NOW(),
            payment_failure_reason = NULL, provider_reference = COALESCE(NULLIF($4, ''), provider_reference),
            intent_id = COALESCE(intent_id, NULLIF($5, '')::uuid), updated_at = NOW()
        WHERE id = $1`, id, start, start.AddDate(0, 0, days), ev.ProviderRef, ev.IntentID); err != nil {
		return fmt.Errorf("activate subscription: %w", err)
	}
	return nil
}

func recordInboxOutcome(ctx context.Context, tx pgx.Tx, eventID string, d payments.Decision) error {
	if _, err := tx.Exec(ctx, `UPDATE rider_payment_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1`,
		eventID, string(d.Outcome), d.Detail); err != nil {
		return fmt.Errorf("record payment outcome: %w", err)
	}
	return nil
}

// PaymentInboxOutcome reads the recorded outcome of an event (tests, ops).
func (s *Store) PaymentInboxOutcome(ctx context.Context, eventID string) (string, error) {
	var out *string
	if err := s.db.QueryRow(ctx, `SELECT outcome FROM rider_payment_inbox WHERE event_id = $1`, eventID).Scan(&out); err != nil {
		return "", err
	}
	if out == nil {
		return "", nil
	}
	return *out, nil
}

// CountReconciliationRequired counts open reconciliation rows for a ride
// (tests, ops).
func (s *Store) CountReconciliationRequired(ctx context.Context, rideID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM rider_payment_reconciliation
        WHERE ride_id = $1 AND canonical_status = 'reconciliation_required'`, rideID).Scan(&n)
	return n, err
}
