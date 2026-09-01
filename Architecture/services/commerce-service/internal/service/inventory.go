package service

// Seller stock adjustment.
//
// The seller id is resolved from the caller's own profile, never taken from
// the request — the same rule every other seller write path follows, and the
// reason a request body cannot name someone else's stock.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// AdjustStockInput is one seller-initiated stock change.
type AdjustStockInput struct {
	VariantID uuid.UUID
	Delta     int
	Reason    string
	Notes     string
}

// StockAdjustReasons are the reason codes a seller may use.
//
// This mirrors the `inventory_adjustments_reason_code_check` constraint, minus
// the return-QC codes, which belong to the fenced returns surface and are
// written by that flow rather than typed by a seller. Validating here turns
// what would be a 500 from a CHECK violation into a 400 that names the codes.
var StockAdjustReasons = []string{"purchase", "damage", "theft", "correction", "recount"}

func isStockAdjustReason(r string) bool {
	for _, a := range StockAdjustReasons {
		if r == a {
			return true
		}
	}
	return false
}

// AdjustStock applies a signed delta to one of the caller's own variants.
func (s *Service) AdjustStock(ctx context.Context, actorUserID uuid.UUID, in AdjustStockInput) (*postgres.StockLevel, error) {
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		// A stock movement with no stated cause is unauditable. 'correction'
		// is the honest default for "the number was wrong", and it is what an
		// unstated reason actually means.
		reason = "correction"
	}
	if !isStockAdjustReason(reason) {
		return nil, fmt.Errorf("%w: %q is not one of %s",
			ErrInvalidStockReason, reason, strings.Join(StockAdjustReasons, ", "))
	}

	return s.store.AdjustStock(ctx, postgres.StockAdjustment{
		VariantID: in.VariantID,
		SellerID:  seller.ID,
		ActorID:   actorUserID,
		Delta:     in.Delta,
		Reason:    reason,
		Notes:     strings.TrimSpace(in.Notes),
	})
}

// StockFor reads the current level of one of the caller's own variants.
func (s *Service) StockFor(ctx context.Context, actorUserID, variantID uuid.UUID) (*postgres.StockLevel, error) {
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	return s.store.StockFor(ctx, variantID, seller.ID)
}

// ─── Product image hydration ───────────────────────────────────────────

// hydrateProductImages fills ImageURL/ThumbnailURL on every product that has a
// primary image, in ONE call to media-service regardless of page size.
//
// Commerce returns a bare media UUID and nothing else, so no product screen
// can draw an image today. The Android `core:commerce` module has no
// dependency on `core:media` (which holds the resolver) and giving it one
// would pull the whole ExoPlayer stack into a module that needs a URL string.
// Resolving here fixes it once for every client.
//
// One batch, not one call per product: a twenty-row catalogue page issuing
// twenty sequential HTTP calls would add more latency than the query it
// decorates.
//
// Fails SOFT. Every failure leaves the URLs empty and the client draws a
// placeholder — see the note in internal/media on why this is the opposite of
// the write path's fail-closed rule.
func (s *Service) hydrateProductImages(ctx context.Context, products []*postgres.Product) {
	if s.media == nil || len(products) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		if p != nil && p.PrimaryImageMediaID != nil {
			ids = append(ids, *p.PrimaryImageMediaID)
		}
	}
	if len(ids) == 0 {
		return
	}
	resolved := s.media.ResolveURLs(ctx, ids)
	for _, p := range products {
		if p == nil || p.PrimaryImageMediaID == nil {
			continue
		}
		if r, ok := resolved[*p.PrimaryImageMediaID]; ok {
			p.ImageURL = r.URL()
			p.ThumbnailURL = r.Thumbnail()
			p.ImageBlurhash = r.Blurhash
		}
	}
}

// CartView is the cart as a client reads it: flat lines, paise, and image
// URLs already resolved.
//
// It replaces serialising the internal CartSummary, whose Go field names and
// rupee floats meant every buyer's cart rendered as empty. See
// internal/store/postgres/cartview.go for the full account.
func (s *Service) CartView(ctx context.Context, userID uuid.UUID) (*postgres.CartView, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	view, err := s.store.CartViewFor(ctx, cart.ID)
	if err != nil {
		return nil, err
	}
	s.hydrateCartImages(ctx, view)
	return view, nil
}

// hydrateCartImages resolves the cart's media ids in ONE call, so a ten-line
// cart does not cost ten round trips to media-service.
func (s *Service) hydrateCartImages(ctx context.Context, view *postgres.CartView) {
	if s.media == nil || view == nil || len(view.Items) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(view.Items))
	for _, l := range view.Items {
		if l.ImageMediaID != nil {
			ids = append(ids, *l.ImageMediaID)
		}
	}
	if len(ids) == 0 {
		return
	}
	resolved := s.media.ResolveURLs(ctx, ids)
	for i := range view.Items {
		if view.Items[i].ImageMediaID == nil {
			continue
		}
		if r, ok := resolved[*view.Items[i].ImageMediaID]; ok {
			view.Items[i].ImageURL = r.URL()
			view.Items[i].ThumbnailURL = r.Thumbnail()
		}
	}
}

// TaxClasses returns the GST rate table a seller chooses from.
func (s *Service) TaxClasses(ctx context.Context) ([]postgres.TaxClassOption, error) {
	return s.store.ListTaxClasses(ctx)
}

// SellerVariant reads one of the caller's own variants: its price, its
// availability, and whether it is on sale.
func (s *Service) SellerVariant(ctx context.Context, actorUserID, variantID uuid.UUID) (*postgres.SellerVariant, error) {
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	return s.store.SellerVariantFor(ctx, variantID, seller.ID)
}
