// Phase F2.1 — tiered B2B pricing data access. Tiers are per-variant
// quantity bands the seller defines; priceCart resolves the right band
// at checkout. The (variant_id, min_qty) UNIQUE constraint blocks
// duplicate min thresholds; service-layer validation rejects overlaps
// on the max_qty side.
package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type PriceTier struct {
	ID        uuid.UUID `db:"id" json:"id"`
	VariantID uuid.UUID `db:"variant_id" json:"variant_id"`
	MinQty    int       `db:"min_qty" json:"min_qty"`
	MaxQty    *int      `db:"max_qty" json:"max_qty,omitempty"`
	Price     float64   `db:"price" json:"price"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// ListPriceTiers returns the tiers for a variant sorted by min_qty asc
// (which is the order priceCart needs to walk them).
func (s *Store) ListPriceTiers(ctx context.Context, variantID uuid.UUID) ([]*PriceTier, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, variant_id, min_qty, max_qty, price, created_at, updated_at
		FROM product_price_tiers
		WHERE variant_id = $1
		ORDER BY min_qty ASC`, variantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PriceTier
	for rows.Next() {
		var t PriceTier
		if err := rows.Scan(&t.ID, &t.VariantID, &t.MinQty, &t.MaxQty, &t.Price,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// ReplacePriceTiers performs an atomic delete-then-insert in one tx so
// the API contract "set the tiers" can never leave a half-written state.
// Empty `tiers` clears the variant's tier table — variant.selling_price
// becomes the effective price again.
func (s *Store) ReplacePriceTiers(ctx context.Context, variantID uuid.UUID, tiers []*PriceTier) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`DELETE FROM product_price_tiers WHERE variant_id = $1`, variantID); err != nil {
		return err
	}
	for _, t := range tiers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_price_tiers (variant_id, min_qty, max_qty, price)
			VALUES ($1, $2, $3, $4)`,
			variantID, t.MinQty, t.MaxQty, t.Price); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ListPriceTiersForVariants is the priceCart fast-path: one query for a
// whole cart's worth of variant ids returns a map[variantID] -> []tier.
// Cuts an N+1 to a single round trip during checkout.
func (s *Store) ListPriceTiersForVariants(ctx context.Context, variantIDs []uuid.UUID) (map[uuid.UUID][]*PriceTier, error) {
	if len(variantIDs) == 0 {
		return map[uuid.UUID][]*PriceTier{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, variant_id, min_qty, max_qty, price, created_at, updated_at
		FROM product_price_tiers
		WHERE variant_id = ANY($1)
		ORDER BY variant_id, min_qty ASC`, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]*PriceTier, len(variantIDs))
	for rows.Next() {
		var t PriceTier
		if err := rows.Scan(&t.ID, &t.VariantID, &t.MinQty, &t.MaxQty, &t.Price,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out[t.VariantID] = append(out[t.VariantID], &t)
	}
	return out, rows.Err()
}
