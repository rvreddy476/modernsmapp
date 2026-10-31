package orderstate

import (
	"errors"
	"testing"
)

// Audit M5 (founder decision, option a): a customer cancels an order only
// before the kitchen starts. From PREPARING onward the refusal is the stable
// ErrCustomerCancelAfterPreparing (HTTP 409 FOOD_CANCEL_AFTER_PREPARING), so
// the app can send the customer to a support refund request.
func TestCustomerCancel(t *testing.T) {
	allowed := map[string]bool{Placed: true, Confirmed: true}
	afterPreparing := map[string]bool{
		Preparing: true, ReadyForPickup: true, DeliveryAssigning: true, DeliveryAssigned: true,
		PickedUp: true, OutForDelivery: true, Delivered: true,
	}
	all := []string{
		Draft, Placed, PaymentPending, PaymentFailed, Confirmed, RestaurantRejected, Preparing,
		ReadyForPickup, DeliveryAssigning, DeliveryAssigned, PickedUp, OutForDelivery, Delivered,
		CancelledByCustomer, CancelledByRestaurant, CancelledByAdmin, RefundPending, Refunded, Failed,
	}
	for _, from := range all {
		t.Run(from, func(t *testing.T) {
			err := CustomerCancel(from)
			switch {
			case allowed[from]:
				if err != nil {
					t.Fatalf("customer cancel from %s refused: %v", from, err)
				}
				if !CustomerCanCancel(from) {
					t.Fatalf("can_cancel false for %s", from)
				}
			case afterPreparing[from]:
				if !errors.Is(err, ErrCustomerCancelAfterPreparing) {
					t.Fatalf("customer cancel from %s: want ErrCustomerCancelAfterPreparing, got %v", from, err)
				}
				if CustomerCanCancel(from) {
					t.Fatalf("can_cancel true for %s", from)
				}
			default:
				if !errors.Is(err, ErrTransitionNotAllowed) || errors.Is(err, ErrCustomerCancelAfterPreparing) {
					t.Fatalf("customer cancel from %s: want plain ErrTransitionNotAllowed, got %v", from, err)
				}
				if CustomerCanCancel(from) {
					t.Fatalf("can_cancel true for %s", from)
				}
			}
		})
	}
}

// The table, not just the guard, refuses the customer from every
// kitchen-started status: no edge may reappear behind the guard's back.
func TestNoCustomerCancelEdgeAfterKitchenStarts(t *testing.T) {
	for from := range kitchenStarted {
		if Validate(ActorCustomer, from, CancelledByCustomer) == nil {
			t.Fatalf("table lets the customer cancel from %s", from)
		}
	}
	for e, a := range table {
		if e.to != CancelledByCustomer {
			continue
		}
		if _, ok := a[ActorCustomer]; ok && e.from != Placed && e.from != Confirmed {
			t.Fatalf("unexpected customer cancel edge from %s", e.from)
		}
	}
}
