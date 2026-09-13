package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// seedPaymentOrder seeds a 250.00 order (25000 paise) in `status` with one
// food.payments row in `paymentStatus` carrying a payments intent id.
func seedPaymentOrder(t *testing.T, s *Store, status, paymentStatus string) (orderID, customerID uuid.UUID, intentID string) {
	t.Helper()
	ctx := context.Background()
	orderID, _, customerID = seedOrderWithItem(t, s, status)
	intentID = uuid.NewString()
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET payment_status = $2::food.payment_status WHERE id = $1`, orderID, paymentStatus); err != nil {
		t.Fatalf("seed payment status: %v", err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.payments (order_id, payment_method, status, provider, provider_payment_id, amount, currency)
		VALUES ($1, 'ONLINE', $2::food.payment_status, 'payments-service', $3, 250, 'INR')
	`, orderID, paymentStatus, intentID); err != nil {
		t.Fatalf("seed payment row: %v", err)
	}
	return orderID, customerID, intentID
}

func succeeded(orderID, payer uuid.UUID, intentID string) payments.Event {
	return payments.Event{
		EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded,
		IntentID: intentID, OrderID: orderID, PayerID: payer, AmountMinor: 25000, Currency: "INR", Status: "succeeded",
	}
}

func readPaymentState(t *testing.T, s *Store, orderID uuid.UUID) (orderStatus, orderPayment, paymentRow string) {
	t.Helper()
	if err := s.db.QueryRow(context.Background(), `
		SELECT o.status::text, o.payment_status::text,
			COALESCE((SELECT status::text FROM food.payments WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1), '')
		FROM food.orders o WHERE o.id = $1
	`, orderID).Scan(&orderStatus, &orderPayment, &paymentRow); err != nil {
		t.Fatalf("read payment state: %v", err)
	}
	return
}

func readInbox(t *testing.T, s *Store, eventID string) string {
	t.Helper()
	var outcome string
	if err := s.db.QueryRow(context.Background(), `SELECT outcome FROM food.payment_event_inbox WHERE event_id = $1`, eventID).Scan(&outcome); err != nil {
		t.Fatalf("read inbox %s: %v", eventID, err)
	}
	return outcome
}

func countHistoryTo(t *testing.T, s *Store, orderID uuid.UUID, to string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM food.order_status_history WHERE order_id = $1 AND to_status = $2::food.order_status`, orderID, to).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyPaymentEvent_SucceededConfirmsThroughGuard(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	ev := succeeded(orderID, customerID, intentID)
	applied, err := s.ApplyPaymentEvent(ctx, ev)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Decision.Outcome != payments.OutcomeConfirmed || applied.OrderID != orderID {
		t.Fatalf("applied = %+v", applied)
	}
	status, orderPay, rowPay := readPaymentState(t, s, orderID)
	if status != "CONFIRMED" || orderPay != "CAPTURED" || rowPay != "CAPTURED" {
		t.Fatalf("state = %s/%s/%s, want CONFIRMED/CAPTURED/CAPTURED", status, orderPay, rowPay)
	}
	assertHistoryChain(t, s, orderID, "PAYMENT_PENDING", []string{"CONFIRMED"})
	if got := readInbox(t, s, ev.EventID); got != string(payments.OutcomeConfirmed) {
		t.Fatalf("inbox outcome = %s", got)
	}
	var deadlineSet bool
	if err := s.db.QueryRow(ctx, `SELECT accept_deadline_at IS NOT NULL FROM food.orders WHERE id = $1`, orderID).Scan(&deadlineSet); err != nil || !deadlineSet {
		t.Fatalf("accept_deadline_at not stamped (err %v)", err)
	}
	if _, _, astatus := readAssignment(t, s, orderID); astatus != "CREATED" {
		t.Fatalf("delivery assignment status = %s", astatus)
	}
}

func TestApplyPaymentEvent_ReplayIsANoOp(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	ev := succeeded(orderID, customerID, intentID)
	if _, err := s.ApplyPaymentEvent(ctx, ev); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	again, err := s.ApplyPaymentEvent(ctx, ev)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again.Decision.Outcome != payments.OutcomeDuplicate {
		t.Fatalf("replay outcome = %s, want duplicate", again.Decision.Outcome)
	}
	if n := countHistoryTo(t, s, orderID, "CONFIRMED"); n != 1 {
		t.Fatalf("CONFIRMED history rows = %d, want 1", n)
	}
	if got := readInbox(t, s, ev.EventID); got != string(payments.OutcomeConfirmed) {
		t.Fatalf("replay rewrote the inbox outcome to %s", got)
	}
}

func TestApplyPaymentEvent_AmountMismatchRecordsInboxOnly(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	ev := succeeded(orderID, customerID, intentID)
	ev.AmountMinor = 100
	applied, err := s.ApplyPaymentEvent(ctx, ev)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Decision.Outcome != payments.OutcomeAmountMismatch {
		t.Fatalf("outcome = %s", applied.Decision.Outcome)
	}
	status, orderPay, rowPay := readPaymentState(t, s, orderID)
	if status != "PAYMENT_PENDING" || orderPay != "PENDING" || rowPay != "PENDING" {
		t.Fatalf("a mismatched payment moved the order: %s/%s/%s", status, orderPay, rowPay)
	}
	if n := countHistoryTo(t, s, orderID, "CONFIRMED"); n != 0 {
		t.Fatalf("history recorded a confirmation")
	}
	if got := readInbox(t, s, ev.EventID); got != string(payments.OutcomeAmountMismatch) {
		t.Fatalf("inbox outcome = %s", got)
	}
}

func TestApplyPaymentEvent_LateCaptureNeverRevives(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "CANCELLED_BY_CUSTOMER", "PENDING")
	applied, err := s.ApplyPaymentEvent(ctx, succeeded(orderID, customerID, intentID))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Decision.Outcome != payments.OutcomeLateCapture {
		t.Fatalf("outcome = %s", applied.Decision.Outcome)
	}
	if status, _, _ := readPaymentState(t, s, orderID); status != "CANCELLED_BY_CUSTOMER" {
		t.Fatalf("late capture revived the order to %s", status)
	}
}

func TestApplyPaymentEvent_FailedThenSucceededConfirms(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	failed := succeeded(orderID, customerID, intentID)
	failed.EventType = events.EventPaymentFailed
	failed.Status = "failed"
	applied, err := s.ApplyPaymentEvent(ctx, failed)
	if err != nil || applied.Decision.Outcome != payments.OutcomeMarkedFailed {
		t.Fatalf("failed: %+v %v", applied, err)
	}
	if status, orderPay, rowPay := readPaymentState(t, s, orderID); status != "PAYMENT_FAILED" || orderPay != "FAILED" || rowPay != "FAILED" {
		t.Fatalf("after failed: %s/%s/%s", status, orderPay, rowPay)
	}
	applied, err = s.ApplyPaymentEvent(ctx, succeeded(orderID, customerID, intentID))
	if err != nil || applied.Decision.Outcome != payments.OutcomeConfirmed {
		t.Fatalf("succeeded after failed: %+v %v", applied, err)
	}
	assertHistoryChain(t, s, orderID, "PAYMENT_PENDING", []string{"PAYMENT_FAILED", "CONFIRMED"})
}

func TestApplyPaymentEvent_UnknownOrderRecorded(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ev := succeeded(uuid.New(), uuid.New(), uuid.NewString())
	applied, err := s.ApplyPaymentEvent(context.Background(), ev)
	if err != nil || applied.Decision.Outcome != payments.OutcomeOrderNotFound {
		t.Fatalf("applied=%+v err=%v", applied, err)
	}
	if got := readInbox(t, s, ev.EventID); got != string(payments.OutcomeOrderNotFound) {
		t.Fatalf("inbox outcome = %s", got)
	}
}

func TestAdminRequestRefund_PendingThenRefundedOnEvent(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, intentID := seedPaymentOrder(t, s, "CANCELLED_BY_ADMIN", "CAPTURED")
	adminID := uuid.New()
	key := uuid.NewString()
	plan, err := s.AdminRequestRefund(ctx, adminID, orderID, "restaurant closed", 0, key)
	if err != nil {
		t.Fatalf("request refund: %v", err)
	}
	if plan.IntentID != intentID || plan.AmountMinor != 25000 || plan.OrderStatus != "REFUND_PENDING" || plan.PaymentMethod != "ONLINE" {
		t.Fatalf("plan = %+v", plan)
	}
	status, orderPay, rowPay := readPaymentState(t, s, orderID)
	if status != "REFUND_PENDING" || orderPay != "REFUND_PENDING" || rowPay != "REFUND_PENDING" {
		t.Fatalf("after request: %s/%s/%s, want REFUND_PENDING x3 (never REFUNDED before the event)", status, orderPay, rowPay)
	}

	// Same key again: the same refund, no second row, no second transition.
	again, err := s.AdminRequestRefund(ctx, adminID, orderID, "restaurant closed", 0, key)
	if err != nil || again.RefundID != plan.RefundID {
		t.Fatalf("replay: plan=%+v err=%v", again, err)
	}
	if err := s.MarkRefundSubmitted(ctx, plan.RefundID, map[string]any{"command_id": uuid.NewString()}); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}

	applied, err := s.ApplyPaymentEvent(ctx, payments.Event{
		EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentRefunded,
		IntentID: intentID, OrderID: orderID, AmountMinor: 25000, Status: "refunded",
	})
	if err != nil || applied.Decision.Outcome != payments.OutcomeRefunded {
		t.Fatalf("refunded event: %+v %v", applied, err)
	}
	status, orderPay, rowPay = readPaymentState(t, s, orderID)
	if status != "REFUNDED" || orderPay != "REFUNDED" || rowPay != "REFUNDED" {
		t.Fatalf("after event: %s/%s/%s", status, orderPay, rowPay)
	}
	assertHistoryChain(t, s, orderID, "CANCELLED_BY_ADMIN", []string{"REFUND_PENDING", "REFUNDED"})
	var refundStatus string
	var rows int
	if err := s.db.QueryRow(ctx, `SELECT MAX(status), COUNT(*) FROM food.refunds WHERE order_id = $1`, orderID).Scan(&refundStatus, &rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || refundStatus != "PROCESSED" {
		t.Fatalf("refund rows = %d status = %s", rows, refundStatus)
	}
}

// The old AdminRefundOrder LEFT JOINed payments and scanned a NULL id when the
// order had no payment row. Now there is nothing to refund, cleanly.
func TestAdminRequestRefund_NoPaymentRowIsNotEligible(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "CANCELLED_BY_ADMIN")
	_, err := s.AdminRequestRefund(ctx, uuid.New(), orderID, "x", 0, uuid.NewString())
	if !errors.Is(err, ErrRefundNotEligible) {
		t.Fatalf("err = %v, want ErrRefundNotEligible", err)
	}
	if status, _, _ := readPaymentState(t, s, orderID); status != "CANCELLED_BY_ADMIN" {
		t.Fatalf("status moved to %s", status)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.refunds WHERE order_id = $1`, orderID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refund rows = %d (err %v)", n, err)
	}
}

func TestAdminRequestRefund_RefusesUncapturedAndInFlight(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	unpaid, _, _ := seedPaymentOrder(t, s, "CANCELLED_BY_ADMIN", "PENDING")
	if _, err := s.AdminRequestRefund(ctx, uuid.New(), unpaid, "x", 0, uuid.NewString()); !errors.Is(err, ErrRefundNotEligible) {
		t.Fatalf("uncaptured: err = %v", err)
	}
	cooking, _, _ := seedPaymentOrder(t, s, "PREPARING", "CAPTURED")
	if _, err := s.AdminRequestRefund(ctx, uuid.New(), cooking, "x", 0, uuid.NewString()); !errors.Is(err, ErrOrderTransitionNotAllowed) {
		t.Fatalf("in the kitchen: err = %v", err)
	}
	if status, _, _ := readPaymentState(t, s, cooking); status != "PREPARING" {
		t.Fatalf("status moved to %s", status)
	}
}

func TestCreatePaymentIntent_CODOnlyFromPlacedOrPending(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, st := range []string{"CONFIRMED", "PAYMENT_FAILED", "CANCELLED_BY_CUSTOMER"} {
		orderID, customerID, _ := seedPaymentOrder(t, s, st, "PENDING")
		if _, err := s.CreatePaymentIntent(ctx, customerID, orderID, "COD", "", uuid.NewString()); !errors.Is(err, ErrCODNotAllowed) {
			t.Fatalf("COD from %s: err = %v, want ErrCODNotAllowed", st, err)
		}
		if status, _, _ := readPaymentState(t, s, orderID); status != st {
			t.Fatalf("COD from %s moved the order to %s", st, status)
		}
	}
	captured, c2, _ := seedPaymentOrder(t, s, "PAYMENT_PENDING", "CAPTURED")
	if _, err := s.CreatePaymentIntent(ctx, c2, captured, "COD", "", uuid.NewString()); !errors.Is(err, ErrCODNotAllowed) {
		t.Fatalf("COD over a captured payment: err = %v", err)
	}

	orderID, customerID, _ := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	if _, err := s.CreatePaymentIntent(ctx, customerID, orderID, "COD", "", uuid.NewString()); err != nil {
		t.Fatalf("COD from PAYMENT_PENDING: %v", err)
	}
	status, orderPay, _ := readPaymentState(t, s, orderID)
	if status != "CONFIRMED" || orderPay != "NOT_REQUIRED" {
		t.Fatalf("COD result %s/%s", status, orderPay)
	}
	assertHistoryChain(t, s, orderID, "PAYMENT_PENDING", []string{"CONFIRMED"})
}

func TestCreatePaymentIntent_OnlineGuardedWithHistory(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, _ := seedPaymentOrder(t, s, "PLACED", "PENDING")
	if _, err := s.CreatePaymentIntent(ctx, customerID, orderID, "ONLINE", "card", uuid.NewString()); err != nil {
		t.Fatalf("online from PLACED: %v", err)
	}
	if status, _, _ := readPaymentState(t, s, orderID); status != "PAYMENT_PENDING" {
		t.Fatalf("status = %s", status)
	}
	assertHistoryChain(t, s, orderID, "PLACED", []string{"PAYMENT_PENDING"})

	confirmed, c2, _ := seedPaymentOrder(t, s, "CONFIRMED", "CAPTURED")
	if _, err := s.CreatePaymentIntent(ctx, c2, confirmed, "ONLINE", "upi", uuid.NewString()); !errors.Is(err, ErrPaymentNotAllowedFromState) {
		t.Fatalf("online over a confirmed order: err = %v", err)
	}
	if status, _, _ := readPaymentState(t, s, confirmed); status != "CONFIRMED" {
		t.Fatalf("status moved to %s", status)
	}
}

func TestPlaceOrder_RefusesMissingPaymentMethod(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	lat, lng := 12.9, 77.6
	customerID, _, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
	addr := seedAddress(t, s, customerID, f64(lat+1/kmPerDegLat), f64(lng))
	if _, err := s.PlaceOrder(ctx, customerID, PlaceOrderInput{AddressID: addr}, uuid.NewString()); !errors.Is(err, payments.ErrPaymentMethodInvalid) {
		t.Fatalf("err = %v, want ErrPaymentMethodInvalid", err)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.orders WHERE user_id = $1`, customerID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("orders = %d (err %v)", n, err)
	}
}

func TestAdminCancelOrder_UnpaidOrdersCancellable(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, st := range []string{"PAYMENT_PENDING", "PAYMENT_FAILED"} {
		orderID, _, _ := seedOrderWithItem(t, s, st)
		if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "ops"); err != nil {
			t.Fatalf("admin cancel from %s: %v", st, err)
		}
		assertHistoryChain(t, s, orderID, st, []string{"CANCELLED_BY_ADMIN"})
	}
	for _, st := range []string{"RESTAURANT_REJECTED", "REFUND_PENDING", "FAILED", "DRAFT"} {
		orderID, _, _ := seedOrderWithItem(t, s, st)
		if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "ops"); !errors.Is(err, ErrOrderTransitionNotAllowed) {
			t.Fatalf("admin cancel from %s: err = %v, want refused", st, err)
		}
	}
}
