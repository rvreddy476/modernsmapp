package postgres

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

func paise(v int64) *money.Paise { p := money.Paise(v); return &p }

// Defect 1: what the buyer paid is the captured money, never the order value,
// when payments states it; the stated value stands in only as inexact.
func TestCapturedMoney(t *testing.T) {
	offer := &PaymentOffer{OfferID: uuid.NewString(), DiscountMinor: 12181, CapturedMinor: 109629}
	for _, tc := range []struct {
		name  string
		e     PaymentEvent
		want  int64
		exact bool
	}{
		{"captured_minor on a bank-offer capture", PaymentEvent{AmountMinor: 121810, CapturedMinor: paise(109629)}, 109629, true},
		{"captured_minor equal to the order value", PaymentEvent{AmountMinor: 121810, CapturedMinor: paise(121810)}, 121810, true},
		{"offer figures that reconcile, no captured_minor", PaymentEvent{AmountMinor: 121810, Offer: offer}, 109629, true},
		{"a plain capture: amount_minor is the money (payments' B2)", PaymentEvent{AmountMinor: 121810}, 121810, true},
		{"captured above the order value is not money we trust", PaymentEvent{AmountMinor: 100, CapturedMinor: paise(101)}, 100, false},
		{"zero captured is not money we trust", PaymentEvent{AmountMinor: 100, CapturedMinor: paise(0)}, 100, false},
		{"offer figures that do not reconcile", PaymentEvent{AmountMinor: 121810,
			Offer: &PaymentOffer{OfferID: uuid.NewString(), DiscountMinor: 1, CapturedMinor: 109629}}, 121810, false},
		{"negative stated amount floors at zero", PaymentEvent{AmountMinor: -5}, 0, false},
	} {
		got, exact := capturedMoney(tc.e)
		if got != tc.want || exact != tc.exact {
			t.Errorf("%s: (%d, %v), want (%d, %v)", tc.name, got, exact, tc.want, tc.exact)
		}
	}
}

func TestOwedKeys(t *testing.T) {
	order := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	if got := owedKeyForIntent("  ABC-Def "); got != "intent:abc-def" {
		t.Fatalf("intent key = %q", got)
	}
	if got := owedKeyForCapture(order, " "); got != "late-capture:"+order.String() {
		t.Fatalf("blank-intent capture key = %q", got)
	}
	if got := owedKeyForCapture(order, "X"); got != "intent:x" {
		t.Fatalf("capture key = %q", got)
	}
	// A duplicate order capture is the row commerce wrote when it reported
	// it; everything else payments records on its own row.
	if got := owedKeyForPaymentsRow(OwedReasonDuplicateOrderCapture, "Dup", 7); got != "intent:dup" {
		t.Fatalf("duplicate order capture key = %q", got)
	}
	for _, r := range []string{OwedReasonRepeatCapture, OwedReasonLateCaptureAfterFailed, OwedReasonCaptureMismatch} {
		if got := owedKeyForPaymentsRow(r, "Dup", 7); got != "refund_required:7" {
			t.Fatalf("%s key = %q", r, got)
		}
	}
	if got := owedKeyForPaymentsRow(OwedReasonDuplicateOrderCapture, "", 9); got != "refund_required:9" {
		t.Fatalf("duplicate without an intent key = %q", got)
	}
}

func TestMergeOwedFromPayments(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	ev := OwedCaptureEvent{AmountMinor: 109629, RefundedMinor: 0}

	// New: payments' money, exact, pending.
	n, ok := mergeOwedFromPayments(nil, ev, now)
	if !ok || n.AmountMinor != 109629 || !n.AmountExact || n.RefundDueMinor != 109629 || n.RefundStatus != OwedRefundPending ||
		n.RefundVia != refundViaPayments || n.RefundedAt != nil {
		t.Fatalf("new = %+v", n)
	}

	// Commerce's stated (inexact) duplicate is replaced by payments' money.
	cur := &owedState{AmountMinor: 121810, AmountExact: false, RefundVia: refundViaPayments, RefundDueMinor: 121810,
		RefundStatus: OwedRefundPending}
	n, _ = mergeOwedFromPayments(cur, ev, now)
	if n.AmountMinor != 109629 || !n.AmountExact {
		t.Fatalf("payments' figure did not replace the stated one: %+v", n)
	}

	// Resolved: refunded, stamped once.
	done := ev
	done.RefundedMinor, done.Resolved = 109629, true
	n, _ = mergeOwedFromPayments(cur, done, now)
	if n.RefundStatus != OwedRefundRefunded || n.RefundedAt == nil || !n.RefundedAt.Equal(now) {
		t.Fatalf("resolved = %+v", n)
	}
	earlier := now.Add(-time.Hour)
	n2, _ := mergeOwedFromPayments(&owedState{RefundVia: refundViaPayments, RefundedMinor: 109629,
		RefundStatus: OwedRefundRefunded, RefundedAt: &earlier}, done, now)
	if !n2.RefundedAt.Equal(earlier) {
		t.Fatalf("a redelivered refund moved refunded_at: %v", n2.RefundedAt)
	}

	// Out of order: an older "refund_required" after the refund never
	// takes the refund back.
	n, _ = mergeOwedFromPayments(&owedState{RefundVia: refundViaPayments, RefundDueMinor: 109629, RefundedMinor: 109629,
		RefundStatus: OwedRefundRefunded, RefundedAt: &earlier}, ev, now)
	if n.RefundedMinor != 109629 || n.RefundStatus != OwedRefundRefunded {
		t.Fatalf("stale event undid a refund: %+v", n)
	}

	// Failed stays failed until money moves again.
	failed := &owedState{RefundVia: refundViaPayments, RefundDueMinor: 109629, RefundStatus: OwedRefundFailed}
	if n, _ = mergeOwedFromPayments(failed, ev, now); n.RefundStatus != OwedRefundFailed {
		t.Fatalf("failed reset without money moving: %+v", n)
	}
	part := ev
	part.RefundedMinor = 50000
	if n, _ = mergeOwedFromPayments(failed, part, now); n.RefundStatus != OwedRefundPending || n.RefundedMinor != 50000 {
		t.Fatalf("partial refund after a failure: %+v, want pending", n)
	}

	// A row commerce refunds itself is not payments' to change.
	own := &owedState{RefundVia: refundViaOwedCommand, AmountMinor: 1, RefundStatus: OwedRefundPending}
	if n, ok = mergeOwedFromPayments(own, done, now); ok || n.RefundStatus != OwedRefundPending || n.AmountMinor != 1 {
		t.Fatalf("payments overwrote a commerce-refunded row: %+v ok=%v", n, ok)
	}
}

// Defect 2: a refund naming the duplicate's intent settles the owed payment,
// and only a payment the order did not keep is routed there.
func TestRoutesToOwed(t *testing.T) {
	bound, dup := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name string
		via  string
		w    string
		want bool
	}{
		{"duplicate order capture on another intent", refundViaPayments, dup, true},
		{"a second payment on the order's own intent is the order's refund", refundViaPayments, bound, false},
		{"refused capture, even on the bound intent", refundViaOwedCommand, bound, true},
		{"refused capture on another intent", refundViaOwedCommand, dup, true},
		{"late capture: the order's own ledger", refundViaOrder, dup, false},
		{"payments row without an intent", refundViaPayments, "", false},
	} {
		w := owedRefundTarget{IntentID: tc.w, owedState: owedState{RefundVia: tc.via}}
		if got := routesToOwed(w, " "+bound+" "); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOwedRefundUnit(t *testing.T) {
	ret := int64(109629)
	ev := RefundEvent{AmountMinor: 121810, ReturnedMinor: &ret}
	if got := owedRefundUnit(refundViaPayments, ev); got != 109629 {
		t.Fatalf("payments path counts money: %d", got)
	}
	if got := owedRefundUnit(refundViaOwedCommand, ev); got != 121810 {
		t.Fatalf("command path counts the order value it asked for: %d", got)
	}
	if got := owedRefundUnit(refundViaPayments, RefundEvent{AmountMinor: 500}); got != 500 {
		t.Fatalf("no amount_returned_minor: %d", got)
	}
}

func TestCreditOwedRefund(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	st := owedState{RefundDueMinor: 1000, RefundStatus: OwedRefundPending}
	half, err := creditOwedRefund(st, 400, now)
	if err != nil || half.RefundedMinor != 400 || half.RefundStatus != OwedRefundPending || half.RefundedAt != nil {
		t.Fatalf("partial = %+v, %v", half, err)
	}
	full, err := creditOwedRefund(half, 600, now)
	if err != nil || full.RefundStatus != OwedRefundRefunded || full.RefundedAt == nil {
		t.Fatalf("full = %+v, %v", full, err)
	}
	if _, err := creditOwedRefund(half, 601, now); !errors.Is(err, paymentevents.ErrMismatch) {
		t.Fatalf("over-refund: %v, want a mismatch", err)
	}
	if _, err := creditOwedRefund(st, 0, now); !errors.Is(err, paymentevents.ErrMismatch) {
		t.Fatalf("zero refund: %v, want a mismatch", err)
	}
	failed := owedState{RefundDueMinor: 1000, RefundStatus: OwedRefundFailed}
	if again, _ := creditOwedRefund(failed, 1, now); again.RefundStatus != OwedRefundPending {
		t.Fatalf("money moving after a failure: %+v, want pending", again)
	}
}

// Case: amount/currency/payer mismatch. Refunded as an owed payment only when
// it is the intent's first money and a refund can name it.
func TestRefusedCaptureRefundable(t *testing.T) {
	intent := uuid.NewString()
	ok := PaymentEvent{IntentID: intent, AmountMinor: 100}
	for _, tc := range []struct {
		name string
		v    captureVerdict
		e    PaymentEvent
		want bool
	}{
		{"first money on a payable order", captureApplies, ok, true},
		{"first money on a dead order", captureLate, ok, true},
		{"a redelivery of money already counted", captureRedelivered, ok, false},
		{"a duplicate goes to payments, never here", captureDuplicate, ok, false},
		{"no intent to refund", captureApplies, PaymentEvent{AmountMinor: 100}, false},
		{"not a payments intent", captureApplies, PaymentEvent{IntentID: "pay_x", AmountMinor: 100}, false},
		{"nothing to refund", captureApplies, PaymentEvent{IntentID: intent, AmountMinor: 0}, false},
	} {
		if got := refusedCaptureRefundable(tc.v, tc.e); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMismatchField(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &paymentevents.MismatchError{Field: paymentevents.FieldCurrency, Detail: "x"})
	if got := mismatchField(err); got != "currency" {
		t.Fatalf("field = %q", got)
	}
	if got := mismatchField(errors.New("plain")); got != "" {
		t.Fatalf("plain error field = %q", got)
	}
}

// Defect 3: a payment_failed order on which money landed is never retried.
func TestRetryRefusedForCapture(t *testing.T) {
	for _, tc := range []struct {
		pay  string
		late bool
		want bool
	}{
		{"failed", false, false},
		{"pending", false, false},
		{"refund_pending", true, true},
		{"refunded", true, true},
		{"refund_pending", false, true},
		{"paid", false, true},
		{"failed", true, true},
	} {
		if got := retryRefusedForCapture(tc.pay, tc.late); got != tc.want {
			t.Errorf("payment_status %s late=%v: %v, want %v", tc.pay, tc.late, got, tc.want)
		}
	}
	if !errors.Is(ErrPaymentAlreadyCaptured, ErrPaymentNotRetryable) {
		t.Fatal("ErrPaymentAlreadyCaptured must still map as ErrPaymentNotRetryable")
	}
}
