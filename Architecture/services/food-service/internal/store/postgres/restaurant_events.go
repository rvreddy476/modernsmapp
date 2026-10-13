package postgres

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Decision and notification intent commit together, without document contents
// or administrator free-text reasons crossing the notification boundary.
func enqueueRestaurantReviewTx(ctx context.Context, tx pgx.Tx, restaurantID, documentID uuid.UUID, status string) error {
	var owner uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT owner_user_id FROM food.restaurants WHERE id=$1`, restaurantID).Scan(&owner); err != nil {
		return err
	}
	typ := "food.restaurant.status_changed"
	if documentID != uuid.Nil {
		typ = "food.restaurant.document_reviewed"
	}
	body, err := json.Marshal(map[string]any{"restaurant_id": restaurantID, "restaurant_owner_user_id": owner, "document_id": documentID, "status": status, "occurred_at": time.Now().UTC()})
	if err != nil {
		return err
	}
	return foodOutbox.Enqueue(ctx, tx, typ, "food.restaurant."+restaurantID.String(), body)
}
