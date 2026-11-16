package postgres

// The buyer-facing payment notice: what the buyer's order screen and order
// list say when the buyer paid money the order did not keep, or a refund owed
// to them has stalled. It is how every payment mismatch reaches the buyer; the
// money outcome of each is deterministic (the order keeps the payment, or the
// payment is refunded) and is decided where the payment lands
// (owed_captures.go, p0.go).
//
// Codes. Stable: clients switch on them, and an unknown code must be shown
// generically ("we are returning a payment to you"), never hidden.
//
//	duplicate_capture   The order was paid, then paid again on another
//	                    payment attempt. The extra payment is refunded by
//	                    payments-service; the order is unchanged.
//	repeat_capture      The same payment attempt was charged twice (a second
//	                    provider payment on one intent). The second charge is
//	                    refunded by payments-service; the order is unchanged.
//	late_capture        Money arrived after the order could no longer take it
//	                    (expired, cancelled, or its payment had already been
//	                    finalised as failed). Not confirmed: refunded in full.
//	mismatched_capture  A payment whose amount, currency or payer did not match
//	                    the order. Never applied: refunded in full.
//	refund_delayed      A refund of the order itself (a cancellation, say) was
//	                    parked by the payment provider and is with a person.
//
// Every notice carries refund_status: "pending" (being refunded), "refunded"
// (returned; refunded_at says when) or "failed" (the automatic refund stalled
// and support is resolving it; the money is still owed and will be returned).
//
// amount_minor is the MONEY the buyer paid on those payments (the captured
// amount, which is less than the order value on a bank-offer payment). When a
// payment's captured amount was never stated to commerce — only on rows
// recorded before captured amounts were (migration 055) and not yet confirmed
// by payments-service — amount_minor is the order value the payment event
// stated, an upper bound, and amount_approximate is true: show "up to ₹X",
// never a figure as if confirmed. For refund_delayed it is the refund owed.
//
// An order with more than one kind of notice carries the most urgent as
// payment_notice and the rest under its "others" (failed refunds first, then
// mismatched, late, duplicate, repeat, delayed; earliest first).
//
// JSON shape (fields in this order; amount_approximate, refunded_at,
// refunded_at_epoch and others only when set):
//
//	{"code":"duplicate_capture","amount_minor":109629,"currency":"INR","payments":1,
//	 "detected_at":"…","detected_at_epoch":0,"amount_approximate":true,
//	 "refund_status":"refunded","refunded_at":"…","refunded_at_epoch":0,"others":[…]}

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// Payment notice codes. See the list above.
const (
	PaymentNoticeDuplicateCapture  = "duplicate_capture"
	PaymentNoticeRepeatCapture     = "repeat_capture"
	PaymentNoticeLateCapture       = "late_capture"
	PaymentNoticeMismatchedCapture = "mismatched_capture"
	PaymentNoticeRefundDelayed     = "refund_delayed"
)

// PaymentNoticeCodes is every code, most urgent kind first.
var PaymentNoticeCodes = []string{
	PaymentNoticeMismatchedCapture, PaymentNoticeLateCapture, PaymentNoticeDuplicateCapture,
	PaymentNoticeRepeatCapture, PaymentNoticeRefundDelayed,
}

// noticeCodeForReason maps an owed payment's reason to the buyer's code.
// An unrecognised reason is still money owed back and is shown as a
// duplicate, the most generic "you paid twice" wording, rather than hidden.
func noticeCodeForReason(reason string) string {
	switch reason {
	case OwedReasonRepeatCapture:
		return PaymentNoticeRepeatCapture
	case OwedReasonLateCapture, OwedReasonLateCaptureAfterFailed:
		return PaymentNoticeLateCapture
	case OwedReasonCaptureMismatch:
		return PaymentNoticeMismatchedCapture
	}
	return PaymentNoticeDuplicateCapture
}

// PaymentNotice is the buyer-facing note on an order's payment. It is nil,
// and omitted from the JSON, when there is nothing to say.
type PaymentNotice struct {
	// Code is the notice kind (PaymentNoticeCodes).
	Code string `json:"code"`
	// AmountMinor is the money concerned, in paise: what the buyer paid on
	// the payments being returned (summed over Payments), or the refund owed
	// for refund_delayed.
	AmountMinor money.Paise `json:"amount_minor"`
	// Currency is the payments' currency.
	Currency string `json:"currency"`
	// Payments is how many payments the notice covers; usually 1.
	Payments int `json:"payments"`
	// DetectedAt is when commerce first caught it.
	DetectedAt time.Time `json:"detected_at"`
	// DetectedAtEpoch is DetectedAt in unix seconds, the form clients parse.
	DetectedAtEpoch int64 `json:"detected_at_epoch"`
	// AmountApproximate: AmountMinor includes a stated order value standing
	// in for money never confirmed; it is an upper bound.
	AmountApproximate bool `json:"amount_approximate,omitempty"`
	// RefundStatus is pending, refunded or failed.
	RefundStatus string `json:"refund_status"`
	// RefundedAt is when the last of the payments came back (refunded only).
	RefundedAt      *time.Time `json:"refunded_at,omitempty"`
	RefundedAtEpoch *int64     `json:"refunded_at_epoch,omitempty"`
	// Others are the order's other notices, when it has more than one kind.
	Others []PaymentNotice `json:"others,omitempty"`
}

// owedNoticeRow is one order_owed_captures row as the notice reads it.
type owedNoticeRow struct {
	OrderID      uuid.UUID
	Reason       string
	AmountMinor  int64
	Exact        bool
	Currency     string
	RefundStatus string
	RefundedAt   *time.Time
	DetectedAt   time.Time
}

// delayedRefundRow is one of the order's own refund commands that stalled.
type delayedRefundRow struct {
	OrderID     uuid.UUID
	AmountMinor int64
	Currency    string
	DetectedAt  time.Time
}

func refundStatusRank(s string) int {
	switch s {
	case OwedRefundFailed:
		return 0
	case OwedRefundPending:
		return 1
	}
	return 2
}

func noticeCodeRank(code string) int {
	for i, c := range PaymentNoticeCodes {
		if c == code {
			return i
		}
	}
	return len(PaymentNoticeCodes)
}

// buildPaymentNotices folds owed payments and stalled refunds into each
// order's notices, most urgent first. It has no I/O.
//
// Rows are one per payment (order_owed_captures is unique per payment), so
// each counts once. Within a code: amounts add up, the earliest detection
// stands, any stalled refund makes it failed, it is refunded only when every
// payment is (refunded_at the last of them), and pending otherwise.
func buildPaymentNotices(owed []owedNoticeRow, delayed []delayedRefundRow) map[uuid.UUID][]PaymentNotice {
	type key struct {
		order uuid.UUID
		code  string
	}
	acc := map[key]*PaymentNotice{}
	allRefunded := map[key]bool{}
	var order []key
	get := func(k key, currency string, at time.Time) *PaymentNotice {
		n := acc[k]
		if n == nil {
			n = &PaymentNotice{Code: k.code, Currency: coalesceCurrency(currency), DetectedAt: at}
			acc[k] = n
			allRefunded[k] = true
			order = append(order, k)
		}
		if at.Before(n.DetectedAt) {
			n.DetectedAt = at
		}
		return n
	}
	for _, r := range owed {
		k := key{r.OrderID, noticeCodeForReason(r.Reason)}
		n := get(k, r.Currency, r.DetectedAt)
		n.Payments++
		if r.AmountMinor > 0 {
			n.AmountMinor = n.AmountMinor.Add(money.Paise(r.AmountMinor))
		}
		if !r.Exact {
			n.AmountApproximate = true
		}
		switch r.RefundStatus {
		case OwedRefundRefunded:
			if r.RefundedAt != nil && (n.RefundedAt == nil || r.RefundedAt.After(*n.RefundedAt)) {
				t := *r.RefundedAt
				n.RefundedAt = &t
			}
		case OwedRefundFailed:
			n.RefundStatus = OwedRefundFailed
			allRefunded[k] = false
		default:
			allRefunded[k] = false
		}
	}
	for _, r := range delayed {
		k := key{r.OrderID, PaymentNoticeRefundDelayed}
		n := get(k, r.Currency, r.DetectedAt)
		n.Payments = 1 // the order's own payment
		if r.AmountMinor > 0 {
			n.AmountMinor = n.AmountMinor.Add(money.Paise(r.AmountMinor))
		}
		n.RefundStatus = OwedRefundFailed
		allRefunded[k] = false
	}

	out := map[uuid.UUID][]PaymentNotice{}
	for _, k := range order {
		n := acc[k]
		switch {
		case n.RefundStatus == OwedRefundFailed:
			n.RefundedAt = nil
		case allRefunded[k]:
			n.RefundStatus = OwedRefundRefunded
		default:
			n.RefundStatus = OwedRefundPending
			n.RefundedAt = nil
		}
		n.DetectedAt = n.DetectedAt.UTC()
		n.DetectedAtEpoch = n.DetectedAt.Unix()
		if n.RefundedAt != nil {
			t := n.RefundedAt.UTC()
			e := t.Unix()
			n.RefundedAt, n.RefundedAtEpoch = &t, &e
		}
		out[k.order] = append(out[k.order], *n)
	}
	for id, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if ra, rb := refundStatusRank(a.RefundStatus), refundStatusRank(b.RefundStatus); ra != rb {
				return ra < rb
			}
			if ca, cb := noticeCodeRank(a.Code), noticeCodeRank(b.Code); ca != cb {
				return ca < cb
			}
			return a.DetectedAt.Before(b.DetectedAt)
		})
		out[id] = list
	}
	return out
}

// primaryNotice is what an order shows: its most urgent notice, carrying the
// rest under Others. nil for none.
func primaryNotice(list []PaymentNotice) *PaymentNotice {
	if len(list) == 0 {
		return nil
	}
	p := list[0]
	if len(list) > 1 {
		p.Others = append([]PaymentNotice(nil), list[1:]...)
	}
	return &p
}

func coalesceCurrency(c string) string {
	if c = strings.TrimSpace(c); c != "" {
		return c
	}
	return "INR"
}

// PaymentNoticesForOrders returns the payment notice of each order that has
// one; orders with nothing to say are absent from the map. Two queries for
// the whole page.
func (s *Store) PaymentNoticesForOrders(ctx context.Context, orderIDs []uuid.UUID) (map[uuid.UUID]*PaymentNotice, error) {
	if len(orderIDs) == 0 {
		return map[uuid.UUID]*PaymentNotice{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT order_id, reason, amount_minor, amount_exact, currency, refund_status, refunded_at, detected_at
		  FROM order_owed_captures
		 WHERE order_id = ANY($1)
		 ORDER BY order_id, detected_at, owed_key`, orderIDs)
	if err != nil {
		return nil, err
	}
	var owed []owedNoticeRow
	for rows.Next() {
		var r owedNoticeRow
		if err := rows.Scan(&r.OrderID, &r.Reason, &r.AmountMinor, &r.Exact, &r.Currency, &r.RefundStatus,
			&r.RefundedAt, &r.DetectedAt); err != nil {
			rows.Close()
			return nil, err
		}
		owed = append(owed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The order's own refunds that stalled: parked by commerce's worker or
	// by payments-service. A late capture's command is the late_capture
	// notice's business, not this one's.
	rows, err = s.db.Query(ctx, `
		SELECT c.order_id, c.amount_minor, COALESCE(o.currency_code, ''),
		       COALESCE(c.provider_failed_at, c.updated_at)
		  FROM order_refund_commands c
		  JOIN orders o ON o.id = c.order_id
		 WHERE c.order_id = ANY($1)
		   AND c.scope = 'order'
		   AND c.status <> 'succeeded'
		   AND (c.status = 'needs_attention' OR c.provider_failed_at IS NOT NULL)
		   AND c.idempotency_key NOT LIKE 'late-capture:%'
		 ORDER BY c.order_id, c.created_at, c.id`, orderIDs)
	if err != nil {
		return nil, err
	}
	var delayed []delayedRefundRow
	for rows.Next() {
		var r delayedRefundRow
		if err := rows.Scan(&r.OrderID, &r.AmountMinor, &r.Currency, &r.DetectedAt); err != nil {
			rows.Close()
			return nil, err
		}
		delayed = append(delayed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := map[uuid.UUID]*PaymentNotice{}
	for id, list := range buildPaymentNotices(owed, delayed) {
		out[id] = primaryNotice(list)
	}
	return out, nil
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
