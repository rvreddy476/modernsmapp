package postgres

import (
	"context"

	"github.com/google/uuid"
)

// SellerVariantsForProducts reads every variant of the given products, in
// creation order, as SellerVariantRow — one query for a whole page of the
// seller's catalogue.
//
// Archived variants are included with their status, because the seller's
// own list is the one place a withdrawn variant should still be visible;
// the public product read filters them.
func (s *Store) SellerVariantsForProducts(ctx context.Context, productIDs []uuid.UUID) (map[uuid.UUID][]SellerVariantRow, error) {
	out := make(map[uuid.UUID][]SellerVariantRow, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT v.product_id, v.id, v.sku,
		       v.option_1_name, v.option_1_value, v.option_2_name, v.option_2_value,
		       v.option_3_name, v.option_3_value,
		       COALESCE(NULLIF(v.mrp_minor, 0),           ROUND(v.mrp*100))::bigint,
		       COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price*100))::bigint,
		       GREATEST(COALESCE(i.total_qty - i.reserved_qty, 0), 0),
		       v.status
		  FROM product_variants v
		  LEFT JOIN inventory_items i ON i.variant_id = v.id
		 WHERE v.product_id = ANY($1)
		 ORDER BY v.product_id, v.created_at, v.id`, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			productID uuid.UUID
			r         SellerVariantRow
		)
		if err := rows.Scan(&productID, &r.ID, &r.SKU,
			&r.Option1Name, &r.Option1Value, &r.Option2Name, &r.Option2Value,
			&r.Option3Name, &r.Option3Value,
			&r.MRPMinor, &r.SellingPriceMinor, &r.AvailableQty, &r.Status); err != nil {
			return nil, err
		}
		out[productID] = append(out[productID], r)
	}
	return out, rows.Err()
}
