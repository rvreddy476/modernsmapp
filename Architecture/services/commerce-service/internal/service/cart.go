// Cart: adding, removing and updating cart lines and reading the cart
// summary. Moved out of service.go unchanged.

package service

import (
	"context"
	"fmt"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Cart ────────────────────────────────────────────────────

func (s *Service) AddToCart(ctx context.Context, userID, variantID uuid.UUID, qty int) error {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return fmt.Errorf("variant not found: %w", err)
	}
	// LB-17 / v1 §5.6 — moderation is no longer advisory.
	//
	// This used to check ONLY `variant.Status != "active"`. Neither it nor
	// pricing looked at the product's `status` or `approval_status`, so a
	// seller could create a product, share a direct link and sell it while
	// `approval_status = 'pending'` — or keep selling after an admin had
	// rejected it. The entire moderation queue was decorative.
	//
	// This is the cheap early guard, so the customer gets a clear answer at
	// add-to-cart time. The AUTHORITATIVE check runs inside the checkout
	// transaction under the row lock, because a product can be rejected
	// between being added to a cart and being paid for.
	sellerID, eligible, err := s.store.ProductSaleEligibility(ctx, variantID)
	if err != nil {
		return err
	}
	if !eligible || variant.Status != "active" {
		return postgres.ErrProductUnavailable
	}

	// D2 — one seller per cart for P0.
	//
	// The schema supports multi-seller orders, but every cancellation,
	// refund and fulfilment rule in this module assumes one order is one
	// shipment from one seller. A mixed cart would mean partial cancels,
	// partial refunds and split shipments on day one, in precisely the area
	// that was already least correct.
	current, err := s.store.CartSellerID(ctx, userID)
	if err != nil {
		return err
	}
	if current != uuid.Nil && current != sellerID {
		return postgres.ErrMultipleSellers
	}

	inv, err := s.store.GetInventory(ctx, variantID)
	if err != nil {
		return fmt.Errorf("inventory not found: %w", err)
	}
	if inv.AvailableQty() < qty {
		// Typed, so the edge answers 409 OUT_OF_STOCK with the line — the
		// same shape checkout refuses with — rather than a 400 whose only
		// content was this sentence.
		return &postgres.OutOfStockError{Lines: []postgres.OutOfStockLine{{
			VariantID: variantID, ProductID: variant.ProductID,
			Requested: qty, Available: inv.AvailableQty(),
		}}}
	}

	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return fmt.Errorf("get cart: %w", err)
	}

	// B5/LB-19: snapshot the price in PAISE, read the same way checkout
	// reads it. The float `variant.SellingPrice` that used to be passed here
	// never reached `price_snapshot_minor` at all, which silently disabled
	// checkout's price-change detection for every line added through this
	// route.
	priceMinor, err := s.store.VariantSellingPriceMinor(ctx, variantID)
	if err != nil {
		return fmt.Errorf("read variant price: %w", err)
	}
	return s.store.UpsertCartItem(ctx, cart.ID, variantID, variant.ProductID, qty, priceMinor)
}

func (s *Service) RemoveFromCart(ctx context.Context, userID, variantID uuid.UUID) error {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return err
	}
	return s.store.RemoveCartItem(ctx, cart.ID, variantID)
}

// UpdateCartItem sets the absolute quantity for a variant in the user's
// cart. Quantity 0 removes the line. Replaces the mobile delete+add
// roundtrip from commerce_repository.dart (Phase 1.2). UpsertCartItem at
// the SQL layer is INSERT ... ON CONFLICT DO UPDATE, so this is atomic at
// the row level — concurrent calls converge on a single final quantity.
func (s *Service) UpdateCartItem(ctx context.Context, userID, variantID uuid.UUID, qty int) error {
	if qty < 0 {
		return fmt.Errorf("quantity must be >= 0")
	}
	if qty == 0 {
		return s.RemoveFromCart(ctx, userID, variantID)
	}
	// AddToCart's stock check + upsert semantics are exactly what an
	// atomic set-to-N needs; reuse rather than duplicate.
	return s.AddToCart(ctx, userID, variantID, qty)
}

type CartSummary struct {
	CartID    uuid.UUID
	Items     []*CartItemDetail
	Subtotal  float64 // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	ItemCount int
}

type CartItemDetail struct {
	Item    *postgres.CartItem
	Product *postgres.Product
	Variant *postgres.ProductVariant
}

func (s *Service) GetCart(ctx context.Context, userID uuid.UUID) (*CartSummary, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	items, err := s.store.GetCartItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}

	// HP2: was 2N queries (one product + one variant per cart row); now
	// 2 queries total regardless of cart size. Typical 10-item cart drops
	// from ~21 to 3 round trips (cart-items + products + variants).
	productIDs := make([]uuid.UUID, 0, len(items))
	variantIDs := make([]uuid.UUID, 0, len(items))
	for _, ci := range items {
		productIDs = append(productIDs, ci.ProductID)
		variantIDs = append(variantIDs, ci.VariantID)
	}
	products, err := s.store.GetProductsByIDs(ctx, productIDs)
	if err != nil {
		return nil, fmt.Errorf("batch products: %w", err)
	}
	variants, err := s.store.GetVariantsByIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("batch variants: %w", err)
	}

	// The cart carries the whole Product, so hydrating it here gives every
	// cart line a renderable image for free — the same one the catalogue
	// showed. Without it the cart is a column of grey boxes next to the
	// product the buyer just looked at.
	hydrate := make([]*postgres.Product, 0, len(products))
	for _, p := range products {
		hydrate = append(hydrate, p)
	}
	s.hydrateProductImages(ctx, hydrate)

	summary := &CartSummary{CartID: cart.ID}
	for _, ci := range items {
		summary.Items = append(summary.Items, &CartItemDetail{
			Item:    ci,
			Product: products[ci.ProductID],
			Variant: variants[ci.VariantID],
		})
		summary.Subtotal += ci.PriceSnapshot * float64(ci.Quantity)
		summary.ItemCount += ci.Quantity
	}
	return summary, nil
}
