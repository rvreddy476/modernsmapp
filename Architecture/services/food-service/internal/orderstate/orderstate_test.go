package orderstate

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		actor    Actor
		from, to string
		ok       bool
	}{
		{"restaurant accepts", ActorRestaurant, Confirmed, Preparing, true},
		{"customer cannot accept for the kitchen", ActorCustomer, Confirmed, Preparing, false},
		{"system SLA reject", ActorSystem, Confirmed, RestaurantRejected, true},
		{"ready chains to assigning by system", ActorSystem, ReadyForPickup, DeliveryAssigning, true},
		{"restaurant cannot start assigning", ActorRestaurant, ReadyForPickup, DeliveryAssigning, false},
		{"partner takes the job", ActorDeliveryPartner, DeliveryAssigning, DeliveryAssigned, true},
		{"partner releases the job", ActorDeliveryPartner, DeliveryAssigned, DeliveryAssigning, true},
		{"pickup is the restaurant OTP", ActorRestaurant, DeliveryAssigned, PickedUp, true},
		{"partner cannot self-mark pickup", ActorDeliveryPartner, DeliveryAssigned, PickedUp, false},
		{"partner marks out for delivery", ActorDeliveryPartner, PickedUp, OutForDelivery, true},
		{"delivery is the customer OTP", ActorCustomer, OutForDelivery, Delivered, true},
		{"partner cannot self-mark delivered", ActorDeliveryPartner, OutForDelivery, Delivered, false},
		{"customer cancels while preparing", ActorCustomer, Preparing, CancelledByCustomer, true},
		{"customer cannot cancel once assigned", ActorCustomer, DeliveryAssigned, CancelledByCustomer, false},
		{"admin cancels in flight", ActorAdmin, OutForDelivery, CancelledByAdmin, true},
		{"admin cannot cancel delivered", ActorAdmin, Delivered, CancelledByAdmin, false},
		{"no edge skips the kitchen", ActorAdmin, Confirmed, Delivered, false},
		{"payment confirms", ActorPayment, PaymentPending, Confirmed, true},
		{"customer cannot confirm payment", ActorCustomer, PaymentPending, Confirmed, false},
		{"unknown actor", Actor("mallory"), Confirmed, Preparing, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.actor, tc.from, tc.to)
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want refusal")
				}
				if !errors.Is(err, ErrTransitionNotAllowed) {
					t.Fatalf("refusal does not wrap ErrTransitionNotAllowed: %v", err)
				}
			}
		})
	}
}

// Every edge must name at least one actor; an edge nobody can take is a bug.
func TestEveryEdgeHasAnActor(t *testing.T) {
	for e, a := range table {
		if len(a) == 0 {
			t.Errorf("edge %s -> %s has no actor", e.from, e.to)
		}
	}
}

func TestIsCancellation(t *testing.T) {
	for _, s := range []string{CancelledByAdmin, CancelledByCustomer, CancelledByRestaurant, RestaurantRejected} {
		if !IsCancellation(s) {
			t.Errorf("%s should be a cancellation", s)
		}
	}
	if IsCancellation(Delivered) || IsCancellation(PickedUp) {
		t.Error("non-cancellation flagged")
	}
}
