package postgres

import (
	"context"
	"testing"

	"github.com/atpost/food-service/internal/payments"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M1: money captured on an order nobody will cook, and money
// held by an admin-cancelled paid order, is queued for refund through the
// durable food.refunds path in the same transaction.

func countRefunds(t *testing.T, s *Store, orderID uuid.UUID) (n int, sumMinor int64) {
	t.Helper()
	if err := s.db.QueryRow(context.Background(), `
		SELECT COUNT(*), COALESCE(SUM(ROUND(amount * 100)::bigint), 0)::bigint
		FROM food.refunds WHERE order_id = $1 AND status IN ('PENDING', 'SUBMITTED', 'PROCESSED')
	`, orderID).Scan(&n, &sumMinor); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return n, sumMinor
}

func TestApplyPaymentEvent_LateCaptureQueuesOneDurableRefund(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "CANCELLED_BY_ADMIN", "PENDING")
	applied, err := s.ApplyPaymentEvent(ctx, succeeded(orderID, customerID, intentID))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Decision.Outcome != payments.OutcomeLateCapture || applied.Decision.Effect != payments.EffectLateCaptureRefund {
		t.Fatalf("decision = %+v", applied.Decision)
	}
	status, orderPay, rowPay := readPaymentState(t, s, orderID)
	if status != "CANCELLED_BY_ADMIN" {
		t.Fatalf("late capture moved the order to %s", status)
	}
	if orderPay != "REFUND_PENDING" || rowPay != "REFUND_PENDING" {
		t.Fatalf("payment state = %s/%s, want REFUND_PENDING", orderPay, rowPay)
	}
	if n, sum := countRefunds(t, s, orderID); n != 1 || sum != 25000 {
		t.Fatalf("refunds = %d rows / %d paise, want 1 / 25000", n, sum)
	}
	plan, err := s.OpenRefundPlan(ctx, orderID)
	if err != nil || plan.Status != "PENDING" || plan.IntentID != intentID || plan.AmountMinor != 25000 {
		t.Fatalf("open plan = %+v err=%v", plan, err)
	}

	// A second capture event for the same payment (new event id) queues nothing.
	again, err := s.ApplyPaymentEvent(ctx, succeeded(orderID, customerID, intentID))
	if err != nil {
		t.Fatalf("apply again: %v", err)
	}
	if again.Decision.Effect != payments.EffectNone {
		t.Fatalf("second capture decision = %+v", again.Decision)
	}
	if n, sum := countRefunds(t, s, orderID); n != 1 || sum != 25000 {
		t.Fatalf("after redelivery refunds = %d / %d, want 1 / 25000", n, sum)
	}
}

func TestAdminCancelOrder_PaidOrderRequestsRefundInSameTx(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedPaymentOrder(t, s, "DELIVERY_ASSIGNING", "CAPTURED")
	adminID := uuid.New()
	o, err := s.AdminCancelOrder(ctx, adminID, orderID, "no rider")
	if err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	if o.Status != "REFUND_PENDING" {
		t.Fatalf("order status = %s, want REFUND_PENDING", o.Status)
	}
	if _, orderPay, rowPay := readPaymentState(t, s, orderID); orderPay != "REFUND_PENDING" || rowPay != "REFUND_PENDING" {
		t.Fatalf("payment state = %s/%s", orderPay, rowPay)
	}
	if n, sum := countRefunds(t, s, orderID); n != 1 || sum != 25000 {
		t.Fatalf("refunds = %d / %d, want 1 / 25000", n, sum)
	}
	assertHistoryChain(t, s, orderID, "DELIVERY_ASSIGNING", []string{"CANCELLED_BY_ADMIN", "REFUND_PENDING"})
}

func TestAdminCancelOrder_UnpaidOrderOnlyCancels(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	o, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "stale collect")
	if err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	if o.Status != "CANCELLED_BY_ADMIN" {
		t.Fatalf("status = %s", o.Status)
	}
	if n, _ := countRefunds(t, s, orderID); n != 0 {
		t.Fatalf("an unpaid cancel queued %d refunds", n)
	}
}
