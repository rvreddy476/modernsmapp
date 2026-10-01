package service

// Approved sellers only (founder, 1 Oct 2026): "Only approved sellers show in
// the retail ... non-approved or status pending should not come into the
// customer portal."
//
// The list surfaces carry that rule inside their SQL (productSummaryLive).
// This file is the rest of it:
//
//   - RequireProductReadable — the single-product reads (detail, gallery,
//     specifications, variants, reviews, price tiers, preview) answer 404 for
//     a product a shopper may not see, except to the seller who owns it, whose
//     own listing editor reads the same routes.
//
//   - publishSellerCatalogueVisibility — a seller-level decision changes the
//     visibility of EVERY listing that seller has, and search-service hears
//     about visibility only through one commerce.product.published /
//     unpublished event per product. Without these, suspending a seller took
//     their listings off the storefront and left them in search until a
//     reindex — and the reindex adds, it does not delete.

import (
	"context"
	"errors"
	"log/slog"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// RequireProductReadable answers nil when viewer may read this product:
// it is live by the storefront's rule, or viewer is the user who sells it.
// Everything else is postgres.ErrProductNotFound — the same answer as a
// product that does not exist, so a hidden listing is not confirmed to exist.
//
// viewer is uuid.Nil for an anonymous shopper.
func (s *Service) RequireProductReadable(ctx context.Context, productID, viewer uuid.UUID) error {
	visible, owner, err := s.store.ProductBuyerVisibility(ctx, productID)
	if err != nil {
		return err
	}
	if visible {
		return nil
	}
	if viewer != uuid.Nil && viewer == owner {
		return nil
	}
	return postgres.ErrProductNotFound
}

// RequireVariantReadable is RequireProductReadable for a variant-scoped read.
func (s *Service) RequireVariantReadable(ctx context.Context, variantID, viewer uuid.UUID) error {
	productID, err := s.store.VariantProductID(ctx, variantID)
	if err != nil {
		return err
	}
	return s.RequireProductReadable(ctx, productID, viewer)
}

// publishSellerCatalogueVisibility re-announces every listing of a seller
// after a seller-level transition. publishProductVisibility reads each
// product back and lets ProductLifecycle.Visible() choose published or
// unpublished, so this never decides visibility itself.
//
// Best-effort like every publish here: the decision has committed, and a lost
// event is recovered by the reindex (for additions) — which is exactly why
// the removals must be sent now.
func (s *Service) publishSellerCatalogueVisibility(ctx context.Context, sellerID uuid.UUID) {
	ids, err := s.store.SellerProductIDs(ctx, sellerID)
	if err != nil {
		slog.WarnContext(ctx, "commerce: could not list a seller's products for the search events",
			"seller_id", sellerID, "error", err)
		return
	}
	for _, id := range ids {
		s.publishProductVisibility(ctx, id)
	}
}

// isProductNotFound reports either spelling of "no such product".
func isProductNotFound(err error) bool {
	return errors.Is(err, postgres.ErrProductNotFound) || errors.Is(err, ErrProductNotFound)
}
