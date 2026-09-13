// Package foodevents is the one place food-service decides what an order
// lifecycle event is called on Kafka and what its payload carries.
//
// It is a leaf (it imports only orderstate) so the store, which writes the
// outbox row inside the transition's transaction, and the service, which
// publishes the matching realtime frame, share the same names, payload shape
// and OTP guard.
//
// CONSUMER. notification-service (internal/events/food_consumer.go) is the
// only consumer of food-events. It reads recipients from payload fields only:
// restaurant_owner_user_id for the kitchen, user_id for the customer,
// delivery_partner_user_id for the rider. Every order event here carries the
// first two; the offer events built in the service carry the third.
package foodevents

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
)

// Event types, exactly as notification-service matches them.
const (
	OrderPlaced             = "food.order.placed"
	OrderPaymentSucceeded   = "food.order.payment_succeeded"
	OrderPaymentFailed      = "food.order.payment_failed"
	OrderConfirmed          = "food.order.confirmed"
	OrderRestaurantAccepted = "food.order.restaurant_accepted"
	OrderRestaurantRejected = "food.order.restaurant_rejected"
	OrderPreparing          = "food.order.preparing"
	OrderReadyForPickup     = "food.order.ready_for_pickup"
	DeliveryOffered         = "food.delivery.offered"
	DeliveryAssigned        = "food.delivery.assigned"
	DeliveryPickedUp        = "food.delivery.picked_up"
	DeliveryDelivered       = "food.delivery.delivered"
	OrderCancelled          = "food.order.cancelled"
	OrderRefundRequested    = "food.order.refund_requested"
	OrderRefunded           = "food.order.refunded"
)

// OrderTopic is the order's realtime topic, also used as the outbox partition
// key so one order's events keep their order on Kafka.
func OrderTopic(orderID uuid.UUID) string { return "food.order." + orderID.String() }

// ForTransition is the event announcing from -> to, or "" when the edge is not
// announced (PLACED, PAYMENT_PENDING, DELIVERY_ASSIGNING, OUT_FOR_DELIVERY).
//
// CONFIRMED -> PREPARING is the restaurant's accept: the state machine has no
// separate "accepted" status, so the accept is announced once, as
// restaurant_accepted, and food.order.preparing is not emitted (it would be a
// second push for the same tap).
func ForTransition(from, to string) string {
	switch to {
	case orderstate.Confirmed:
		return OrderConfirmed
	case orderstate.Preparing:
		if from == orderstate.Confirmed {
			return OrderRestaurantAccepted
		}
		return OrderPreparing
	case orderstate.ReadyForPickup:
		return OrderReadyForPickup
	case orderstate.RestaurantRejected:
		return OrderRestaurantRejected
	case orderstate.DeliveryAssigned:
		return DeliveryAssigned
	case orderstate.PickedUp:
		return DeliveryPickedUp
	case orderstate.Delivered:
		return DeliveryDelivered
	case orderstate.CancelledByCustomer, orderstate.CancelledByRestaurant, orderstate.CancelledByAdmin:
		return OrderCancelled
	case orderstate.RefundPending:
		return OrderRefundRequested
	case orderstate.PaymentFailed:
		return OrderPaymentFailed
	case orderstate.Refunded:
		return OrderRefunded
	}
	return ""
}

// OrderHeader is what the store reads, inside the transaction, to address an
// order event. Ids only: no amounts, no address, no names, no codes.
type OrderHeader struct {
	OrderID               uuid.UUID
	OrderNumber           string
	UserID                uuid.UUID
	RestaurantID          uuid.UUID
	RestaurantOwnerUserID uuid.UUID
	Status                string
}

// OrderEvent is the Kafka payload of every order lifecycle event.
type OrderEvent struct {
	ID          string `json:"id"`
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
	// UserID is the customer: food_order_status pushes go here.
	UserID       string `json:"user_id"`
	RestaurantID string `json:"restaurant_id"`
	// RestaurantOwnerUserID is the kitchen: food_order_new pushes go here.
	// There is no staff model, so restaurant_staff_user_ids is never sent.
	RestaurantOwnerUserID string `json:"restaurant_owner_user_id"`
	Status                string `json:"status"`
	// PreviousStatus is the status the transition left; empty on placed and
	// on events that are not a transition (a partial refund).
	PreviousStatus string `json:"previous_status,omitempty"`
}

// NewOrderEvent builds the payload; previous may be empty. Free-text reasons
// (a customer's or an admin's cancel note) are never carried.
func NewOrderEvent(h OrderHeader, previous string) OrderEvent {
	return OrderEvent{
		ID:                    h.OrderID.String(),
		OrderID:               h.OrderID.String(),
		OrderNumber:           h.OrderNumber,
		UserID:                idString(h.UserID),
		RestaurantID:          idString(h.RestaurantID),
		RestaurantOwnerUserID: idString(h.RestaurantOwnerUserID),
		Status:                h.Status,
		PreviousStatus:        previous,
	}
}

func idString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// otpFieldNames are the JSON keys that carry a delivery OTP. None may ever
// appear in a realtime frame or an outbox event.
var otpFieldNames = map[string]struct{}{
	"pickup_code":   {},
	"delivery_code": {},
	"pickup_otp":    {},
	"delivery_otp":  {},
}

// OTPField reports the first OTP key found anywhere in data's JSON form.
func OTPField(data any) (string, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	return findOTPField(v)
}

func findOTPField(v any) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if _, bad := otpFieldNames[k]; bad {
				return k, true
			}
			if f, bad := findOTPField(child); bad {
				return f, true
			}
		}
	case []any:
		for _, child := range t {
			if f, bad := findOTPField(child); bad {
				return f, true
			}
		}
	}
	return "", false
}

// ErrPayloadCarriesOTP refuses an event payload with an OTP key.
var ErrPayloadCarriesOTP = errors.New("event payload carries a delivery code")

// Marshal is json.Marshal that refuses a payload carrying an OTP key.
func Marshal(eventType string, data any) ([]byte, error) {
	if field, bad := OTPField(data); bad {
		return nil, fmt.Errorf("%w: %s has %s", ErrPayloadCarriesOTP, eventType, field)
	}
	return json.Marshal(data)
}
