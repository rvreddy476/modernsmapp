package postgres

// The buyer-facing payment notice: what the buyer's order screen and order
// list say when commerce caught something about the order's money that the
// buyer should hear from us rather than from their bank statement.
//
// The one notice today is a duplicate capture: the buyer paid an order that
// was already paid, on another payment intent (decideCapture's
// captureDuplicate, recordDuplicateCaptureTx). Commerce never books that money
// against the order, so the order's own status, payment_status and refund
// ledger say nothing about it; before this the buyer was never told.
//
// Nothing new is persisted for it. The fact is already durable in
// payment_event_inbox, written in the same transaction as the verdict: a
// payment.succeeded row whose intent is not the order's bound intent. That is
// exactly the duplicate rule, because every capture that is NOT a duplicate
// (applies or late) binds its own intent in that transaction
// (bindCaptureIntentTx) and a redelivery is the bound intent by definition.
// A capture stating no intent is never a duplicate (differentIntent) and is
// excluded the same way. The inbox is indexed on order_id; the outbox row the
// verdict also writes has no index a read path could use.
//
// Refund status is deliberately not reported. payments-service refunds the
// duplicate on its own ledger (refund_required, reason
// duplicate_order_capture); commerce does not record that refund (its
// payment.refunded names an intent that is not the order's, which
// SettleRefund refuses), so commerce cannot say whether it has landed.

import (
	"context"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// PaymentNoticeDuplicateCapture is PaymentNotice.Code for a second payment on
// an order that was already paid. Stable: clients switch on it.
const PaymentNoticeDuplicateCapture = "duplicate_capture"

// PaymentNotice is the buyer-facing note on an order's payment. It is nil,
// and omitted from the JSON, when there is nothing to say.
type PaymentNotice struct {
	// Code is the notice kind; "duplicate_capture" is the only one.
	Code string `json:"code"`
	// AmountMinor is what the extra payments came to, in paise: the money
	// owed back to the buyer. Summed over Payments.
	AmountMinor money.Paise `json:"amount_minor"`
	// Currency is the order's currency.
	Currency string `json:"currency"`
	// Payments is how many extra payments there were (each a distinct
	// payment intent); usually 1.
	Payments int `json:"payments"`
	// DetectedAt is when commerce first caught an extra payment.
	DetectedAt time.Time `json:"detected_at"`
	// DetectedAtEpoch is DetectedAt in unix seconds, the form clients parse.
	DetectedAtEpoch int64 `json:"detected_at_epoch"`
}

// duplicateCaptureRow is one payment_event_inbox row that is a duplicate
// capture of its order.
type duplicateCaptureRow struct {
	OrderID     uuid.UUID
	IntentID    string
	AmountMinor int64
	ProcessedAt time.Time
	Currency    string
}

// buildPaymentNotices folds duplicate-capture rows into one notice per order.
// It has no I/O.
//
// A payment is identified by its intent, compared without case or spacing as
// decideCapture compares it. The same duplicate announced under several event
// ids is one payment (counted, and its amount added, once); the earliest row
// is when it was detected.
func buildPaymentNotices(rows []duplicateCaptureRow) map[uuid.UUID]*PaymentNotice {
	out := map[uuid.UUID]*PaymentNotice{}
	seen := map[uuid.UUID]map[string]bool{}
	for _, r := range rows {
		intent := strings.ToLower(strings.TrimSpace(r.IntentID))
		if intent == "" {
			continue
		}
		n := out[r.OrderID]
		if n == nil {
			n = &PaymentNotice{Code: PaymentNoticeDuplicateCapture, Currency: coalesceCurrency(r.Currency), DetectedAt: r.ProcessedAt}
			out[r.OrderID] = n
			seen[r.OrderID] = map[string]bool{}
		}
		if r.ProcessedAt.Before(n.DetectedAt) {
			n.DetectedAt = r.ProcessedAt
		}
		if seen[r.OrderID][intent] {
			continue
		}
		seen[r.OrderID][intent] = true
		n.Payments++
		if r.AmountMinor > 0 {
			n.AmountMinor = n.AmountMinor.Add(money.Paise(r.AmountMinor))
		}
	}
	for _, n := range out {
		n.DetectedAt = n.DetectedAt.UTC()
		n.DetectedAtEpoch = n.DetectedAt.Unix()
	}
	return out
}

func coalesceCurrency(c string) string {
	if c = strings.TrimSpace(c); c != "" {
		return c
	}
	return "INR"
}

// PaymentNoticesForOrders returns the payment notice of each order that has
// one; orders with nothing to say are absent from the map. One query for the
// whole page.
func (s *Store) PaymentNoticesForOrders(ctx context.Context, orderIDs []uuid.UUID) (map[uuid.UUID]*PaymentNotice, error) {
	if len(orderIDs) == 0 {
		return map[uuid.UUID]*PaymentNotice{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT i.order_id, i.intent_id, COALESCE(i.amount_minor, 0), i.processed_at, o.currency_code
		  FROM payment_event_inbox i
		  JOIN orders o ON o.id = i.order_id
		 WHERE i.order_id = ANY($1)
		   AND i.event_type = $2
		   AND o.payment_intent_id IS NOT NULL
		   AND btrim(COALESCE(i.intent_id, '')) <> ''
		   AND lower(btrim(i.intent_id)) <> lower(o.payment_intent_id::text)
		 ORDER BY i.order_id, i.processed_at, i.event_id`,
		orderIDs, paymentevents.TypeSucceeded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []duplicateCaptureRow
	for rows.Next() {
		var (
			r        duplicateCaptureRow
			currency *string
		)
		if err := rows.Scan(&r.OrderID, &r.IntentID, &r.AmountMinor, &r.ProcessedAt, &currency); err != nil {
			return nil, err
		}
		if currency != nil {
			r.Currency = *currency
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildPaymentNotices(found), nil
}

// PaymentNoticeForOrder is PaymentNoticesForOrders for one order: nil when
// there is nothing to say.
func (s *Store) PaymentNoticeForOrder(ctx context.Context, orderID uuid.UUID) (*PaymentNotice, error) {
	m, err := s.PaymentNoticesForOrders(ctx, []uuid.UUID{orderID})
	if err != nil {
		return nil, err
	}
	return m[orderID], nil
}
