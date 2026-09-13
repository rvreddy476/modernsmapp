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

	{Placed, PaymentPending}:        actors(ActorPayment),
	{Placed, Confirmed}:             actors(ActorPayment),
	{Placed, CancelledByCustomer}:   actors(ActorCustomer),
	{Placed, CancelledByRestaurant}: actors(ActorRestaurant),
	{Placed, CancelledByAdmin}:      actors(ActorAdmin),

	{PaymentPending, PaymentFailed}: actors(ActorPayment),
	{PaymentPending, Confirmed}:     actors(ActorPayment),

	{Confirmed, Preparing}:             actors(ActorRestaurant),
	{Confirmed, RestaurantRejected}:    actors(ActorRestaurant, ActorSystem),
	{Confirmed, CancelledByCustomer}:   actors(ActorCustomer),
	{Confirmed, CancelledByRestaurant}: actors(ActorRestaurant),
	{Confirmed, CancelledByAdmin}:      actors(ActorAdmin),

	{Preparing, ReadyForPickup}:      actors(ActorRestaurant),
	{Preparing, CancelledByCustomer}: actors(ActorCustomer),
	{Preparing, CancelledByAdmin}:    actors(ActorAdmin),

	{ReadyForPickup, DeliveryAssigning}: actors(ActorSystem),
	{ReadyForPickup, CancelledByAdmin}:  actors(ActorAdmin),

	{DeliveryAssigning, DeliveryAssigned}: actors(ActorDeliveryPartner),
	{DeliveryAssigning, CancelledByAdmin}: actors(ActorAdmin),

	// Pickup completes only through the restaurant-side OTP verify.
	{DeliveryAssigned, PickedUp}: actors(ActorRestaurant),
	// The assigned partner releases the job; the order goes back to the queue.
	{DeliveryAssigned, DeliveryAssigning}: actors(ActorDeliveryPartner),
	{DeliveryAssigned, CancelledByAdmin}:  actors(ActorAdmin),

	{PickedUp, OutForDelivery}:   actors(ActorDeliveryPartner, ActorSystem),
	{PickedUp, CancelledByAdmin}: actors(ActorAdmin),

	// Delivery completes only through the customer-side OTP verify.
	{OutForDelivery, Delivered}:        actors(ActorCustomer),
	{OutForDelivery, CancelledByAdmin}: actors(ActorAdmin),

	{CancelledByCustomer, RefundPending}:   actors(ActorAdmin),
	{CancelledByRestaurant, RefundPending}: actors(ActorAdmin),
	{CancelledByAdmin, RefundPending}:      actors(ActorAdmin),
	{RefundPending, Refunded}:              actors(ActorAdmin),
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
