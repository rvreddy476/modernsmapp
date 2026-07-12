package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WishlistProduct is the tile snapshot joined from products + the saved
// variant, matching what the mobile wishlist screen renders.
type WishlistProduct struct {
	ID                  uuid.UUID  `json:"id"`
	Title               string     `json:"title"`
	SellingPrice        float64    `json:"selling_price"`
	MRP                 *float64   `json:"mrp,omitempty"`
	PrimaryImageMediaID *uuid.UUID `json:"primary_image_media_id,omitempty"`
}

// WishlistEntry is one saved product on the customer's wishlist.
type WishlistEntry struct {
	ProductID uuid.UUID       `json:"product_id"`
	SavedAt   time.Time       `json:"saved_at"`
	Product   WishlistProduct `json:"product"`
}

// ensureWishlist returns the user's default wishlist id, creating the
// row on first use (the schema allows several named lists per user; the
// API surface only exposes the default one today).
func (s *Store) ensureWishlist(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM wishlists WHERE user_id=$1 ORDER BY created_at LIMIT 1`,
		userID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	err = s.db.QueryRow(ctx,
		`INSERT INTO wishlists (user_id) VALUES ($1) RETURNING id`,
		userID).Scan(&id)
	return id, err
}

// GetWishlistByUser lists the user's saved products, newest first.
func (s *Store) GetWishlistByUser(ctx context.Context, userID uuid.UUID) ([]WishlistEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT wi.product_id, wi.added_at, p.title,
		       COALESCE(v.selling_price, 0), v.mrp, p.primary_image_media_id
		FROM wishlist_items wi
		JOIN wishlists w ON w.id = wi.wishlist_id
		JOIN products p ON p.id = wi.product_id
		LEFT JOIN product_variants v ON v.id = wi.variant_id
		WHERE w.user_id = $1
		ORDER BY wi.added_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []WishlistEntry{}
	for rows.Next() {
		var e WishlistEntry
		if err := rows.Scan(&e.ProductID, &e.SavedAt, &e.Product.Title,
			&e.Product.SellingPrice, &e.Product.MRP,
			&e.Product.PrimaryImageMediaID); err != nil {
			return nil, err
		}
		e.Product.ID = e.ProductID
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ErrWishlistProductUnavailable is returned when the product has no
// variant to key the wishlist row on (draft/broken listings).
var ErrWishlistProductUnavailable = errors.New("product cannot be wishlisted")

// AddToWishlist saves a product (idempotent). The schema keys items by
// variant, so the product's first variant anchors the row; the API
// surface stays product-level like the mobile client expects.
func (s *Store) AddToWishlist(ctx context.Context, userID, productID uuid.UUID) error {
	wishlistID, err := s.ensureWishlist(ctx, userID)
	if err != nil {
		return err
	}
	var variantID uuid.UUID
	err = s.db.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE product_id=$1 ORDER BY created_at LIMIT 1`,
		productID).Scan(&variantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWishlistProductUnavailable
	}
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO wishlist_items (wishlist_id, variant_id, product_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (wishlist_id, variant_id) DO NOTHING`,
		wishlistID, variantID, productID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		// Best-effort popularity counter; the wishlist row is the truth.
		_, _ = s.db.Exec(ctx,
			`UPDATE products SET wishlist_count = wishlist_count + 1 WHERE id=$1`,
			productID)
	}
	return nil
}

// RemoveFromWishlist drops every saved variant of the product from the
// user's wishlist (idempotent — removing an absent product is a no-op).
func (s *Store) RemoveFromWishlist(ctx context.Context, userID, productID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM wishlist_items wi
		USING wishlists w
		WHERE wi.wishlist_id = w.id AND w.user_id = $1 AND wi.product_id = $2`,
		userID, productID)
	if err != nil {
		return err
	}
	if n := tag.RowsAffected(); n > 0 {
		_, _ = s.db.Exec(ctx, `
			UPDATE products
			SET wishlist_count = GREATEST(wishlist_count - $2, 0)
			WHERE id = $1`, productID, n)
	}
	return nil
}
