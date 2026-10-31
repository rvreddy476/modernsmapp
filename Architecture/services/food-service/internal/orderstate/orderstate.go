// Package orderstate is the single source of truth for which food order
// status transitions exist and which actor may perform each one.
//
// It is a leaf package (no imports from this module) so that both the store
// (which enforces it inside transitionOrderTx) and the service layer can
// depend on it without an import cycle.
package orderstate

import (
	"errors"
	"fmt"
)

const (
	Draft                 = "DRAFT"
	Placed                = "PLACED"
	PaymentPending        = "PAYMENT_PENDING"
	PaymentFailed         = "PAYMENT_FAILED"
	Confirmed             = "CONFIRMED"
	RestaurantRejected    = "RESTAURANT_REJECTED"
	Preparing             = "PREPARING"
	ReadyForPickup        = "READY_FOR_PICKUP"
	DeliveryAssigning     = "DELIVERY_ASSIGNING"
	DeliveryAssigned      = "DELIVERY_ASSIGNED"
	PickedUp              = "PICKED_UP"
	OutForDelivery        = "OUT_FOR_DELIVERY"
	Delivered             = "DELIVERED"
	CancelledByCustomer   = "CANCELLED_BY_CUSTOMER"
	CancelledByRestaurant = "CANCELLED_BY_RESTAURANT"
	CancelledByAdmin      = "CANCELLED_BY_ADMIN"
	RefundPending         = "REFUND_PENDING"
	Refunded              = "REFUNDED"
	Failed                = "FAILED"
)

// Actor is who is asking for a transition.
type Actor string

const (
	ActorCustomer        Actor = "customer"
	ActorRestaurant      Actor = "restaurant"
	ActorDeliveryPartner Actor = "delivery_partner"
	ActorAdmin           Actor = "admin"
	// ActorSystem is a worker or an automatic chained step (SLA auto-reject,
	// READY_FOR_PICKUP -> DELIVERY_ASSIGNING).
	ActorSystem Actor = "system"
	// ActorPayment is reserved for the payment writers.
	ActorPayment Actor = "payment"
)

// ErrTransitionNotAllowed is returned (wrapped) when the edge does not exist
// or the actor may not perform it.
var ErrTransitionNotAllowed = errors.New("order status transition not allowed")

type edge struct{ from, to string }

func actors(a ...Actor) map[Actor]struct{} {
	m := make(map[Actor]struct{}, len(a))
	for _, x := range a {
		m[x] = struct{}{}
	}
	return m
}

// table is every allowed edge and the actors allowed to take it.
var table = map[edge]map[Actor]struct{}{
	{Draft, Placed}: actors(ActorCustomer),

	// Payment edges. ActorPayment is the payment-event consumer (a verified
	// payment.succeeded / payment.failed) and the payment-intent writer; it
	// can never reach any other status (TestPaymentActorEdgesAreExactly).
	{Placed, PaymentPending}:        actors(ActorPayment),
	{Placed, Confirmed}:             actors(ActorPayment),
	{Placed, PaymentFailed}:         actors(ActorPayment),
	{Placed, CancelledByCustomer}:   actors(ActorCustomer),
	{Placed, CancelledByRestaurant}: actors(ActorRestaurant),
	{Placed, CancelledByAdmin}:      actors(ActorAdmin),

	{PaymentPending, PaymentFailed}: actors(ActorPayment),
	{PaymentPending, Confirmed}:     actors(ActorPayment),
	// Cancelling an unpaid order is ordinary ops work.
	{PaymentPending, CancelledByAdmin}: actors(ActorAdmin),

	// A capture after a failure on the same intent is a legitimate retry, and
	// a fresh intent reopens the payment.
	{PaymentFailed, Confirmed}:        actors(ActorPayment),
	{PaymentFailed, PaymentPending}:   actors(ActorPayment),
	{PaymentFailed, CancelledByAdmin}: actors(ActorAdmin),

	{Confirmed, Preparing}:             actors(ActorRestaurant),
	{Confirmed, RestaurantRejected}:    actors(ActorRestaurant, ActorSystem),
	{Confirmed, CancelledByCustomer}:   actors(ActorCustomer),
	{Confirmed, CancelledByRestaurant}: actors(ActorRestaurant),
	{Confirmed, CancelledByAdmin}:      actors(ActorAdmin),

	// Audit M5 (founder, option a): the customer's own cancel ends at
	// CONFIRMED. Once the kitchen has started (PREPARING or later) there is
	// no customer edge; the customer raises a support refund request and an
	// admin decides the amount (CustomerCancel names the refusal).
	{Preparing, ReadyForPickup}:   actors(ActorRestaurant),
	{Preparing, CancelledByAdmin}: actors(ActorAdmin),

	{ReadyForPickup, DeliveryAssigning}: actors(ActorSystem),
	{ReadyForPickup, CancelledByAdmin}:  actors(ActorAdmin),

	{DeliveryAssigning, DeliveryAssigned}: actors(ActorDeliveryPartner),
	{DeliveryAssigning, CancelledByAdmin}: actors(ActorAdmin),

	// Pickup completes only through the restaurant-side OTP verify.
	{DeliveryAssigned, PickedUp}: actors(ActorRestaurant),
	// The assigned partner releases the job; the order goes back to the queue.
	// Audit L4: the system releases the job of a rider silent before pickup.
	{DeliveryAssigned, DeliveryAssigning}: actors(ActorDeliveryPartner, ActorSystem),
	{DeliveryAssigned, CancelledByAdmin}:  actors(ActorAdmin),

	{PickedUp, OutForDelivery}:   actors(ActorDeliveryPartner, ActorSystem),
	{PickedUp, CancelledByAdmin}: actors(ActorAdmin),

	// Delivery completes only through the delivery-code verify: the customer
	// shows the code and the rider holding the assignment enters it (B5c). The
	// customer can no longer mark their own order delivered.
	{OutForDelivery, Delivered}:        actors(ActorDeliveryPartner),
	{OutForDelivery, CancelledByAdmin}: actors(ActorAdmin),

	// Refunds: an admin REQUESTS a full refund of a paid order that is no longer
	// being fulfilled (or was delivered); only the payment.refunded event
	// finalises it. An order still in the kitchen or on the road is cancelled
	// first.
	// B4 follow-up: a customer's cancellation of a paid order has its refund
	// requested by the system in the cancelling transaction.
	{CancelledByCustomer, RefundPending}:   actors(ActorAdmin, ActorSystem),
	{CancelledByRestaurant, RefundPending}: actors(ActorAdmin),
	{CancelledByAdmin, RefundPending}:      actors(ActorAdmin),
	// B3: the system (SLA worker, or the chained step after a restaurant's
	// rejection) requests the refund of a paid rejected order in the same flow,
	// so a paid order never rests in RESTAURANT_REJECTED.
	{RestaurantRejected, RefundPending}: actors(ActorAdmin, ActorSystem),
	{Delivered, RefundPending}:          actors(ActorAdmin),
	{RefundPending, Refunded}:           actors(ActorPayment),
}

// ErrCustomerCancelAfterPreparing is returned when a customer asks to cancel
// an order the restaurant has already started preparing (audit M5). The
// customer is directed to a support refund request instead. HTTP 409
// FOOD_CANCEL_AFTER_PREPARING.
var ErrCustomerCancelAfterPreparing = errors.New("the restaurant has started preparing this order; request a refund through support instead")

// kitchenStarted are the statuses the restaurant has moved an order into once
// the food is being made, up to and including delivery. A customer cancel
// from any of them is refused with ErrCustomerCancelAfterPreparing.
var kitchenStarted = map[string]bool{
	Preparing:         true,
	ReadyForPickup:    true,
	DeliveryAssigning: true,
	DeliveryAssigned:  true,
	PickedUp:          true,
	OutForDelivery:    true,
	Delivered:         true,
}

// CustomerCancel is the guard for a customer's own cancel from `from`. It
// returns ErrCustomerCancelAfterPreparing once the kitchen has started, and
// otherwise whatever the transition table says (nil from PLACED and
// CONFIRMED; ErrTransitionNotAllowed from unpaid or already-ended orders).
func CustomerCancel(from string) error {
	if kitchenStarted[from] {
		return fmt.Errorf("%w (order is %s)", ErrCustomerCancelAfterPreparing, from)
	}
	return Validate(ActorCustomer, from, CancelledByCustomer)
}

// CustomerCanCancel reports whether the customer may cancel an order in
// `status` themselves. Clients read it as can_cancel to show or hide the
// cancel button.
func CustomerCanCancel(status string) bool {
	return CustomerCancel(status) == nil
}

// EdgeExists reports whether from -> to is a transition at all, regardless of
// actor.
func EdgeExists(from, to string) bool {
	_, ok := table[edge{from, to}]
	return ok
}

// Validate returns nil only when from -> to exists and actor may take it.
func Validate(actor Actor, from, to string) error {
	allowed, ok := table[edge{from, to}]
	if !ok {
		return fmt.Errorf("%w: %s -> %s", ErrTransitionNotAllowed, from, to)
	}
	if _, ok := allowed[actor]; !ok {
		return fmt.Errorf("%w: %s may not move an order %s -> %s", ErrTransitionNotAllowed, actor, from, to)
	}
	return nil
}

// IsCancellation reports whether reaching `to` ends the order before delivery
// (so the writer stamps cancelled_at).
func IsCancellation(to string) bool {
	switch to {
	case CancelledByCustomer, CancelledByRestaurant, CancelledByAdmin, RestaurantRejected:
		return true
	}
	return false
}
