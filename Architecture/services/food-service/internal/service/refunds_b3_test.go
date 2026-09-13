package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// refundFakeStore models the store after a rejection of a paid order: the
// refund request is durable (REFUND_PENDING) and the service must submit it.
type refundFakeStore struct {
	Store
	rejected   []uuid.UUID
	plans      map[uuid.UUID]*postgres.RefundPlan
	unsent     []postgres.RefundPlan
	submitted  []uuid.UUID
	partnerOut *postgres.Order
}

func (f *refundFakeStore) AutoRejectExpiredOrders(context.Context, int) ([]uuid.UUID, error) {
	return f.rejected, nil
}

func (f *refundFakeStore) OpenRefundPlan(_ context.Context, orderID uuid.UUID) (*postgres.RefundPlan, error) {
	if p, ok := f.plans[orderID]; ok {
		c := *p
		return &c, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *refundFakeStore) ListUnsubmittedSystemRefunds(context.Context, time.Duration, int) ([]postgres.RefundPlan, error) {
	return f.unsent, nil
}

func (f *refundFakeStore) MarkRefundSubmitted(_ context.Context, refundID uuid.UUID, _ map[string]any) error {
	f.submitted = append(f.submitted, refundID)
	return nil
}

func (f *refundFakeStore) PartnerUpdateOrderStatus(context.Context, uuid.UUID, uuid.UUID, string, string, string) (*postgres.Order, error) {
	o := *f.partnerOut
	return &o, nil
}

func refundPlanFor(orderID uuid.UUID) *postgres.RefundPlan {
	return &postgres.RefundPlan{RefundID: uuid.New(), OrderID: orderID, PaymentMethod: "ONLINE", IntentID: sIntentID.String(),
		AmountMinor: 30262, Status: "PENDING", OrderStatus: "REFUND_PENDING"}
}

func TestAutoRejectSubmitsTheRefundOfAPaidOrder(t *testing.T) {
	paid, unpaid := uuid.New(), uuid.New()
	plan := refundPlanFor(paid)
	st := &refundFakeStore{rejected: []uuid.UUID{paid, unpaid}, plans: map[uuid.UUID]*postgres.RefundPlan{paid: plan}}
	pay := &fakePayments{}
	svc := New(st).WithPayments(pay)
	n, err := svc.AutoRejectSLAExpiredOrders(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("auto reject = %d, %v", n, err)
	}
	if len(pay.refunds) != 1 || pay.refunds[0].amount != 30262 || pay.refunds[0].intent != sIntentID ||
		pay.refunds[0].key != "food_refund:"+paid.String()+":"+plan.RefundID.String() {
		t.Fatalf("payments refunds = %+v", pay.refunds)
	}
	if len(st.submitted) != 1 || st.submitted[0] != plan.RefundID {
		t.Fatalf("submitted = %v", st.submitted)
	}
}

func TestPartnerRejectSubmitsTheRefundOfAPaidOrder(t *testing.T) {
	orderID := uuid.New()
	plan := refundPlanFor(orderID)
	st := &refundFakeStore{plans: map[uuid.UUID]*postgres.RefundPlan{orderID: plan},
		partnerOut: &postgres.Order{ID: orderID, Status: "REFUND_PENDING"}}
	pay := &fakePayments{}
	if _, err := New(st).WithPayments(pay).PartnerUpdateOrderStatus(context.Background(), uuid.New(), orderID, "RESTAURANT_REJECTED", "closed", "k"); err != nil {
		t.Fatal(err)
	}
	if len(pay.refunds) != 1 || len(st.submitted) != 1 {
		t.Fatalf("refunds %v submitted %v", pay.refunds, st.submitted)
	}
}

func TestResubmitPendingSystemRefunds(t *testing.T) {
	a, b := refundPlanFor(uuid.New()), refundPlanFor(uuid.New())
	st := &refundFakeStore{unsent: []postgres.RefundPlan{*a, *b}}
	pay := &fakePayments{}
	n, err := New(st).WithPayments(pay).ResubmitPendingSystemRefunds(context.Background())
	if err != nil || n != 2 || len(pay.refunds) != 2 || len(st.submitted) != 2 {
		t.Fatalf("resubmitted %d (%v): refunds %v submitted %v", n, err, pay.refunds, st.submitted)
	}
	// Without a payments client nothing is marked submitted.
	st2 := &refundFakeStore{unsent: []postgres.RefundPlan{*a}}
	if n, _ := New(st2).ResubmitPendingSystemRefunds(context.Background()); n != 0 || len(st2.submitted) != 0 {
		t.Fatalf("submitted without a payments client: %d %v", n, st2.submitted)
	}
}
