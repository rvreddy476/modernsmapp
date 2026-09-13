package service

import (
	"fmt"

	"github.com/atpost/food-service/internal/orderstate"
)

// Status names live in internal/orderstate; these aliases keep existing
// callers compiling.
const (
	OrderStatusDraft                 = orderstate.Draft
	OrderStatusPlaced                = orderstate.Placed
	OrderStatusPaymentPending        = orderstate.PaymentPending
	OrderStatusPaymentFailed         = orderstate.PaymentFailed
	OrderStatusConfirmed             = orderstate.Confirmed
	OrderStatusRestaurantRejected    = orderstate.RestaurantRejected
	OrderStatusPreparing             = orderstate.Preparing
	OrderStatusReadyForPickup        = orderstate.ReadyForPickup
	OrderStatusDeliveryAssigning     = orderstate.DeliveryAssigning
	OrderStatusDeliveryAssigned      = orderstate.DeliveryAssigned
	OrderStatusPickedUp              = orderstate.PickedUp
	OrderStatusOutForDelivery        = orderstate.OutForDelivery
	OrderStatusDelivered             = orderstate.Delivered
	OrderStatusCancelledByCustomer   = orderstate.CancelledByCustomer
	OrderStatusCancelledByRestaurant = orderstate.CancelledByRestaurant
	OrderStatusCancelledByAdmin      = orderstate.CancelledByAdmin
	OrderStatusRefundPending         = orderstate.RefundPending
	OrderStatusRefunded              = orderstate.Refunded
	OrderStatusFailed                = orderstate.Failed
)

// ValidateOrderTransition reports whether from -> to is an edge at all,
// regardless of actor. Writers use orderstate.Validate (actor-aware) inside
// the store's transitionOrderTx.
func ValidateOrderTransition(from, to string) error {
	if !orderstate.EdgeExists(from, to) {
		return fmt.Errorf("%w: %s -> %s", orderstate.ErrTransitionNotAllowed, from, to)
	}
	return nil
}

func IsTerminalOrderStatus(status string) bool {
	switch status {
	case OrderStatusDelivered,
		OrderStatusRestaurantRejected,
		OrderStatusPaymentFailed,
		OrderStatusRefunded,
		OrderStatusFailed:
		return true
	default:
		return false
	}
}
