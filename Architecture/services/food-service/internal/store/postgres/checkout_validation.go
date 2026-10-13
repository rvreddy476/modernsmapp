package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrCartItemUnavailable        = errors.New("a cart item is no longer available; review your cart")
	ErrCartVariantInvalid         = errors.New("the selected variant is unavailable or does not belong to this item; select it again")
	ErrRestaurantMinimum          = errors.New("restaurant minimum order amount not met")
	ErrIdempotencyPayloadMismatch = errors.New("this checkout key was already used for a different request")
)

func checkoutRequestHash(in PlaceOrderInput) string {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// FOR UPDATE on the item also blocks concurrent insertion of required groups
// (their foreign-key check takes a key-share lock). Existing options are locked
// explicitly, including price changes. Locks live until the order commits.
func validateCartItemTx(ctx context.Context, tx pgx.Tx, restaurantID, itemID uuid.UUID, variantID *uuid.UUID) error {
	var owner uuid.UUID
	var available bool
	err := tx.QueryRow(ctx, `SELECT restaurant_id,is_active AND is_available FROM food.menu_items WHERE id=$1 FOR UPDATE`, itemID).Scan(&owner, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCartItemUnavailable
	}
	if err != nil {
		return err
	}
	if owner != restaurantID || !available {
		return ErrCartItemUnavailable
	}
	if variantID == nil {
		return nil
	}
	err = tx.QueryRow(ctx, `SELECT menu_item_id,is_available FROM food.menu_item_variants WHERE id=$1 FOR SHARE`, *variantID).Scan(&owner, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCartVariantInvalid
	}
	if err != nil {
		return err
	}
	if owner != itemID || !available {
		return ErrCartVariantInvalid
	}
	return nil
}

func (s *Store) validateCheckoutCartTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	cartID, restaurantID, err := s.ensureCartForUpdate(ctx, tx, userID)
	if err != nil {
		return err
	}
	if restaurantID == nil {
		return ErrCartEmpty
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM food.restaurants WHERE id=$1 FOR SHARE`, *restaurantID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,menu_item_id,variant_id,selected_variant_id,restaurant_id,quantity
		FROM food.cart_items WHERE cart_id=$1 ORDER BY menu_item_id,id FOR UPDATE`, cartID)
	if err != nil {
		return err
	}
	type selection struct {
		id, item, restaurant uuid.UUID
		variant, selected    *uuid.UUID
		quantity             int
	}
	var items []selection
	for rows.Next() {
		var i selection
		if err := rows.Scan(&i.id, &i.item, &i.variant, &i.selected, &i.restaurant, &i.quantity); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return ErrCartEmpty
	}
	for _, i := range items {
		if i.restaurant != *restaurantID || i.quantity <= 0 {
			return ErrCartItemUnavailable
		}
		if i.selected != nil && (i.variant == nil || *i.variant != *i.selected) {
			return ErrCartVariantInvalid
		}
		if err := validateCartItemTx(ctx, tx, *restaurantID, i.item, i.variant); err != nil {
			return fmt.Errorf("item %s: %w", i.item, err)
		}
		addonRows, err := tx.Query(ctx, `SELECT addon_id,quantity FROM food.cart_item_addons WHERE cart_item_id=$1 ORDER BY addon_id FOR UPDATE`, i.id)
		if err != nil {
			return err
		}
		var requested []CartAddonInput
		for addonRows.Next() {
			var a CartAddonInput
			if err := addonRows.Scan(&a.AddonID, &a.Quantity); err != nil {
				addonRows.Close()
				return err
			}
			requested = append(requested, a)
		}
		addonRows.Close()
		if err := addonRows.Err(); err != nil {
			return err
		}
		if err := validateCartAddonsTx(ctx, tx, i.item, requested); err != nil {
			return err
		}
	}
	return nil
}
