// The try-on descriptor: which AR effect renders a product on a face, and
// which of its colourways map to which parameters inside that effect.
//
// Read on product detail, written by the product's own seller. Absence is the
// normal case and is not an error — see migration 034 for why capability is
// opt-in per product rather than derived from a category.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TryOnVariant is one look in the strip a viewer scrolls through.
//
// ID is the product variant id when the look is separately purchasable, so
// the try-on strip and the buy strip agree on what is being tried; it may
// also be a free label for a look with no SKU of its own. JS carries an
// effect-specific call for the cases a colour alone cannot drive.
type TryOnVariant struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hex   string `json:"hex,omitempty"`
	JS    string `json:"js,omitempty"`
}

// ProductTryOn is the whole descriptor. Capable is always true on a row that
// exists; it is carried explicitly because it is what the wire says to a
// client, and a client that understands the field but not the kind should
// still be able to read "no try-on here" from one boolean.
type ProductTryOn struct {
	ProductID  uuid.UUID      `json:"-"`
	Capable    bool           `json:"capable"`
	Kind       string         `json:"kind"`
	EffectSlug string         `json:"effect_slug"`
	Variants   []TryOnVariant `json:"variants"`
}

// GetProductTryOn returns the descriptor, or (nil, nil) when the product has
// none. A malformed `variants` blob is reported rather than swallowed: it
// means someone wrote past the write path, and silently serving a try-on with
// no looks would send a viewer to a camera with nothing to apply.
func (s *Store) GetProductTryOn(ctx context.Context, productID uuid.UUID) (*ProductTryOn, error) {
	var (
		out ProductTryOn
		raw []byte
	)
	err := s.db.QueryRow(ctx, `
		SELECT product_id, kind, effect_slug, variants
		  FROM product_try_on
		 WHERE product_id = $1`, productID).
		Scan(&out.ProductID, &out.Kind, &out.EffectSlug, &raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("try-on: load %s: %w", productID, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Variants); err != nil {
			return nil, fmt.Errorf("try-on: decode variants for %s: %w", productID, err)
		}
	}
	if out.Variants == nil {
		out.Variants = []TryOnVariant{}
	}
	out.Capable = true
	return &out, nil
}

// UpsertProductTryOn publishes or replaces the descriptor. The whole row is
// replaced rather than patched: a descriptor is authored as one unit beside
// an effect bundle, and a partial update would let a variant list outlive the
// effect it was written for.
func (s *Store) UpsertProductTryOn(ctx context.Context, d *ProductTryOn, updatedBy uuid.UUID) error {
	variants := d.Variants
	if variants == nil {
		variants = []TryOnVariant{}
	}
	raw, err := json.Marshal(variants)
	if err != nil {
		return fmt.Errorf("try-on: encode variants: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO product_try_on (product_id, kind, effect_slug, variants, updated_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (product_id) DO UPDATE
		   SET kind        = EXCLUDED.kind,
		       effect_slug = EXCLUDED.effect_slug,
		       variants    = EXCLUDED.variants,
		       updated_by  = EXCLUDED.updated_by,
		       updated_at  = NOW()`,
		d.ProductID, d.Kind, d.EffectSlug, raw, updatedBy)
	if err != nil {
		return fmt.Errorf("try-on: upsert %s: %w", d.ProductID, err)
	}
	return nil
}

// DeleteProductTryOn withdraws the capability. Idempotent: removing a
// descriptor that is not there is the state the caller asked for.
func (s *Store) DeleteProductTryOn(ctx context.Context, productID uuid.UUID) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM product_try_on WHERE product_id = $1`, productID); err != nil {
		return fmt.Errorf("try-on: delete %s: %w", productID, err)
	}
	return nil
}

// CategoryRootSlug walks parent_id to the top of the tree and returns the
// root's slug, or "" when the category does not exist.
//
// A recursive CTE rather than a loop of round trips: the try-on fence asks
// this on every publish, and the depth is not bounded by anything in the
// schema. The walk is depth-capped so a parent cycle written past the
// application's own cycle check cannot spin here.
func (s *Store) CategoryRootSlug(ctx context.Context, categoryID uuid.UUID) (string, error) {
	var slug string
	err := s.db.QueryRow(ctx, `
		WITH RECURSIVE up AS (
		    SELECT id, parent_id, slug, 1 AS depth
		      FROM product_categories
		     WHERE id = $1
		    UNION ALL
		    SELECT c.id, c.parent_id, c.slug, up.depth + 1
		      FROM product_categories c
		      JOIN up ON up.parent_id = c.id
		     WHERE up.depth < 16
		)
		SELECT slug FROM up ORDER BY depth DESC LIMIT 1`, categoryID).Scan(&slug)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("try-on: category root for %s: %w", categoryID, err)
	}
	return slug, nil
}
