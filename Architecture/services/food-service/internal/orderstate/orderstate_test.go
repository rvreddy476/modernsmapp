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
		{"payment confirms from placed", ActorPayment, Placed, Confirmed, true},
		{"payment confirms after a failed attempt", ActorPayment, PaymentFailed, Confirmed, true},
		{"payment marks failed from placed", ActorPayment, Placed, PaymentFailed, true},
		{"payment marks failed from pending", ActorPayment, PaymentPending, PaymentFailed, true},
		{"a new intent reopens a failed payment", ActorPayment, PaymentFailed, PaymentPending, true},
		{"admin cannot confirm payment", ActorAdmin, PaymentPending, Confirmed, false},
		{"system cannot confirm payment", ActorSystem, PaymentPending, Confirmed, false},
		{"payment cannot revive a cancelled order", ActorPayment, CancelledByAdmin, Confirmed, false},
		{"payment cannot revive a rejected order", ActorPayment, RestaurantRejected, Confirmed, false},
		{"payment finalises a refund", ActorPayment, RefundPending, Refunded, true},
		{"admin cannot finalise a refund", ActorAdmin, RefundPending, Refunded, false},
		{"admin requests refund on a rejected order", ActorAdmin, RestaurantRejected, RefundPending, true},
		// B3: a paid order rejected by the SLA worker or the restaurant has its
		// refund requested in the same flow by the system.
		{"system requests refund on a rejected order", ActorSystem, RestaurantRejected, RefundPending, true},
		// B4 follow-up: a customer's cancellation of a paid order requests its
		// refund in the same flow. Admin and restaurant cancellations do not.
		{"system requests refund on a customer cancellation", ActorSystem, CancelledByCustomer, RefundPending, true},
		{"system cannot request refund on an admin cancellation", ActorSystem, CancelledByAdmin, RefundPending, false},
		{"system cannot request refund on a restaurant cancellation", ActorSystem, CancelledByRestaurant, RefundPending, false},
		{"customer cannot request a refund", ActorCustomer, CancelledByCustomer, RefundPending, false},
		{"system cannot finalise a refund", ActorSystem, RefundPending, Refunded, false},
		{"restaurant cannot request a refund", ActorRestaurant, RestaurantRejected, RefundPending, false},
		{"admin requests refund after delivery", ActorAdmin, Delivered, RefundPending, true},
		{"admin requests refund on a cancelled order", ActorAdmin, CancelledByCustomer, RefundPending, true},
		{"payment cannot request a refund", ActorPayment, CancelledByAdmin, RefundPending, false},
		{"admin cannot refund an order still in the kitchen", ActorAdmin, Preparing, RefundPending, false},
		{"admin cancels an unpaid pending order", ActorAdmin, PaymentPending, CancelledByAdmin, true},
		{"admin cancels a failed payment", ActorAdmin, PaymentFailed, CancelledByAdmin, true},
		{"admin cannot cancel a rejected order", ActorAdmin, RestaurantRejected, CancelledByAdmin, false},
		{"admin cannot cancel a refund in flight", ActorAdmin, RefundPending, CancelledByAdmin, false},
		{"admin cannot cancel a failed order", ActorAdmin, Failed, CancelledByAdmin, false},
		{"admin cannot cancel a draft", ActorAdmin, Draft, CancelledByAdmin, false},
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
