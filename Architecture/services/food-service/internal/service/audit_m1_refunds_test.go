package service

import (
	"context"
	"testing"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M1: the refund queued by a late capture or an admin cancel
// of a paid order is submitted to payments straight away.

type adminCancelFakeStore struct {
	refundFakeStore
	cancelled *postgres.Order
}

func (f *adminCancelFakeStore) AdminCancelOrder(context.Context, uuid.UUID, uuid.UUID, string) (*postgres.Order, error) {
	o := *f.cancelled
	return &o, nil
}

func (f *adminCancelFakeStore) AdminGetOrder(context.Context, uuid.UUID) (*postgres.Order, error) {
	o := *f.cancelled
	return &o, nil
}

func TestLateCaptureSubmitsTheQueuedRefund(t *testing.T) {
	orderID := uuid.New()
	plan := refundPlanFor(orderID)
	st := &refundFakeStore{plans: map[uuid.UUID]*postgres.RefundPlan{orderID: plan}}
	pay := &fakePayments{}
	New(st).WithPayments(pay).OnPaymentEventApplied(context.Background(), payments.Applied{
		OrderID:  orderID,
		Decision: payments.Decision{Outcome: payments.OutcomeLateCapture, Effect: payments.EffectLateCaptureRefund},
	})
	if len(pay.refunds) != 1 || pay.refunds[0].amount != plan.AmountMinor ||
		pay.refunds[0].key != "food_refund:"+orderID.String()+":"+plan.RefundID.String() {
		t.Fatalf("payments refunds = %+v", pay.refunds)
	}
	if len(st.submitted) != 1 || st.submitted[0] != plan.RefundID {
		t.Fatalf("submitted = %v", st.submitted)
	}
}

func TestAdminCancelSubmitsTheRefundOfAPaidOrder(t *testing.T) {
	orderID := uuid.New()
	plan := refundPlanFor(orderID)
	st := &adminCancelFakeStore{
		refundFakeStore: refundFakeStore{plans: map[uuid.UUID]*postgres.RefundPlan{orderID: plan}},
		cancelled:       &postgres.Order{ID: orderID, Status: "REFUND_PENDING"},
	}
	pay := &fakePayments{}
	if _, err := New(st).WithPayments(pay).AdminCancelOrder(context.Background(), uuid.New(), orderID, "no rider"); err != nil {
		t.Fatal(err)
	}
	if len(pay.refunds) != 1 || len(st.submitted) != 1 || st.submitted[0] != plan.RefundID {
		t.Fatalf("refunds %v submitted %v", pay.refunds, st.submitted)
	}
}
