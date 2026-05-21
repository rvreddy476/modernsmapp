package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ItemReview mirrors food.item_reviews.
type ItemReview struct {
	ID         uuid.UUID `json:"id"`
	OrderID    uuid.UUID `json:"order_id"`
	MenuItemID uuid.UUID `json:"menu_item_id"`
	CustomerID uuid.UUID `json:"customer_id"`
	Rating     int       `json:"rating"`
	Review     *string   `json:"review,omitempty"`
	PhotoURLs  []string  `json:"photo_urls"`
	CreatedAt  string    `json:"created_at"`
}

// CreateItemReviewInput captures one review row.
type CreateItemReviewInput struct {
	OrderID    uuid.UUID
	MenuItemID uuid.UUID
	CustomerID uuid.UUID
	Rating     int
	Review     string
	PhotoURLs  []string
}

// CreateItemReview inserts the review and updates the menu_items
// aggregate (avg_rating + rating_count) atomically. Constraints:
//
//   - Order must be DELIVERED and belong to the customer.
//   - Item must be one of the order's order_items.
//   - One review per (order, item, customer).
func (s *Store) CreateItemReview(ctx context.Context, in CreateItemReviewInput) (*ItemReview, error) {
	if in.Rating < 1 || in.Rating > 5 {
		return nil, fmt.Errorf("rating must be 1..5")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT status::text FROM food.orders WHERE id = $1 AND user_id = $2
	`, in.OrderID, in.CustomerID).Scan(&status); err != nil {
		return nil, err
	}
	if status != "DELIVERED" {
		return nil, fmt.Errorf("can only review delivered orders")
	}
	var itemInOrder int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.order_items
		WHERE order_id = $1 AND menu_item_id = $2
	`, in.OrderID, in.MenuItemID).Scan(&itemInOrder); err != nil {
		return nil, err
	}
	if itemInOrder == 0 {
		return nil, fmt.Errorf("item not present in order")
	}
	photos, err := json.Marshal(in.PhotoURLs)
	if err != nil {
		return nil, err
	}
	var r ItemReview
	var photosOut []byte
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.item_reviews (order_id, menu_item_id, customer_id, rating, review, photo_urls)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
		RETURNING id, order_id, menu_item_id, customer_id, rating, review, photo_urls, created_at::text
	`, in.OrderID, in.MenuItemID, in.CustomerID, in.Rating, in.Review, photos).Scan(
		&r.ID, &r.OrderID, &r.MenuItemID, &r.CustomerID, &r.Rating, &r.Review,
		&photosOut, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	if len(photosOut) > 0 {
		_ = json.Unmarshal(photosOut, &r.PhotoURLs)
	}
	// Recompute aggregate.
	if _, err := tx.Exec(ctx, `
		UPDATE food.menu_items
		SET rating_count = sub.cnt,
			avg_rating   = sub.avg
		FROM (
			SELECT COUNT(*)::int AS cnt, ROUND(AVG(rating)::numeric, 2) AS avg
			FROM food.item_reviews WHERE menu_item_id = $1
		) sub
		WHERE id = $1
	`, in.MenuItemID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListItemReviews returns reviews for one menu item (newest first).
func (s *Store) ListItemReviews(ctx context.Context, menuItemID uuid.UUID, limit int) ([]ItemReview, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, menu_item_id, customer_id, rating, review, photo_urls, created_at::text
		FROM food.item_reviews
		WHERE menu_item_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, menuItemID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ItemReview
	for rows.Next() {
		var r ItemReview
		var photos []byte
		if err := rows.Scan(&r.ID, &r.OrderID, &r.MenuItemID, &r.CustomerID,
			&r.Rating, &r.Review, &photos, &r.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(photos, &r.PhotoURLs)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		// Ensure caller sees [] instead of nil for JSON ergonomics.
		out = []ItemReview{}
	}
	return out, nil
}

// HideItemReview is an admin moderation knob — useful when a review
// includes abuse. v1 just deletes; future versions can soft-hide.
func (s *Store) HideItemReview(ctx context.Context, reviewID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM food.item_reviews WHERE id = $1`, reviewID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
