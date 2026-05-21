// Food (FiGo) event handlers — mirrors rider_consumer.go.
//
// Consumer routes food.* events to the notification graph so FCM pushes
// land on the customer's device for order milestones (placed,
// payment_succeeded, ready_for_pickup, delivered, cancelled, refunded).
// Restaurant-partner + admin pushes go through the same path; the app
// shell renders them differently based on the deep-link prefix.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// foodOrderPayload mirrors the JSON the food-service emit() sends on
// food.order.* events. The shape is a subset of postgres.Order so we
// only carry what notification rendering needs.
type foodOrderPayload struct {
	ID             string    `json:"id"`
	OrderNumber    string    `json:"order_number"`
	UserID         string    `json:"user_id"`
	RestaurantID   string    `json:"restaurant_id"`
	RestaurantName string    `json:"restaurant_name"`
	Status         string    `json:"status"`
	PaymentStatus  string    `json:"payment_status"`
	PlacedAt       string    `json:"placed_at"`
	DeliveredAt    string    `json:"delivered_at,omitempty"`
	OccurredAt     time.Time `json:"emitted_at,omitempty"`
}

// handleFoodEvent is the dispatch entry point invoked from the main
// consumer's processMessage when no other handler claimed the event.
// Returns true when the event was claimed.
func (c *Consumer) handleFoodEvent(ctx context.Context, envelope events.EventEnvelope) (bool, error) {
	switch envelope.EventType {
	case events.EventFoodOrderPlaced,
		events.EventFoodOrderPaymentSucceeded,
		events.EventFoodOrderPaymentFailed,
		events.EventFoodOrderConfirmed,
		events.EventFoodOrderRestaurantAccepted,
		events.EventFoodOrderRestaurantRejected,
		events.EventFoodOrderPreparing,
		events.EventFoodOrderReadyForPickup,
		events.EventFoodDeliveryAssigned,
		events.EventFoodDeliveryPickedUp,
		events.EventFoodDeliveryDelivered,
		events.EventFoodOrderCancelled,
		events.EventFoodOrderRefundRequested,
		events.EventFoodOrderRefunded:
		return true, c.handleFoodOrder(ctx, envelope.EventType, envelope.Payload)
	case events.EventFoodRestaurantCreated,
		events.EventFoodRestaurantApproved,
		events.EventFoodRestaurantRejected,
		events.EventFoodDeliveryPartnerCreated,
		events.EventFoodDeliveryPartnerApproved:
		// Restaurant / partner lifecycle pushes go to the owner. The
		// payload shape is different — out of scope for the order path.
		return true, nil
	}
	return false, nil
}

// handleFoodOrder is the unified handler for food.order.* events.
// Recipient = order owner (customer). Restaurant-owner + admin pushes
// arrive via the SSE/realtime path; this handler dispatches the
// device-FCM copy.
func (c *Consumer) handleFoodOrder(ctx context.Context, eventType string, raw json.RawMessage) error {
	var p foodOrderPayload
	if err := unmarshalPayload(raw, &p); err != nil {
		return fmt.Errorf("food: decode %s: %w", eventType, err)
	}
	customerID, err := uuid.Parse(p.UserID)
	if err != nil {
		return fmt.Errorf("food: invalid user_id in %s: %w", eventType, err)
	}
	orderID, err := uuid.Parse(p.ID)
	if err != nil {
		return fmt.Errorf("food: invalid order id in %s: %w", eventType, err)
	}
	occurredAt := p.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	deepLink := "/figo/orders/" + p.ID

	// Self-notification: the user receives a push for milestones on
	// their own order. CreateNotification's actor/recipient are the
	// same user; the app shell renders the title from the eventType.
	if err := c.service.CreateNotification(
		ctx, customerID, customerID,
		eventType, "food_order", orderID,
		deepLink, occurredAt,
	); err != nil {
		slog.Warn("food: notify customer failed",
			"customer_id", customerID, "order_id", p.ID, "event", eventType, "error", err)
	}
	return nil
}
