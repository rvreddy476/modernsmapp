package postgres

import (
	"testing"

	"github.com/atpost/food-service/internal/orderstate"
)

var allAssignmentStatuses = []string{
	"CREATED", "ASSIGNED", "ACCEPTED", "ARRIVED_AT_RESTAURANT", "PICKED_UP",
	"ARRIVED_AT_CUSTOMER", "DELIVERED", "FAILED", "CANCELLED", "REJECTED",
}

var allOrderStatuses = []string{
	orderstate.Draft, orderstate.Placed, orderstate.PaymentPending, orderstate.PaymentFailed,
	orderstate.Confirmed, orderstate.RestaurantRejected, orderstate.Preparing, orderstate.ReadyForPickup,
	orderstate.DeliveryAssigning, orderstate.DeliveryAssigned, orderstate.PickedUp, orderstate.OutForDelivery,
	orderstate.Delivered, orderstate.CancelledByCustomer, orderstate.CancelledByRestaurant,
	orderstate.CancelledByAdmin, orderstate.RefundPending, orderstate.Refunded, orderstate.Failed,
}

// The rider's position reaches the customer only between the rider accepting
// the job and delivery: never on a merely assigned job, never after delivery,
// never on a cancelled or refunded order.
func TestRiderLocationShareableWindow(t *testing.T) {
	allowed := map[[2]string]bool{}
	for _, a := range []string{"ACCEPTED", "ARRIVED_AT_RESTAURANT", "PICKED_UP", "ARRIVED_AT_CUSTOMER"} {
		for _, o := range []string{orderstate.DeliveryAssigned, orderstate.PickedUp, orderstate.OutForDelivery} {
			allowed[[2]string{a, o}] = true
		}
	}
	for _, a := range allAssignmentStatuses {
		for _, o := range allOrderStatuses {
			if got := RiderLocationShareable(a, o); got != allowed[[2]string{a, o}] {
				t.Errorf("RiderLocationShareable(%s, %s) = %v", a, o, got)
			}
		}
	}
	for _, c := range [][2]string{
		{"ASSIGNED", orderstate.DeliveryAssigned},  // offer accepted, job not yet accepted
		{"DELIVERED", orderstate.Delivered},        // after delivery
		{"PICKED_UP", orderstate.CancelledByAdmin}, // admin cancel leaves the row as it was
		{"ARRIVED_AT_CUSTOMER", orderstate.RefundPending},
	} {
		if RiderLocationShareable(c[0], c[1]) {
			t.Errorf("shared outside the window: %v", c)
		}
	}
}

// pickup_code: only after the rider accepted, only until pickup.
func TestPickupCodeVisibleWindow(t *testing.T) {
	for _, a := range allAssignmentStatuses {
		for _, o := range allOrderStatuses {
			want := (a == "ACCEPTED" || a == "ARRIVED_AT_RESTAURANT") && o == orderstate.DeliveryAssigned
			if got := PickupCodeVisible(a, o); got != want {
				t.Errorf("PickupCodeVisible(%s, %s) = %v, want %v", a, o, got, want)
			}
		}
	}
	if PickupCodeVisible("ASSIGNED", orderstate.DeliveryAssigned) {
		t.Fatal("pickup_code visible before the rider accepted")
	}
}

// delivery_code: only while the food is with the rider.
func TestDeliveryCodeVisibleWindow(t *testing.T) {
	for _, o := range allOrderStatuses {
		want := o == orderstate.PickedUp || o == orderstate.OutForDelivery
		if got := DeliveryCodeVisible(o); got != want {
			t.Errorf("DeliveryCodeVisible(%s) = %v, want %v", o, got, want)
		}
	}
}
