package postgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A duplicate order capture is refunded only when this ledger bears the
// report out, and never for more than the duplicate payment captured. The
// transaction (row, key, redelivery, the refund webhook) is proven in
// service/orderduplicate_od_integration_test.go.
func TestDuplicateOwedMinor(t *testing.T) {
	order, other := uuid.New(), uuid.New()
	dupID, boundID := uuid.New(), uuid.New()
	ev := func(mut func(*OrderDuplicateCapture)) OrderDuplicateCapture {
		e := OrderDuplicateCapture{EventID: "evt-1", OrderID: order, IntentID: dupID, BoundIntentID: boundID,
			AmountMinor: 118000, Currency: "INR"}
		if mut != nil {
			mut(&e)
		}
		return e
	}
	intent := func(id uuid.UUID, status string, mut func(*orderIntent)) orderIntent {
		i := orderIntent{ID: id, OwnerDomain: "commerce-service", ReferenceType: "order", ReferenceID: order,
			Status: status, AmountMinor: 118000, Currency: "INR", Provider: "razorpay", ProviderPayment: "pay_" + id.String()[:8]}
		if mut != nil {
			mut(&i)
		}
		return i
	}
	captured := int64(100000)
	for _, tc := range []struct {
		name       string
		ev         OrderDuplicateCapture
		dup, bound orderIntent
		want       int64 // 0 = refused
	}{
		{"a second paid intent of the order: owed in full",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 118000},
		{"the order's own payment already partly refunded: still a duplicate",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "partially_refunded", nil), 118000},
		{"the order's own payment refunded after a cancel: still a duplicate",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "refunded", nil), 118000},
		{"currency compared case-insensitively",
			ev(func(e *OrderDuplicateCapture) { e.Currency = " inr " }), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 118000},
		{"a bank-offer capture: owed what was captured, not the order value",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { i.CapturedMinor = &captured }), intent(boundID, "succeeded", nil), 100000},

		{"the bound intent is not paid: that would refund the order's only payment",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "pending", nil), 0},
		{"the bound intent failed",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "failed", nil), 0},
		{"the duplicate is the bound intent",
			ev(func(e *OrderDuplicateCapture) { e.IntentID = boundID }), intent(boundID, "succeeded", nil), intent(boundID, "succeeded", nil), 0},
		{"no bound intent named",
			ev(func(e *OrderDuplicateCapture) { e.BoundIntentID = uuid.Nil }), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 0},
		{"no duplicate named",
			ev(func(e *OrderDuplicateCapture) { e.IntentID = uuid.Nil }), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 0},
		{"the duplicate belongs to another order",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { i.ReferenceID = other }), intent(boundID, "succeeded", nil), 0},
		{"the bound intent belongs to another order",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", func(i *orderIntent) { i.ReferenceID = other }), 0},
		{"the duplicate is another domain's",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { i.OwnerDomain = "food-service" }), intent(boundID, "succeeded", nil), 0},
		{"the bound intent is another domain's",
			ev(nil), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", func(i *orderIntent) { i.OwnerDomain = "" }), 0},
		{"the duplicate is not an order intent",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { i.ReferenceType = "food_order" }), intent(boundID, "succeeded", nil), 0},
		{"the duplicate never captured",
			ev(nil), intent(dupID, "pending", nil), intent(boundID, "succeeded", nil), 0},
		{"the duplicate is already partly refunded",
			ev(nil), intent(dupID, "partially_refunded", nil), intent(boundID, "succeeded", nil), 0},
		{"the duplicate has a refund reserved",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { i.ReservedMinor = 1 }), intent(boundID, "succeeded", nil), 0},
		{"the duplicate has a refund settled",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { i.RefundedMinor = 1 }), intent(boundID, "succeeded", nil), 0},
		{"an amount that is not the intent's",
			ev(func(e *OrderDuplicateCapture) { e.AmountMinor = 1 }), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 0},
		{"a currency that is not the intent's",
			ev(func(e *OrderDuplicateCapture) { e.Currency = "USD" }), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 0},
		{"no currency",
			ev(func(e *OrderDuplicateCapture) { e.Currency = "" }), intent(dupID, "succeeded", nil), intent(boundID, "succeeded", nil), 0},
		{"captured more than the intent: never refund past the payment",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { c := int64(118001); i.CapturedMinor = &c }), intent(boundID, "succeeded", nil), 0},
		{"captured nothing",
			ev(nil), intent(dupID, "succeeded", func(i *orderIntent) { c := int64(0); i.CapturedMinor = &c }), intent(boundID, "succeeded", nil), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := duplicateOwedMinor(tc.ev, tc.dup, tc.bound)
			if tc.want == 0 {
				if !errors.Is(err, ErrOrderDuplicateRefused) {
					t.Fatalf("duplicateOwedMinor = %d, %v; want ErrOrderDuplicateRefused", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("duplicateOwedMinor = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestDuplicateOrderPaymentKey(t *testing.T) {
	id := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	if k := duplicateOrderPaymentKey(orderIntent{ID: id, ProviderPayment: " pay_B "}); k != "pay_B" {
		t.Fatalf("key = %q, want the provider payment id", k)
	}
	if k := duplicateOrderPaymentKey(orderIntent{ID: id}); k != "intent:"+id.String() {
		t.Fatalf("key = %q, want the intent when no payment id is held", k)
	}
}
