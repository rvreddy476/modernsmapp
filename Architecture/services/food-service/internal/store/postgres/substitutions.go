package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Substitution is one row in food.order_substitutions.
type Substitution struct {
	ID                uuid.UUID  `json:"id"`
	OrderID           uuid.UUID  `json:"order_id"`
	OriginalItemID    uuid.UUID  `json:"original_item_id"`
	OriginalItemName  string     `json:"original_item_name"`
	SuggestedItemID   *uuid.UUID `json:"suggested_item_id,omitempty"`
	SuggestedItemName *string    `json:"suggested_item_name,omitempty"`
	PriceDiff         float64    `json:"price_diff"`
	Note              *string    `json:"note,omitempty"`
	Status            string     `json:"status"`
	ProposedBy        uuid.UUID  `json:"proposed_by"`
	RespondedBy       *uuid.UUID `json:"responded_by,omitempty"`
	RespondedAt       *string    `json:"responded_at,omitempty"`
	CreatedAt         string     `json:"created_at"`
}

// ProposeSubstitutionInput is the request body for partner-side
// substitution proposal.
type ProposeSubstitutionInput struct {
	OrderID           uuid.UUID
	OriginalItemID    uuid.UUID
	SuggestedItemID   *uuid.UUID
	SuggestedItemName *string
	PriceDiff         float64
	Note              *string
	ProposedBy        uuid.UUID
}

// ProposeSubstitution inserts a substitution proposal. The partner
// must own the restaurant for the order — verified inside the tx.
func (s *Store) ProposeSubstitution(ctx context.Context, ownerID uuid.UUID, in ProposeSubstitutionInput) (*Substitution, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var orderRestaurantID uuid.UUID
	var orderStatus string
	if err := tx.QueryRow(ctx, `
		SELECT restaurant_id, status::text
		FROM food.orders
		WHERE id = $1
		FOR UPDATE
	`, in.OrderID).Scan(&orderRestaurantID, &orderStatus); err != nil {
		return nil, err
	}
	var owned int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
	`, orderRestaurantID, ownerID).Scan(&owned); err != nil {
		return nil, err
	}
	if owned == 0 {
		return nil, pgx.ErrNoRows
	}
	if orderStatus != "CONFIRMED" && orderStatus != "PREPARING" {
		return nil, fmt.Errorf("cannot substitute in status %s", orderStatus)
	}
	var originalName string
	if err := tx.QueryRow(ctx, `
		SELECT item_name_snapshot FROM food.order_items
		WHERE order_id = $1 AND id = $2
	`, in.OrderID, in.OriginalItemID).Scan(&originalName); err != nil {
		return nil, fmt.Errorf("original item lookup: %w", err)
	}
	var sub Substitution
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.order_substitutions
			(order_id, original_item_id, original_item_name,
			 suggested_item_id, suggested_item_name, price_diff, note, proposed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, order_id, original_item_id, original_item_name,
			suggested_item_id, suggested_item_name, price_diff, note, status::text,
			proposed_by, responded_by, responded_at::text, created_at::text
	`, in.OrderID, in.OriginalItemID, originalName,
		in.SuggestedItemID, in.SuggestedItemName, in.PriceDiff, in.Note, in.ProposedBy,
	).Scan(&sub.ID, &sub.OrderID, &sub.OriginalItemID, &sub.OriginalItemName,
		&sub.SuggestedItemID, &sub.SuggestedItemName, &sub.PriceDiff, &sub.Note,
		&sub.Status, &sub.ProposedBy, &sub.RespondedBy, &sub.RespondedAt, &sub.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &sub, nil
}

// RespondToSubstitution sets status to approved/declined/cancelled.
// `customerID` ownership of the order is enforced.
func (s *Store) RespondToSubstitution(ctx context.Context, customerID, subID uuid.UUID, newStatus string) (*Substitution, error) {
	if newStatus != "approved" && newStatus != "declined" && newStatus != "cancelled" {
		return nil, fmt.Errorf("invalid response status: %s", newStatus)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var orderID uuid.UUID
	var currentStatus string
	if err := tx.QueryRow(ctx, `
		SELECT s.order_id, s.status::text
		FROM food.order_substitutions s
		JOIN food.orders o ON o.id = s.order_id
		WHERE s.id = $1 AND o.user_id = $2
		FOR UPDATE OF s
	`, subID, customerID).Scan(&orderID, &currentStatus); err != nil {
		return nil, err
	}
	if currentStatus != "proposed" {
		return nil, fmt.Errorf("substitution already responded: %s", currentStatus)
	}
	var sub Substitution
	if err := tx.QueryRow(ctx, `
		UPDATE food.order_substitutions
		SET status = $2::food.substitution_status,
			responded_by = $3,
			responded_at = NOW()
		WHERE id = $1
		RETURNING id, order_id, original_item_id, original_item_name,
			suggested_item_id, suggested_item_name, price_diff, note, status::text,
			proposed_by, responded_by, responded_at::text, created_at::text
	`, subID, newStatus, customerID,
	).Scan(&sub.ID, &sub.OrderID, &sub.OriginalItemID, &sub.OriginalItemName,
		&sub.SuggestedItemID, &sub.SuggestedItemName, &sub.PriceDiff, &sub.Note,
		&sub.Status, &sub.ProposedBy, &sub.RespondedBy, &sub.RespondedAt, &sub.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &sub, nil
}

// ListSubstitutions returns every substitution for an order. Either
// the customer or the owning partner can read.
func (s *Store) ListSubstitutions(ctx context.Context, userID, orderID uuid.UUID) ([]Substitution, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id, s.order_id, s.original_item_id, s.original_item_name,
			s.suggested_item_id, s.suggested_item_name, s.price_diff, s.note,
			s.status::text, s.proposed_by, s.responded_by, s.responded_at::text, s.created_at::text
		FROM food.order_substitutions s
		JOIN food.orders o ON o.id = s.order_id
		WHERE s.order_id = $1
		  AND (o.user_id = $2 OR EXISTS (
		    SELECT 1 FROM food.restaurants r
		    WHERE r.id = o.restaurant_id AND r.owner_user_id = $2
		  ))
		ORDER BY s.created_at DESC
	`, orderID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Substitution
	for rows.Next() {
		var sub Substitution
		if err := rows.Scan(&sub.ID, &sub.OrderID, &sub.OriginalItemID, &sub.OriginalItemName,
			&sub.SuggestedItemID, &sub.SuggestedItemName, &sub.PriceDiff, &sub.Note,
			&sub.Status, &sub.ProposedBy, &sub.RespondedBy, &sub.RespondedAt, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// LogStockoutChange appends to the audit log when a partner toggles
// item availability. Called from SetMenuItemAvailability inside the
// same transaction.
func (s *Store) LogStockoutChange(ctx context.Context, tx pgx.Tx, menuItemID, restaurantID, changedBy uuid.UUID, isAvailable bool, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO food.menu_item_stockouts
			(menu_item_id, restaurant_id, is_available, reason, changed_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
	`, menuItemID, restaurantID, isAvailable, reason, changedBy)
	return err
}
