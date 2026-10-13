package postgres

import (
	"context"
	"errors"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"math"
	"sync"
	"testing"
	"time"
)

func TestRefundAmountMustBePositiveWholePaise(t *testing.T) {
	for _, amount := range []float64{0, -1, 0.001, 1.001, math.NaN(), math.Inf(1), 100000000} {
		if _, err := refundAmountMinor(amount); !errors.Is(err, ErrInvalidRefundAmount) {
			t.Fatalf("amount %v accepted: %v", amount, err)
		}
	}
	if got, err := refundAmountMinor(100.01); err != nil || got != 10001 {
		t.Fatalf("valid amount: %d %v", got, err)
	}
}

func TestSupportRefundOwnershipAndAmount(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, owner, _ := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
	stranger := uuid.New()
	if _, err := s.CreateTicket(ctx, CreateTicketInput{CustomerID: stranger, OrderID: &order, Category: "payment", Subject: "x"}); !errors.Is(err, ErrSupportOrderForbidden) {
		t.Fatalf("foreign ticket: %v", err)
	}
	ticket, err := s.CreateTicket(ctx, CreateTicketInput{CustomerID: owner, OrderID: &order, Category: "payment", Subject: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user   uuid.UUID
		amount float64
		ticket *uuid.UUID
		want   error
	}{
		{stranger, 10, nil, ErrSupportOrderForbidden}, {owner, 251, nil, ErrInvalidRefundAmount}, {owner, 10, &stranger, ErrSupportOrderForbidden},
	} {
		if _, err := s.CreateRefundRequest(ctx, tc.user, order, tc.ticket, tc.amount, "x"); !errors.Is(err, tc.want) {
			t.Fatalf("request: %v want %v", err, tc.want)
		}
	}
	if _, err = s.CreateRefundRequest(ctx, owner, order, &ticket.ID, 200, "partial"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRefundRequest(ctx, owner, order, nil, 51, "duplicate"); !errors.Is(err, ErrInvalidRefundAmount) {
		t.Fatalf("reserved requests not counted: %v", err)
	}
}

func TestSupportRefundApprovalIsAtomicConcurrentAndSettledByEvent(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, owner, intent := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
	req, err := s.CreateRefundRequest(ctx, owner, order, nil, 100, "partial")
	if err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.DecideRefund(ctx, admin, req.ID, "approved", "accepted") }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	plan, err := s.SupportRefundPlan(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AmountMinor != 10000 || plan.Status != "PENDING" {
		t.Fatalf("bad plan %+v", plan)
	}
	var n int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.refunds WHERE order_id=$1`, order).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate refunds %d", n)
	}
	if err = s.DecideRefund(ctx, admin, req.ID, "rejected", ""); !errors.Is(err, ErrRefundDecisionConflict) {
		t.Fatalf("reverse verdict: %v", err)
	}
	if err = s.RecordRefundSubmissionFailure(ctx, plan.RefundID); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(ctx, `UPDATE food.refunds SET created_at=NOW()-INTERVAL '2 minutes',raw_response=raw_response || jsonb_build_object('next_submission_at',NOW()-INTERVAL '1 second') WHERE id=$1`, plan.RefundID)
	retry, err := s.ListUnsubmittedSystemRefunds(ctx, time.Minute, 500)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range retry {
		found = found || p.RefundID == plan.RefundID
	}
	if !found {
		t.Fatal("admin/support refund missing from retry queue")
	}
	command := uuid.NewString()
	if err = s.MarkRefundSubmitted(ctx, plan.RefundID, map[string]any{"payments_command_id": command}); err != nil {
		t.Fatal(err)
	}
	ev := payments.Event{EventID: uuid.NewString(), EventType: events.EventPaymentRefunded, OrderID: order, IntentID: intent, AmountMinor: 10000, Status: "partially_refunded", CommandID: command, ProviderRefundID: "rf_" + uuid.NewString()}
	if _, err = s.ApplyPaymentEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPaymentEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	ev.EventID = uuid.NewString()
	applied, err := s.ApplyPaymentEvent(ctx, ev)
	if err != nil || applied.Decision.Outcome != payments.OutcomeDuplicate {
		t.Fatalf("provider duplicate: %+v %v", applied, err)
	}
	var status, txn string
	s.db.QueryRow(ctx, `SELECT status::text,refund_txn_id FROM food.refund_requests WHERE id=$1`, req.ID).Scan(&status, &txn)
	if status != "processed" || txn != ev.ProviderRefundID {
		t.Fatalf("readback %s %s", status, txn)
	}
	if _, err = s.CreateRefundRequest(ctx, owner, order, nil, 151, "excess"); !errors.Is(err, ErrInvalidRefundAmount) {
		t.Fatalf("remaining balance: %v", err)
	}
	if _, err = s.CreateRefundRequest(ctx, owner, order, nil, 150, "remaining"); err != nil {
		t.Fatal(err)
	}
}

func TestSupportRefundCannotApproveUncapturedOrForeignLegacyRequest(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, owner, _ := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
	req, err := s.CreateRefundRequest(ctx, owner, order, nil, 10, "test")
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(ctx, `UPDATE food.refund_requests SET customer_id=$2 WHERE id=$1`, req.ID, uuid.New())
	if err = s.DecideRefund(ctx, uuid.New(), req.ID, "approved", ""); !errors.Is(err, ErrRefundNotEligible) {
		t.Fatalf("legacy foreign: %v", err)
	}
	var n int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.refunds WHERE order_id=$1`, order).Scan(&n)
	if n != 0 {
		t.Fatal("failed approval created refund")
	}
	unpaid, user, _ := seedPaymentOrder(t, s, "DELIVERED", "PENDING")
	if _, err = s.CreateRefundRequest(ctx, user, unpaid, nil, 10, ""); !errors.Is(err, ErrRefundNotEligible) {
		t.Fatalf("unpaid: %v", err)
	}
}

func TestRefundFailureEventIsVisibleWithoutMarkingProcessed(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, owner, intent := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
	req, err := s.CreateRefundRequest(ctx, owner, order, nil, 250, "full")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideRefund(ctx, uuid.New(), req.ID, "approved", ""); err != nil {
		t.Fatal(err)
	}
	plan, _ := s.SupportRefundPlan(ctx, req.ID)
	command := uuid.NewString()
	s.MarkRefundSubmitted(ctx, plan.RefundID, map[string]any{"payments_command_id": command})
	ev := payments.Event{EventID: uuid.NewString(), EventType: events.EventPaymentRefundFailed, OrderID: order, IntentID: intent, CommandID: command, AmountMinor: 25000}
	if _, err = s.ApplyPaymentEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListRefundsForAdmin(ctx, "approved", 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == req.ID {
			found = true
			if row.ExecutionError == nil || row.ExecutionStatus == nil || *row.ExecutionStatus != "SUBMITTED" {
				t.Fatalf("missing error %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("approved request not visible")
	}
	ev.EventID = uuid.NewString()
	ev.EventType = events.EventPaymentRefunded
	ev.Status = "refunded"
	ev.ProviderRefundID = "rf_" + uuid.NewString()
	if _, err = s.ApplyPaymentEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	var status string
	s.db.QueryRow(ctx, `SELECT status::text FROM food.refund_requests WHERE id=$1`, req.ID).Scan(&status)
	if status != "processed" {
		t.Fatal(status)
	}
}

func TestConcurrentRefundReservationsCannotExceedCapturedAmount(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	for _, direct := range []bool{false, true} {
		order, owner, _ := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
		var wg sync.WaitGroup
		results := make(chan error, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				var err error
				if direct {
					_, err = s.AdminRequestRefund(ctx, uuid.New(), order, "partial", 200, uuid.NewString())
				} else {
					_, err = s.CreateRefundRequest(ctx, owner, order, nil, 200, "partial")
				}
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		ok, invalid := 0, 0
		for err := range results {
			if err == nil {
				ok++
			} else if errors.Is(err, ErrInvalidRefundAmount) {
				invalid++
			} else {
				t.Fatalf("unexpected reservation result %v", err)
			}
		}
		if ok != 1 || invalid != 1 {
			t.Fatalf("direct=%v accepted=%d refused=%d", direct, ok, invalid)
		}
	}
}
