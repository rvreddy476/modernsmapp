package postgres

import "github.com/atpost/food-service/internal/orderstate"

// B5a visibility rules: who may see a rider's live position and the two
// delivery OTPs, and when. These are the ONLY places the windows are decided;
// the store applies them before anything leaves the database layer.

// RiderLocationShareable reports whether a rider ping may be published to the
// customer's order topic: the rider has ACCEPTED the job (not merely been
// assigned it by an offer accept) and the order is still on its way. Never
// before acceptance, never after delivery, never on a cancelled order (a
// cancellation closes the assignment since B5c, and the order status is still
// checked for rows closed before then).
func RiderLocationShareable(assignmentStatus, orderStatus string) bool {
	switch assignmentStatus {
	case "ACCEPTED", "ARRIVED_AT_RESTAURANT", "PICKED_UP", "ARRIVED_AT_CUSTOMER":
	default:
		return false
	}
	switch orderStatus {
	case orderstate.DeliveryAssigned, orderstate.PickedUp, orderstate.OutForDelivery:
		return true
	}
	return false
}

// PickupCodeVisible reports whether the rider's assignment response may carry
// pickup_code: only after the rider accepted the assignment and only until the
// food is picked up.
func PickupCodeVisible(assignmentStatus, orderStatus string) bool {
	switch assignmentStatus {
	case "ACCEPTED", "ARRIVED_AT_RESTAURANT":
		return orderStatus == orderstate.DeliveryAssigned
	}
	return false
}

// DeliveryCodeVisible reports whether the customer's order detail may carry
// delivery_code: only while the food is with the rider.
func DeliveryCodeVisible(orderStatus string) bool {
	return orderStatus == orderstate.PickedUp || orderStatus == orderstate.OutForDelivery
}
