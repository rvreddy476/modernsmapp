package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit M5 (founder decision, option a): a customer cancel the store refuses
// because the kitchen started submits no refund, and customer order responses
// carry can_cancel so the app can hide the button.

type refusedCancelStore struct {
	*refundFakeStore
	orders []postgres.Order
}

func (f *refusedCancelStore) CancelOrder(context.Context, uuid.UUID, uuid.UUID, string) (*postgres.Order, error) {
	return nil, fmt.Errorf("%w (order is PREPARING)", postgres.ErrCustomerCancelAfterPreparing)
}

func (f *refusedCancelStore) GetOrder(_ context.Context, _, orderID uuid.UUID) (*postgres.Order, error) {
	for i := range f.orders {
		if f.orders[i].ID == orderID {
			o := f.orders[i]
			return &o, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *refusedCancelStore) ListOrders(context.Context, uuid.UUID) ([]postgres.Order, error) {
	return append([]postgres.Order(nil), f.orders...), nil
}

func TestCustomerCancelAfterPreparingSubmitsNoRefund(t *testing.T) {
	orderID := uuid.New()
	st := &refusedCancelStore{refundFakeStore: &refundFakeStore{plans: map[uuid.UUID]*postgres.RefundPlan{orderID: refundPlanFor(orderID)}}}
	pay := &fakePayments{}
	o, err := New(st).WithPayments(pay).CancelOrder(context.Background(), uuid.New(), orderID, "eta slipped")
	if !errors.Is(err, postgres.ErrCustomerCancelAfterPreparing) || o != nil {
		t.Fatalf("cancel after preparing = %v, %v", o, err)
	}
	if len(pay.refunds) != 0 || len(st.submitted) != 0 {
		t.Fatalf("a refused cancel submitted a refund: %+v %v", pay.refunds, st.submitted)
	}
}

func TestCustomerOrderResponsesCarryCanCancel(t *testing.T) {
	statuses := []string{
		orderstate.Placed, orderstate.PaymentPending, orderstate.Confirmed, orderstate.Preparing,
		orderstate.ReadyForPickup, orderstate.OutForDelivery, orderstate.Delivered, orderstate.CancelledByCustomer,
	}
	want := map[string]bool{orderstate.Placed: true, orderstate.Confirmed: true}
	st := &refusedCancelStore{refundFakeStore: &refundFakeStore{}}
	for _, s := range statuses {
		st.orders = append(st.orders, postgres.Order{ID: uuid.New(), Status: s})
	}
	svc := New(st)
	list, err := svc.ListOrders(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range list {
		if o.CanCancel == nil || *o.CanCancel != want[o.Status] {
			t.Fatalf("list: %s can_cancel = %v, want %v", o.Status, o.CanCancel, want[o.Status])
		}
		one, err := svc.GetOrder(context.Background(), uuid.New(), o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if one.CanCancel == nil || *one.CanCancel != want[o.Status] {
			t.Fatalf("detail: %s can_cancel = %v, want %v", o.Status, one.CanCancel, want[o.Status])
		}
	}
}

// A cancel from CONFIRMED still goes through: the cancelled order it returns
// can no longer be cancelled.
func TestCustomerCancelBeforePreparingStillWorks(t *testing.T) {
	orderID := uuid.New()
	plan := refundPlanFor(orderID)
	st := &cancelFakeStore{refundFakeStore: &refundFakeStore{plans: map[uuid.UUID]*postgres.RefundPlan{orderID: plan}},
		out: &postgres.Order{ID: orderID, Status: orderstate.RefundPending}}
	pay := &fakePayments{}
	o, err := New(st).WithPayments(pay).CancelOrder(context.Background(), uuid.New(), orderID, "changed my mind")
	if err != nil {
		t.Fatal(err)
	}
	if o.CanCancel == nil || *o.CanCancel {
		t.Fatalf("cancelled order can_cancel = %v", o.CanCancel)
	}
	if len(pay.refunds) != 1 || pay.refunds[0].amount != plan.AmountMinor {
		t.Fatalf("full refund not submitted: %+v", pay.refunds)
	}
}
