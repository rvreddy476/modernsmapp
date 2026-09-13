package postgres

import (
	"context"
	"fmt"

	"github.com/atpost/food-service/internal/foodevents"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// foodOutbox writes food.outbox_events rows inside the caller's transaction.
// The outbox publisher (cmd/server) ships them to Kafka food-events.
var foodOutbox = outbox.NewQueuer("food")

// Events written outside transitionOrderTx's default edge mapping.
const (
	eventOrderPlaced      = foodevents.OrderPlaced
	eventPaymentSucceeded = foodevents.OrderPaymentSucceeded
	eventRefunded         = foodevents.OrderRefunded
	eventRefundRequested  = foodevents.OrderRefundRequested
)

// enqueueOrderEventTx writes one order lifecycle event in tx. It reads the
// order header in the same transaction, so the row commits (or rolls back)
// with the change it announces and always names the customer and the kitchen.
func enqueueOrderEventTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, eventType, previous string) error {
	h := foodevents.OrderHeader{OrderID: orderID}
	var owner *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT o.order_number, o.user_id, o.restaurant_id, r.owner_user_id, o.status::text
		FROM food.orders o
		LEFT JOIN food.restaurants r ON r.id = o.restaurant_id
		WHERE o.id = $1
	`, orderID).Scan(&h.OrderNumber, &h.UserID, &h.RestaurantID, &owner, &h.Status); err != nil {
		return fmt.Errorf("read order for %s event: %w", eventType, err)
	}
	if owner != nil {
		h.RestaurantOwnerUserID = *owner
	}
	body, err := foodevents.Marshal(eventType, foodevents.NewOrderEvent(h, previous))
	if err != nil {
		return err
	}
	if err := foodOutbox.Enqueue(ctx, tx, eventType, foodevents.OrderTopic(orderID), body); err != nil {
		return fmt.Errorf("enqueue %s: %w", eventType, err)
	}
	return nil
}

// closeOpenAssignmentsTx runs in the transaction that ends an order before
// delivery (any cancellation, a rejection). The order's assignment is marked
// CANCELLED, so it stops counting as active for location pings, the rider's
// current job, presence and the verifies; pending offers are superseded so no
// rider can accept work on an order that no longer exists.
func closeOpenAssignmentsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET status = 'CANCELLED', cancelled_at = NOW()
		WHERE order_id = $1 AND status NOT IN ('DELIVERED', 'FAILED', 'CANCELLED')
	`, orderID); err != nil {
		return fmt.Errorf("close delivery assignment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_offers
		SET status = 'superseded', responded_at = NOW()
		WHERE order_id = $1 AND status = 'pending'
	`, orderID); err != nil {
		return fmt.Errorf("supersede delivery offers: %w", err)
	}
	return nil
}
