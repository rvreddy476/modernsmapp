package service

// Lane C1 rules that need no database: the three-state payment read, who may
// retry a failed payment, and when the stub courier's timer may run.

import (
	"context"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestCustomerPaymentStateRules(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		order, payment string
		want           string
		refund         *string
	}{
		// confirming: payable, no signed capture yet.
		{"payment_pending", "pending", CustomerPaymentConfirming, nil},
		{"payment_pending", "processing", CustomerPaymentConfirming, nil},
		// paid: only when payment_status says a capture was applied.
		{"confirmed", "paid", CustomerPaymentPaid, nil},
		{"shipped", "paid", CustomerPaymentPaid, nil},
		{"delivered", "paid", CustomerPaymentPaid, nil},
		{"cancelled", "refund_pending", CustomerPaymentPaid, str(RefundStatusPending)},
		{"cancelled", "partially_refunded", CustomerPaymentPaid, str(RefundStatusPartiallyRefunded)},
		{"refunded", "refunded", CustomerPaymentPaid, str(RefundStatusRefunded)},
		// An order status alone never makes it paid.
		{"confirmed", "pending", CustomerPaymentConfirming, nil},
		// failed: the payment failed, or the order ended unpaid.
		{"payment_failed", "failed", CustomerPaymentFailed, nil},
		{"payment_pending", "failed", CustomerPaymentFailed, nil},
		{"cancelled", "pending", CustomerPaymentFailed, nil},
		{"expired", "pending", CustomerPaymentFailed, nil},
	}
	for _, c := range cases {
		got, refund := CustomerPaymentState(c.order, c.payment)
		if got != c.want {
			t.Errorf("(%s,%s) status = %q, want %q", c.order, c.payment, got, c.want)
		}
		switch {
		case c.refund == nil && refund != nil:
			t.Errorf("(%s,%s) refund_status = %q, want null", c.order, c.payment, *refund)
		case c.refund != nil && (refund == nil || *refund != *c.refund):
			t.Errorf("(%s,%s) refund_status = %v, want %q", c.order, c.payment, refund, *c.refund)
		}
	}
}

func TestCanRetryPaymentOnlyForThePayerOfAFailedOrder(t *testing.T) {
	payer, stranger := uuid.New(), uuid.New()
	for status, want := range map[string]bool{
		"payment_failed": true, "payment_pending": false, "confirmed": false,
		"cancelled": false, "expired": false,
	} {
		o := &postgres.Order{Status: status, CustomerUserID: payer}
		if got := CanRetryPayment(o, payer); got != want {
			t.Errorf("status %s: CanRetryPayment(payer) = %v, want %v", status, got, want)
		}
		if CanRetryPayment(o, stranger) {
			t.Errorf("status %s: a stranger may retry", status)
		}
	}
	if CanRetryPayment(nil, payer) {
		t.Error("a nil order is retryable")
	}
}

type namedCourier struct {
	courier.StubCourier
	name string
}

func (c namedCourier) Name() string { return c.name }

func TestStubDeliveryTimerRunsOnlyUnderTheStubCourier(t *testing.T) {
	if (&Service{}).StubAutoDeliveryAllowed() {
		t.Error("allowed with no courier configured")
	}
	if !(&Service{}).WithCourier(&courier.StubCourier{}).StubAutoDeliveryAllowed() {
		t.Error("refused under the stub courier")
	}
	for _, name := range []string{"shiprocket", "delhivery", ""} {
		s := (&Service{}).WithCourier(namedCourier{name: name})
		if s.StubAutoDeliveryAllowed() {
			t.Errorf("allowed under courier %q", name)
		}
		// Guarded before the store is touched: the Service has none, so a
		// sweep that got past the guard would panic here.
		if n := s.SweepStubDelivery(context.Background(), 1); n != 0 {
			t.Errorf("courier %q: sweep advanced %d", name, n)
		}
	}
}
