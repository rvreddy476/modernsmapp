// Phase F2.1 — tiered B2B pricing service. Owns the cart-time price
// resolver + the seller-facing endpoints for managing variant tier
// ladders. Designed to plug into the existing priceCart pipeline so
// every B2C, B2B, and RFQ-derived order sees consistent line totals.
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrTierOverlap     = fmt.Errorf("price tiers overlap")
	ErrTierInvalidBand = fmt.Errorf("price tier band is invalid")
	ErrTierNotSeller   = fmt.Errorf("not the seller for this variant")
)

// resolveTieredPrice picks the unit price for a cart line. The slice
// must be sorted ascending by min_qty (Store.ListPriceTiersForVariants
// guarantees this). The walk picks the highest min_qty band that the
// quantity satisfies; falls back to `fallback` (variant.SellingPrice)
// when no tier matches. Pure function so it's trivial to unit-test.
func resolveTieredPrice(tiers []*postgres.PriceTier, fallback float64, qty int) float64 {
	if len(tiers) == 0 || qty <= 0 {
		return fallback
	}
	chosen := fallback
	for _, t := range tiers {
		if qty < t.MinQty {
			continue
		}
		if t.MaxQty != nil && qty > *t.MaxQty {
			continue
		}
		// Tier matches the quantity band; since the slice is sorted by
		// min_qty asc, later matches override earlier ones — that
		// means the seller's highest applicable band wins, which is
		// what bulk pricing should do.
		chosen = t.Price
	}
	return chosen
}

// SetPriceTiersInput is one variant's full ladder. Sent as a single
// PUT — service replaces the whole set atomically (no diff API needed).
type SetPriceTiersInput struct {
	VariantID uuid.UUID
	Tiers     []PriceTierInput
}

type PriceTierInput struct {
	MinQty int
	MaxQty *int
	Price  float64
}

// SetPriceTiers replaces a variant's price ladder. Validates:
//   - Caller is the variant's seller.
//   - min_qty >= 1, max_qty >= min_qty (or NULL meaning unbounded).
//   - Price > 0.
//   - No two tiers overlap in [min_qty, max_qty]. Adjacent bands
//     (max_qty=N, next min_qty=N+1) are allowed.
//
// Empty `Tiers` clears the ladder — the variant returns to flat pricing.
func (s *Service) SetPriceTiers(ctx context.Context, sellerID uuid.UUID, in SetPriceTiersInput) ([]*postgres.PriceTier, error) {
	if err := s.assertVariantSeller(ctx, in.VariantID, sellerID); err != nil {
		return nil, err
	}
	if err := validateTierLadder(in.Tiers); err != nil {
		return nil, err
	}
	rows := make([]*postgres.PriceTier, 0, len(in.Tiers))
	for _, t := range in.Tiers {
		rows = append(rows, &postgres.PriceTier{
			VariantID: in.VariantID,
			MinQty:    t.MinQty,
			MaxQty:    t.MaxQty,
			Price:     t.Price,
		})
	}
	if err := s.store.ReplacePriceTiers(ctx, in.VariantID, rows); err != nil {
		return nil, fmt.Errorf("replace tiers: %w", err)
	}
	return s.store.ListPriceTiers(ctx, in.VariantID)
}

// GetPriceTiers is a public read used by both the seller editor and
// the customer PDP — buyers should see the discount ladder so they
// can choose a quantity that hits a break.
func (s *Service) GetPriceTiers(ctx context.Context, variantID uuid.UUID) ([]*postgres.PriceTier, error) {
	return s.store.ListPriceTiers(ctx, variantID)
}

// assertVariantSeller resolves the variant → product → seller and
// rejects the call unless `actorSellerID` matches.
func (s *Service) assertVariantSeller(ctx context.Context, variantID, actorSellerID uuid.UUID) error {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return fmt.Errorf("variant not found: %w", err)
	}
	product, err := s.store.GetProductByID(ctx, variant.ProductID)
	if err != nil {
		return fmt.Errorf("product not found: %w", err)
	}
	if product.SellerID != actorSellerID {
		return ErrTierNotSeller
	}
	return nil
}

// validateTierLadder enforces the structural invariants on the inbound
// tier list. Mutates `tiers` (sorts in place) for deterministic
// downstream behaviour.
func validateTierLadder(tiers []PriceTierInput) error {
	for _, t := range tiers {
		if t.MinQty < 1 {
			return fmt.Errorf("%w: min_qty must be >= 1", ErrTierInvalidBand)
		}
		if t.MaxQty != nil && *t.MaxQty < t.MinQty {
			return fmt.Errorf("%w: max_qty must be >= min_qty", ErrTierInvalidBand)
		}
		if t.Price <= 0 {
			return fmt.Errorf("%w: price must be > 0", ErrTierInvalidBand)
		}
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i].MinQty < tiers[j].MinQty })
	for i := 1; i < len(tiers); i++ {
		prev := tiers[i-1]
		cur := tiers[i]
		// Adjacency check: prev.max_qty must be < cur.min_qty (or prev
		// must be unbounded which would block all later tiers).
		if prev.MaxQty == nil {
			return fmt.Errorf("%w: unbounded tier blocks later bands", ErrTierOverlap)
		}
		if *prev.MaxQty >= cur.MinQty {
			return fmt.Errorf("%w: tier %d-%d overlaps next band starting at %d",
				ErrTierOverlap, prev.MinQty, *prev.MaxQty, cur.MinQty)
		}
	}
	return nil
}

// Compile-time guard for stable error reuse.
var _ = errors.New
