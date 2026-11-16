//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// Every payment mismatch against a real database (migration 055): the money
// outcome is deterministic, recorded in order_owed_captures, and the buyer's
// payment_notice says so.

type owedRow struct {
	reason, via, status string
	amount, due, refund int64
	exact               bool
	refundedAt          *time.Time
}

func readOwed(t *testing.T, orderID uuid.UUID, intent string) owedRow {
	t.Helper()
	var r owedRow
	if err := testPool.QueryRow(context.Background(), `
		SELECT reason, refund_via, refund_status, amount_minor, refund_due_minor, refunded_minor, amount_exact, refunded_at
		  FROM order_owed_captures WHERE order_id = $1 AND intent_id = $2`, orderID, normIntent(intent)).
		Scan(&r.reason, &r.via, &r.status, &r.amount, &r.due, &r.refund, &r.exact, &r.refundedAt); err != nil {
		t.Fatalf("read owed capture of %s: %v", intent, err)
	}
	return r
}

func count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func mustNotice(t *testing.T, s *Store, orderID uuid.UUID) *PaymentNotice {
	t.Helper()
	n, err := s.PaymentNoticeForOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("payment notice: %v", err)
	}
	if n == nil {
		t.Fatal("no payment notice")
	}
	return n
}

// paidBoundOrder is a paid order whose payment landed on intent.
func paidBoundOrder(t *testing.T) (*fixture, *CheckoutResult, string) {
	t.Helper()
	f, res, intent := pendingOrder(t)
	if err := f.store.ApplyPaymentSucceeded(context.Background(), capture(f, res, intent)); err != nil {
		t.Fatalf("own capture: %v", err)
	}
	return f, res, intent
}

// Defect 1 + 2: a bank-offer duplicate shows the money paid, and its refund
// — on payments' ledger, announced by payment.owed_capture — is recorded.
func TestOwed_DuplicateShowsCapturedMoneyAndRefundStatus(t *testing.T) {
	ctx := context.Background()
	f, res, _ := paidBoundOrder(t)
	dupIntent := uuid.NewString()
	dup := capture(f, res, dupIntent)
	paid := res.TotalMinor - 12181
	dup.CapturedMinor = &paid
	if err := f.store.ApplyPaymentSucceeded(ctx, dup); err != nil {
		t.Fatalf("duplicate capture: %v", err)
	}
	if n := count(t, `SELECT count(*) FROM payment_event_inbox WHERE event_id = $1 AND captured_minor = $2`,
		dup.EventID, paid.Int64()); n != 1 {
		t.Fatalf("inbox captured_minor not recorded")
	}
	n := mustNotice(t, f.store, res.OrderID)
	if n.Code != PaymentNoticeDuplicateCapture || n.AmountMinor != paid || n.AmountApproximate || n.RefundStatus != "pending" {
		t.Fatalf("notice = %+v, want duplicate_capture of %d (the money), pending", n, paid)
	}

	owed := OwedCaptureEvent{
		EventID: "evt-owed-" + uuid.NewString(), OrderID: res.OrderID, IntentID: dupIntent, RefundRequiredID: 9001,
		Reason: OwedReasonDuplicateOrderCapture, AmountMinor: paid.Int64(), Currency: "INR",
	}
	if err := f.store.ApplyOwedCapture(ctx, owed); err != nil {
		t.Fatalf("owed capture recorded: %v", err)
	}
	if err := f.store.ApplyOwedCapture(ctx, owed); !errors.Is(err, ErrDuplicatePaymentEvt) {
		t.Fatalf("redelivered owed capture: %v, want ErrDuplicatePaymentEvt", err)
	}
	done := owed
	done.EventID, done.RefundedMinor, done.Resolved = "evt-owed-"+uuid.NewString(), paid.Int64(), true
	if err := f.store.ApplyOwedCapture(ctx, done); err != nil {
		t.Fatalf("owed capture refunded: %v", err)
	}
	n = mustNotice(t, f.store, res.OrderID)
	if n.RefundStatus != "refunded" || n.RefundedAt == nil || n.RefundedAtEpoch == nil || n.Payments != 1 || n.AmountMinor != paid {
		t.Fatalf("notice after the refund = %+v, want refunded with refunded_at", n)
	}
	if s := readCaptureState(t, res.OrderID); s.payStatus != "paid" || s.commands != 0 {
		t.Fatalf("the duplicate's refund touched the order: %+v", s)
	}
}

// Defect 2: a payment.refunded naming the duplicate's intent is booked to the
// duplicate, not the order's ledger; an intent nobody owes is still refused.
func TestOwed_RefundOfDuplicateIntentRecordedUnknownIntentRefused(t *testing.T) {
	ctx := context.Background()
	f, res, _ := paidBoundOrder(t)
	dupIntent := uuid.NewString()
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, dupIntent)); err != nil {
		t.Fatalf("duplicate capture: %v", err)
	}
	ret := res.TotalMinor.Int64()
	refund := RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: dupIntent,
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: res.TotalMinor, ProviderStatus: "refunded", ReturnedMinor: &ret,
	}
	out, err := f.store.SettleRefund(ctx, refund)
	if err != nil {
		t.Fatalf("refund of the duplicate: %v (it used to be refused as an intent mismatch)", err)
	}
	if out.PaymentStatus != "paid" {
		t.Fatalf("settlement reports payment_status %s, want the order still paid", out.PaymentStatus)
	}
	if n := count(t, `SELECT count(*) FROM order_refund_settlements WHERE order_id = $1`, res.OrderID); n != 0 {
		t.Fatalf("the duplicate's refund reached the order's ledger (%d rows)", n)
	}
	if r := readOwed(t, res.OrderID, dupIntent); r.status != OwedRefundRefunded || r.refund != ret || r.refundedAt == nil {
		t.Fatalf("owed row = %+v, want refunded", r)
	}
	// The same provider refund under another event id: counted once.
	again := refund
	again.EventID = "evt-rf-" + uuid.NewString()
	if out, err := f.store.SettleRefund(ctx, again); err != nil || !out.Duplicate {
		t.Fatalf("same refund again: %+v, %v; want a duplicate", out, err)
	}

	unknown := RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: uuid.NewString(),
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: money.Paise(100),
	}
	if _, err := f.store.SettleRefund(ctx, unknown); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("refund of an unknown intent: %v, want ErrAmountMismatch", err)
	}
}

// Defect 3: money landed on a payment_failed order (the provider captured the
// same intent after its failed attempt). The retry is refused, the intent
// stays bound, and the refund settles against it.
func TestOwed_RetryAfterLateCaptureOnFailedOrderIsRefused(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)
	failed := capture(f, res, intent)
	failed.EventType = "payment.failed"
	if err := f.store.ApplyPaymentFailed(ctx, failed); err != nil {
		t.Fatalf("payment failed: %v", err)
	}
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, intent)); err != nil {
		t.Fatalf("late capture on the failed order: %v", err)
	}
	if s := readCaptureState(t, res.OrderID); s.payStatus != "refund_pending" || s.commands != 1 || !s.lateMarker {
		t.Fatalf("after the late capture: %+v", s)
	}
	if _, err := f.store.RetryPaymentReservation(ctx, res.OrderID, f.userID); !errors.Is(err, ErrPaymentAlreadyCaptured) {
		t.Fatalf("retry after a capture landed: %v, want ErrPaymentAlreadyCaptured", err)
	}
	if got := boundIntent(t, res.OrderID); got != intent {
		t.Fatalf("bound intent = %q, want the captured intent %s kept", got, intent)
	}
	n := mustNotice(t, f.store, res.OrderID)
	if n.Code != PaymentNoticeLateCapture || n.RefundStatus != "pending" || n.AmountMinor != res.TotalMinor {
		t.Fatalf("notice = %+v, want late_capture pending", n)
	}
	if _, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: intent,
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: res.TotalMinor, ProviderStatus: "refunded",
	}); err != nil {
		t.Fatalf("the late capture's refund: %v", err)
	}
	if n := mustNotice(t, f.store, res.OrderID); n.RefundStatus != "refunded" || n.RefundedAt == nil {
		t.Fatalf("notice after the refund = %+v, want refunded", n)
	}
}

// A failed payment with no money on it is still retryable.
func TestOwed_RetryAfterPlainFailureStillWorks(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)
	failed := capture(f, res, intent)
	failed.EventType = "payment.failed"
	if err := f.store.ApplyPaymentFailed(ctx, failed); err != nil {
		t.Fatalf("payment failed: %v", err)
	}
	if _, err := f.store.RetryPaymentReservation(ctx, res.OrderID, f.userID); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

// Case: amount mismatch on the intent's first money. Refunded through an
// owed_capture command that the order's ledger never counts; the order is
// unchanged and the event is acknowledged, not dead-lettered.
func TestOwed_CaptureMismatchIsRefundedNotDeadLettered(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)
	wrong := capture(f, res, intent)
	wrong.AmountMinor = res.TotalMinor + 100
	if err := f.store.ApplyPaymentSucceeded(ctx, wrong); err != nil {
		t.Fatalf("mismatched capture: %v, want it acknowledged and refunded", err)
	}
	s := readCaptureState(t, res.OrderID)
	if s.payStatus == "paid" || s.lateMarker {
		t.Fatalf("a mismatched capture moved the order: %+v", s)
	}
	if n := count(t, `SELECT count(*) FROM order_refund_commands
	                   WHERE order_id = $1 AND scope = 'owed_capture' AND amount_minor = $2 AND intent_id = $3`,
		res.OrderID, wrong.AmountMinor.Int64(), intent); n != 1 {
		t.Fatalf("owed_capture refund commands = %d, want 1", n)
	}
	if r := readOwed(t, res.OrderID, intent); r.reason != OwedReasonCaptureMismatch || r.via != refundViaOwedCommand ||
		r.status != OwedRefundPending || r.due != wrong.AmountMinor.Int64() || !r.exact {
		t.Fatalf("owed row = %+v", r)
	}
	if n := mustNotice(t, f.store, res.OrderID); n.Code != PaymentNoticeMismatchedCapture || n.AmountApproximate || n.AmountMinor != wrong.AmountMinor {
		t.Fatalf("notice = %+v, want mismatched_capture of the money payments verified", n)
	}
	// A cancel of the still-unpaid order owes nothing for it: the owed
	// refund is not the order's.
	settled, owedSum, err := func() (money.Paise, money.Paise, error) {
		tx, err := testPool.Begin(ctx)
		if err != nil {
			return 0, 0, err
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		return orderRefundedAndOwedTx(ctx, tx, res.OrderID)
	}()
	if err != nil || settled != 0 || owedSum != 0 {
		t.Fatalf("order ledger sees settled=%d owed=%d (%v), want nothing", settled, owedSum, err)
	}
	// The refund comes back on that intent, for more than the order total:
	// it settles the owed payment and its command, never the order.
	if _, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: intent,
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: wrong.AmountMinor, ProviderStatus: "refunded",
	}); err != nil {
		t.Fatalf("refund of the mismatched capture: %v", err)
	}
	if n := count(t, `SELECT count(*) FROM order_refund_commands
	                   WHERE order_id = $1 AND scope = 'owed_capture' AND status = 'succeeded'`, res.OrderID); n != 1 {
		t.Fatalf("owed_capture command not settled")
	}
	if n := count(t, `SELECT count(*) FROM order_refund_settlements WHERE order_id = $1`, res.OrderID); n != 0 {
		t.Fatalf("the owed refund reached the order's ledger")
	}
	if n := mustNotice(t, f.store, res.OrderID); n.RefundStatus != "refunded" {
		t.Fatalf("notice = %+v, want refunded", n)
	}
	// The order can still be paid properly on another intent.
	other := uuid.NewString()
	mustExec(t, `UPDATE orders SET payment_intent_id = NULL WHERE id = $1`, res.OrderID)
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, other)); err != nil {
		t.Fatalf("correct payment after the refused one: %v", err)
	}
	if s := readCaptureState(t, res.OrderID); s.payStatus != "paid" {
		t.Fatalf("after the correct payment: %+v, want paid", s)
	}
}

// Case: a refused capture on an intent that pays ANOTHER order is never
// refunded from here: poison, the order unchanged.
func TestOwed_MismatchOnAnotherOrdersIntentStaysPoison(t *testing.T) {
	ctx := context.Background()
	f, res, _ := pendingOrder(t)
	_, _, otherIntent := pendingOrder(t)
	wrong := capture(f, res, otherIntent)
	wrong.Currency = "USD"
	if err := f.store.ApplyPaymentSucceeded(ctx, wrong); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("mismatch on another order's intent: %v, want ErrAmountMismatch", err)
	}
	if n := count(t, `SELECT count(*) FROM order_owed_captures WHERE order_id = $1`, res.OrderID); n != 0 {
		t.Fatalf("recorded %d owed rows for another order's money", n)
	}
}

// Cases: payments-side late capture on a failed intent, its refund parked
// (payment.refund_failed), then refunded.
func TestOwed_PaymentsLateCaptureParkedThenRefunded(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)
	rowID := time.Now().UnixNano() % 1_000_000_000
	ev := OwedCaptureEvent{
		EventID: "evt-owed-" + uuid.NewString(), OrderID: res.OrderID, IntentID: intent, RefundRequiredID: rowID,
		Reason: OwedReasonLateCaptureAfterFailed, AmountMinor: 4200, Currency: "INR",
	}
	if err := f.store.ApplyOwedCapture(ctx, ev); err != nil {
		t.Fatalf("owed capture: %v", err)
	}
	if n := mustNotice(t, f.store, res.OrderID); n.Code != PaymentNoticeLateCapture || n.AmountMinor != 4200 || n.RefundStatus != "pending" {
		t.Fatalf("notice = %+v", n)
	}
	if err := f.store.ApplyRefundFailed(ctx, RefundFailedEvent{
		EventID: "evt-rff-" + uuid.NewString(), ReferenceType: "refund_required",
		ReferenceID: itoa(rowID), IntentID: intent, AmountMinor: 4200, ReasonCode: "provider_refused",
	}); err != nil {
		t.Fatalf("refund failed: %v", err)
	}
	if n := mustNotice(t, f.store, res.OrderID); n.RefundStatus != "failed" {
		t.Fatalf("notice after the park = %+v, want failed", n)
	}
	done := ev
	done.EventID, done.RefundedMinor, done.Resolved = "evt-owed-"+uuid.NewString(), 4200, true
	if err := f.store.ApplyOwedCapture(ctx, done); err != nil {
		t.Fatalf("owed refunded: %v", err)
	}
	if n := mustNotice(t, f.store, res.OrderID); n.RefundStatus != "refunded" {
		t.Fatalf("notice after the refund = %+v, want refunded", n)
	}
	// An owed payment for an order commerce does not have is poison.
	bad := ev
	bad.EventID, bad.OrderID = "evt-owed-"+uuid.NewString(), uuid.New()
	if err := f.store.ApplyOwedCapture(ctx, bad); !errors.Is(err, ErrOrderNotFoundP0) {
		t.Fatalf("unknown order: %v, want ErrOrderNotFoundP0", err)
	}
}

// Case: the order's own refund stalls (worker refused, or payments parked).
func TestOwed_OrderRefundStalledIsRefundDelayed(t *testing.T) {
	ctx := context.Background()
	f, res, intent := paidBoundOrder(t)
	if err := f.store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "changed mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n, err := f.store.PaymentNoticeForOrder(ctx, res.OrderID); err != nil || n != nil {
		t.Fatalf("a refund on its way is not a notice: %+v, %v", n, err)
	}
	if err := f.store.ApplyRefundFailed(ctx, RefundFailedEvent{
		EventID: "evt-rff-" + uuid.NewString(), ReferenceType: "order", ReferenceID: res.OrderID.String(),
		IntentID: intent, AmountMinor: res.TotalMinor.Int64(), ReasonCode: "provider_refused",
	}); err != nil {
		t.Fatalf("refund failed: %v", err)
	}
	n := mustNotice(t, f.store, res.OrderID)
	if n.Code != PaymentNoticeRefundDelayed || n.RefundStatus != "failed" || n.AmountMinor != res.TotalMinor {
		t.Fatalf("notice = %+v, want refund_delayed failed for %d", n, res.TotalMinor)
	}
	if _, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, IntentID: intent,
		RefundID: "rfnd_" + uuid.NewString(), AmountMinor: res.TotalMinor, ProviderStatus: "refunded",
	}); err != nil {
		t.Fatalf("refund lands: %v", err)
	}
	if n, err := f.store.PaymentNoticeForOrder(ctx, res.OrderID); err != nil || n != nil {
		t.Fatalf("after the refund landed: %+v, %v; want no notice", n, err)
	}
}

// Case: the worker parks a late capture's refund; the buyer sees failed.
func TestOwed_LateCaptureRefundRefusedIsFailed(t *testing.T) {
	ctx := context.Background()
	f, res, intent := expiredOrder(t)
	if err := f.store.ApplyPaymentSucceeded(ctx, capture(f, res, intent)); err != nil {
		t.Fatalf("late capture: %v", err)
	}
	var cmd uuid.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM order_refund_commands WHERE order_id = $1`, res.OrderID).Scan(&cmd); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkRefundFailed(ctx, cmd, "refused", true); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if n := mustNotice(t, f.store, res.OrderID); n.Code != PaymentNoticeLateCapture || n.RefundStatus != "failed" {
		t.Fatalf("notice = %+v, want late_capture failed", n)
	}
}

func itoa(v int64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{digits[v%10]}, b...)
		v /= 10
	}
	return string(b)
}
