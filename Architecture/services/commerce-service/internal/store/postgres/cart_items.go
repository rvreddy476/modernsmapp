// Cart rows: get-or-create the cart, the variant price read, and cart item
// upsert, remove, list and clear. Moved out of store.go unchanged.

package postgres

import (
	"context"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// ─── Cart ────────────────────────────────────────────────────

func (s *Store) GetOrCreateCart(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	cart := &Cart{}
	err := s.db.QueryRow(ctx, `SELECT id,user_id,expires_at,updated_at FROM carts WHERE user_id=$1`, userID).
		Scan(&cart.ID, &cart.UserID, &cart.ExpiresAt, &cart.UpdatedAt)
	if err != nil {
		// Create new cart
		cart.ID = uuid.New()
		cart.UserID = userID
		cart.UpdatedAt = time.Now()
		_, err = s.db.Exec(ctx, `INSERT INTO carts (id,user_id,updated_at) VALUES ($1,$2,$3)`,
			cart.ID, cart.UserID, cart.UpdatedAt)
		return cart, err
	}
	return cart, nil
}

// VariantSellingPriceMinor reads a variant's price in paise.
//
// B5/LB-19. It uses the SAME expression lockAndPriceLines uses inside the
// checkout transaction — `COALESCE(selling_price_minor, ROUND(selling_price*100))`
// — so the snapshot written at add-to-cart is comparable to the price charged
// at checkout. Deriving it any other way is how a price-change check ends up
// comparing two numbers that were never the same kind of thing.
func (s *Store) VariantSellingPriceMinor(ctx context.Context, variantID uuid.UUID) (money.Paise, error) {
	var minor int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price*100))
		   FROM product_variants WHERE id = $1`, variantID).Scan(&minor)
	if err != nil {
		return 0, err
	}
	return money.Paise(minor), nil
}

// UpsertCartItem adds or updates a cart line.
//
// B5/LB-19 — this took a rupees-major float and wrote ONLY `price_snapshot`,
// never `price_snapshot_minor`. It is reachable from the live AddToCart and
// UpdateCartItem routes, so it is not legacy dead code, and the consequence
// was quiet: the P0 checkout reads `price_snapshot_minor` to detect a price
// change between add-to-cart and checkout, and
//
//	if l.SnapshotMinor != 0 && l.SnapshotMinor != priced[i].UnitMinor
//
// skips that check entirely when the column is 0. Every line added through
// this path therefore had NO price-change detection: a seller could raise the
// price after the customer added the item and the customer would be charged
// the new price without the ErrPriceChanged response the flow exists to
// produce.
//
// The parameter is paise now. The float column is still written, from the
// integer, because analytics readers still scan it during the deprecation
// window — but it is derived from the minor value rather than being the
// source of it.
func (s *Store) UpsertCartItem(ctx context.Context, cartID, variantID, productID uuid.UUID, qty int, priceMinor money.Paise) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor,added_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5::numeric/100,$5,NOW())
		ON CONFLICT (cart_id,variant_id) DO UPDATE
		   SET quantity = $4,
		       price_snapshot = $5::numeric/100,
		       price_snapshot_minor = $5`,
		cartID, variantID, productID, qty, priceMinor.Int64(),
	)
	return err
}

func (s *Store) RemoveCartItem(ctx context.Context, cartID, variantID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cart_items WHERE cart_id=$1 AND variant_id=$2`, cartID, variantID)
	return err
}

func (s *Store) GetCartItems(ctx context.Context, cartID uuid.UUID) ([]*CartItem, error) {
	rows, err := s.db.Query(ctx, `SELECT id,cart_id,variant_id,product_id,quantity,price_snapshot,added_at
		FROM cart_items WHERE cart_id=$1 ORDER BY added_at`, cartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*CartItem
	for rows.Next() {
		var ci CartItem
		if err := rows.Scan(&ci.ID, &ci.CartID, &ci.VariantID, &ci.ProductID, &ci.Quantity, &ci.PriceSnapshot, &ci.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, &ci)
	}
	return items, nil
}

func (s *Store) ClearCart(ctx context.Context, cartID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cart_items WHERE cart_id=$1`, cartID)
	return err
}
