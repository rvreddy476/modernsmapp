package postgres

// One product, one question: may a shopper see it?
//
// The list surfaces (home, browse, seller storefront, favourites, category
// counts, search docs) apply productSummaryLive inside their SELECTs. The
// single-product surfaces — detail, gallery, specifications, variants,
// reviews, price tiers, the tag-composer preview — read a product by id and
// never asked at all: GET /products/<id> returned a draft, a rejected listing,
// or the listing of a seller who had been suspended or never approved, to
// anyone who had the id. This is the one read they all ask now, and it is the
// same rule, so a product cannot be absent from every list and still open by
// link.

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ProductBuyerVisibility reports whether a product is live by
// productSummaryLive, and the user id of the seller offering it (so the
// owner can still open their own unpublished listing). ErrProductNotFound
// when there is no such product or it has no offer.
func (s *Store) ProductBuyerVisibility(ctx context.Context, productID uuid.UUID) (visible bool, sellerUserID uuid.UUID, err error) {
	var owner *uuid.UUID
	err = s.db.QueryRow(ctx, `
		SELECT (`+productSummaryLive+`) AS live,
		       (SELECT sl.user_id FROM sellers sl WHERE sl.id = po.seller_id)
		  `+productsLiveFrom+`
		 WHERE p.id = $1`, productID).Scan(&visible, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, uuid.Nil, ErrProductNotFound
	}
	if err != nil {
		return false, uuid.Nil, err
	}
	if owner != nil {
		sellerUserID = *owner
	}
	return visible, sellerUserID, nil
}

// VariantProductID resolves the product a variant belongs to.
func (s *Store) VariantProductID(ctx context.Context, variantID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT product_id FROM product_variants WHERE id = $1`, variantID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrVariantNotFound
	}
	return id, err
}

// SellerProductIDs lists every product a seller offers, in any state.
//
// For the seller-level transitions (approve, reject, request changes,
// suspend, unsuspend): each one changes whether EVERY listing of that seller
// is visible, and the search index learns that only from one visibility event
// per product. See Service.publishSellerCatalogueVisibility.
func (s *Store) SellerProductIDs(ctx context.Context, sellerID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT po.product_id FROM product_offers po
		 WHERE po.seller_id = $1
		 ORDER BY po.product_id`, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
