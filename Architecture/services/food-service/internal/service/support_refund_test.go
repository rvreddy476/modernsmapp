package service

import (
	"context"
	"errors"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"testing"
)

type supportRefundStore struct {
	refundFakeStore
	plan      *postgres.RefundPlan
	decisions int
	failures  int
}

func (f *supportRefundStore) DecideRefund(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	f.decisions++
	return nil
}
func (f *supportRefundStore) SupportRefundPlan(context.Context, uuid.UUID) (*postgres.RefundPlan, error) {
	return f.plan, nil
}
func (f *supportRefundStore) RecordRefundSubmissionFailure(context.Context, uuid.UUID) error {
	f.failures++
	return nil
}

type retryableRefundPayments struct {
	fakePayments
	fail bool
}

func (f *retryableRefundPayments) Refund(ctx context.Context, id uuid.UUID, minor int64, reason, key string) (*payments.RefundAccepted, error) {
	if f.fail {
		return nil, errors.New("provider temporarily unavailable")
	}
	return f.fakePayments.Refund(ctx, id, minor, reason, key)
}

func TestSupportApprovalSubmissionFailureRetriesExistingCommand(t *testing.T) {
	ctx := context.Background()
	p := refundPlanFor(uuid.New())
	st := &supportRefundStore{plan: p}
	st.unsent = []postgres.RefundPlan{*p}
	pay := &retryableRefundPayments{fail: true}
	svc := New(st).WithPayments(pay)
	// Approval is accepted durably; a payment outage cannot undo or lose it.
	if err := svc.DecideRefund(ctx, uuid.New(), uuid.New(), "approved", "support decision"); err != nil {
		t.Fatal(err)
	}
	if st.decisions != 1 || st.failures != 1 || len(st.submitted) != 0 {
		t.Fatalf("state %+v", st)
	}
	pay.fail = false
	if n, err := svc.ResubmitPendingSystemRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("retry %d %v", n, err)
	}
	if len(pay.refunds) != 1 || pay.refunds[0].key != "food_refund:"+p.OrderID.String()+":"+p.RefundID.String() {
		t.Fatalf("unstable command %+v", pay.refunds)
	}
	if len(st.submitted) != 1 || st.submitted[0] != p.RefundID {
		t.Fatal("original refund not submitted")
	}
}

func TestSupportRejectDoesNotSubmitMoney(t *testing.T) {
	st := &supportRefundStore{}
	pay := &fakePayments{}
	svc := New(st).WithPayments(pay)
	if err := svc.DecideRefund(context.Background(), uuid.New(), uuid.New(), "rejected", "not eligible"); err != nil {
		t.Fatal(err)
	}
	if len(pay.refunds) != 0 {
		t.Fatal("rejection submitted refund")
	}
}
