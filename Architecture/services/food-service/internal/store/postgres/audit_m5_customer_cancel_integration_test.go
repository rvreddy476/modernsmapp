package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
)

// Audit M5 (founder decision, option a). Runs only with TEST_PG_DSN (CI).
//
// Only the database proves these: a refused cancel must leave no trace (the
// order row, payment, refunds, history and outbox are all untouched), and the
// customer's way out, a support ticket plus refund request on the same
// in-kitchen order, must be accepted by the real ownership and balance checks.

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCustomerCancelAfterPreparingIsRefusedAndLeavesNoTrace(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, status := range []string{
		orderstate.Preparing, orderstate.ReadyForPickup, orderstate.DeliveryAssigning,
		orderstate.OutForDelivery, orderstate.Delivered,
	} {
		t.Run(status, func(t *testing.T) {
			orderID, customerID, _ := seedPaymentOrder(t, s, status, "CAPTURED")
			historyBefore := len(readHistory(t, s, orderID))
			outboxBefore := len(readOrderOutbox(t, s, orderID))

			_, err := s.CancelOrder(ctx, customerID, orderID, "eta slipped")
			if !errors.Is(err, ErrCustomerCancelAfterPreparing) {
				t.Fatalf("cancel from %s: want ErrCustomerCancelAfterPreparing, got %v", status, err)
			}
			orderStatus, orderPay, rowPay := readPaymentState(t, s, orderID)
			if orderStatus != status || orderPay != "CAPTURED" || rowPay != "CAPTURED" {
				t.Fatalf("state after refused cancel = %s/%s/%s", orderStatus, orderPay, rowPay)
			}
			if n := countRows(t, s, `SELECT COUNT(*) FROM food.refunds WHERE order_id = $1`, orderID); n != 0 {
				t.Fatalf("refused cancel wrote %d refund rows", n)
			}
			if n := countRows(t, s, `SELECT COUNT(*) FROM food.orders WHERE id = $1 AND (cancelled_at IS NOT NULL OR cancelled_by IS NOT NULL)`, orderID); n != 0 {
				t.Fatal("refused cancel stamped the order cancelled")
			}
			if got := len(readHistory(t, s, orderID)); got != historyBefore {
				t.Fatalf("history rows %d -> %d", historyBefore, got)
			}
			if got := len(readOrderOutbox(t, s, orderID)); got != outboxBefore {
				t.Fatalf("outbox rows %d -> %d", outboxBefore, got)
			}
		})
	}
}

// Cancels before the kitchen starts are unchanged: a paid CONFIRMED or PLACED
// order is cancelled and its full captured amount requested through the
// durable refund path in the same transaction.
func TestCustomerCancelBeforePreparingStillRefundsInFull(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, status := range []string{orderstate.Placed, orderstate.Confirmed} {
		t.Run(status, func(t *testing.T) {
			orderID, customerID, intentID := seedPaymentOrder(t, s, status, "CAPTURED")
			order, err := s.CancelOrder(ctx, customerID, orderID, "changed my mind")
			if err != nil {
				t.Fatalf("cancel from %s: %v", status, err)
			}
			if order.Status != orderstate.RefundPending {
				t.Fatalf("returned status = %s", order.Status)
			}
			assertHistoryChain(t, s, orderID, status, []string{orderstate.CancelledByCustomer, orderstate.RefundPending})
			plan, err := s.OpenRefundPlan(ctx, orderID)
			if err != nil {
				t.Fatal(err)
			}
			if plan.AmountMinor != 25000 || plan.IntentID != intentID {
				t.Fatalf("plan = %+v, want the full 25000 paise", plan)
			}
		})
	}

	// A stranger still gets not-found, not the after-preparing refusal.
	orderID, _, _ := seedPaymentOrder(t, s, orderstate.Preparing, "CAPTURED")
	if _, err := s.CancelOrder(ctx, uuid.New(), orderID, "x"); errors.Is(err, ErrCustomerCancelAfterPreparing) || err == nil {
		t.Fatalf("stranger cancel: %v", err)
	}
}

// The customer's route once the kitchen has started: a support ticket and a
// refund request on the in-kitchen order. The admin decides it through the
// existing admin refund path (here, approval once the order is delivered).
func TestRefundRequestOpensForAnOrderInPreparing(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, _ := seedPaymentOrder(t, s, orderstate.Preparing, "CAPTURED")
	if _, err := s.CancelOrder(ctx, customerID, orderID, "eta slipped"); !errors.Is(err, ErrCustomerCancelAfterPreparing) {
		t.Fatalf("cancel: %v", err)
	}
	ticket, err := s.CreateTicket(ctx, CreateTicketInput{CustomerID: customerID, OrderID: &orderID, Category: "refund", Subject: "Order is late"})
	if err != nil {
		t.Fatalf("ticket on a PREPARING order: %v", err)
	}
	req, err := s.CreateRefundRequest(ctx, customerID, orderID, &ticket.ID, 100, "eta slipped")
	if err != nil {
		t.Fatalf("refund request on a PREPARING order: %v", err)
	}
	if req.Status != "requested" || req.TicketID == nil || *req.TicketID != ticket.ID {
		t.Fatalf("request = %+v", req)
	}
	assertOrderStatus(t, s, orderID, orderstate.Preparing)
	if n := countRows(t, s, `SELECT COUNT(*) FROM food.refunds WHERE order_id = $1`, orderID); n != 0 {
		t.Fatalf("opening a request moved money: %d refund rows", n)
	}

	// The food is delivered; the admin approves the requested amount.
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET status = 'DELIVERED' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideRefund(ctx, uuid.New(), req.ID, "approved", "late delivery"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	plan, err := s.SupportRefundPlan(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AmountMinor != 10000 {
		t.Fatalf("approved plan = %+v, want the 10000 paise asked for", plan)
	}
}
