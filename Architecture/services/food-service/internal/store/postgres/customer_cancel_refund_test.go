package postgres

import (
	"context"
	"testing"
)

// B4 follow-up: a customer who cancels a PAID order no longer waits for an
// admin. The cancelling transaction requests the refund through the same
// guarded system path a rejection uses; payment.refunded finalises it.

func TestCustomerCancel_PaidOrderRequestsRefund(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "CONFIRMED", "CAPTURED")
	order, err := s.CancelOrder(ctx, customerID, orderID, "changed my mind")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if order.Status != "REFUND_PENDING" {
		t.Fatalf("returned status = %s", order.Status)
	}
	status, orderPay, rowPay := readPaymentState(t, s, orderID)
	if status != "REFUND_PENDING" || orderPay != "REFUND_PENDING" || rowPay != "REFUND_PENDING" {
		t.Fatalf("state = %s/%s/%s, want REFUND_PENDING everywhere", status, orderPay, rowPay)
	}
	assertHistoryChain(t, s, orderID, "CONFIRMED", []string{"CANCELLED_BY_CUSTOMER", "REFUND_PENDING"})
	plan, err := s.OpenRefundPlan(ctx, orderID)
	if err != nil {
		t.Fatalf("open refund plan: %v", err)
	}
	if plan.AmountMinor != 25000 || plan.Status != "PENDING" || plan.IntentID != intentID {
		t.Fatalf("plan = %+v", plan)
	}
	var requestedBy *string
	if err := s.db.QueryRow(ctx, `SELECT requested_by::text FROM food.refunds WHERE id = $1`, plan.RefundID).Scan(&requestedBy); err != nil || requestedBy != nil {
		t.Fatalf("refund is not a system request: %v", err)
	}
	pending, err := s.ListUnsubmittedSystemRefunds(ctx, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, p := range pending {
		listed = listed || p.RefundID == plan.RefundID
	}
	if !listed {
		t.Fatal("the cancellation refund is not listed for (re)submission")
	}

	// An unpaid order only cancels, as before.
	unpaid, c2, _ := seedPaymentOrder(t, s, "CONFIRMED", "PENDING")
	if _, err := s.CancelOrder(ctx, c2, unpaid, "changed my mind"); err != nil {
		t.Fatal(err)
	}
	assertOrderStatus(t, s, unpaid, "CANCELLED_BY_CUSTOMER")
	if _, err := s.OpenRefundPlan(ctx, unpaid); err == nil {
		t.Fatal("a refund was requested for an unpaid cancellation")
	}
}
