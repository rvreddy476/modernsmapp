package service

import "fmt"

const (
	OrderStatusDraft                 = "DRAFT"
	OrderStatusPlaced                = "PLACED"
	OrderStatusPaymentPending        = "PAYMENT_PENDING"
	OrderStatusPaymentFailed         = "PAYMENT_FAILED"
	OrderStatusConfirmed             = "CONFIRMED"
	OrderStatusRestaurantRejected    = "RESTAURANT_REJECTED"
	OrderStatusPreparing             = "PREPARING"
	OrderStatusReadyForPickup        = "READY_FOR_PICKUP"
	OrderStatusDeliveryAssigning     = "DELIVERY_ASSIGNING"
	OrderStatusDeliveryAssigned      = "DELIVERY_ASSIGNED"
	OrderStatusPickedUp              = "PICKED_UP"
	OrderStatusOutForDelivery        = "OUT_FOR_DELIVERY"
	OrderStatusDelivered             = "DELIVERED"
	OrderStatusCancelledByCustomer   = "CANCELLED_BY_CUSTOMER"
	OrderStatusCancelledByRestaurant = "CANCELLED_BY_RESTAURANT"
	OrderStatusCancelledByAdmin      = "CANCELLED_BY_ADMIN"
	OrderStatusRefundPending         = "REFUND_PENDING"
	OrderStatusRefunded              = "REFUNDED"
	OrderStatusFailed                = "FAILED"
)

var validOrderTransitions = map[string]map[string]struct{}{
	OrderStatusDraft: {
		OrderStatusPlaced: {},
	},
	OrderStatusPlaced: {
		OrderStatusPaymentPending:        {},
		OrderStatusConfirmed:             {},
		OrderStatusCancelledByCustomer:   {},
		OrderStatusCancelledByRestaurant: {},
		OrderStatusCancelledByAdmin:      {},
	},
	OrderStatusPaymentPending: {
		OrderStatusPaymentFailed: {},
		OrderStatusConfirmed:     {},
	},
	OrderStatusConfirmed: {
		OrderStatusPreparing:             {},
		OrderStatusRestaurantRejected:    {},
		OrderStatusCancelledByCustomer:   {},
		OrderStatusCancelledByRestaurant: {},
		OrderStatusCancelledByAdmin:      {},
	},
	OrderStatusPreparing: {
		OrderStatusReadyForPickup:      {},
		OrderStatusCancelledByCustomer: {},
		OrderStatusCancelledByAdmin:    {},
	},
	OrderStatusReadyForPickup: {
		OrderStatusDeliveryAssigning: {},
		OrderStatusCancelledByAdmin:  {},
	},
	OrderStatusDeliveryAssigning: {
		OrderStatusDeliveryAssigned: {},
		OrderStatusCancelledByAdmin: {},
	},
	OrderStatusDeliveryAssigned: {
		OrderStatusPickedUp:         {},
		OrderStatusCancelledByAdmin: {},
	},
	OrderStatusPickedUp: {
		OrderStatusOutForDelivery:   {},
		OrderStatusCancelledByAdmin: {},
	},
	OrderStatusOutForDelivery: {
		OrderStatusDelivered:        {},
		OrderStatusCancelledByAdmin: {},
	},
	OrderStatusCancelledByCustomer: {
		OrderStatusRefundPending: {},
	},
	OrderStatusCancelledByRestaurant: {
		OrderStatusRefundPending: {},
	},
	OrderStatusCancelledByAdmin: {
		OrderStatusRefundPending: {},
	},
	OrderStatusRefundPending: {
		OrderStatusRefunded: {},
	},
}

func ValidateOrderTransition(from, to string) error {
	allowed, ok := validOrderTransitions[from]
	if !ok {
		return fmt.Errorf("no transitions allowed from %s", from)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("invalid order status transition: %s -> %s", from, to)
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
