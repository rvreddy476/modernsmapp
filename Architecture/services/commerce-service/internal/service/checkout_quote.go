// Checkout pricing: the tax breakdown, coupon caps, the quote types, the
// shared cart pricer (priceCart), the serviceability check and the Quote
// endpoint. Moved out of service.go unchanged.

package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Tax Calculation ─────────────────────────────────────────

type TaxBreakdown struct {
	TaxableAmount float64
	CGSTPct       float64
	SGSTPct       float64
	IGSTPct       float64
	CGSTAmount    float64
	SGSTAmount    float64
	IGSTAmount    float64
	TotalTax      float64
	IsInterstate  bool
}

// CalcTax returns GST breakdown for a given amount.
// If sellerState == customerState → intrastate (CGST+SGST); else interstate (IGST).
func CalcTax(amount, cgstPct, sgstPct, igstPct float64, sellerState, customerState string) TaxBreakdown {
	interstate := sellerState != customerState
	tb := TaxBreakdown{TaxableAmount: amount, CGSTPct: cgstPct, SGSTPct: sgstPct, IGSTPct: igstPct, IsInterstate: interstate}
	if interstate {
		tb.IGSTAmount = round2(amount * igstPct / 100)
		tb.TotalTax = tb.IGSTAmount
	} else {
		tb.CGSTAmount = round2(amount * cgstPct / 100)
		tb.SGSTAmount = round2(amount * sgstPct / 100)
		tb.TotalTax = tb.CGSTAmount + tb.SGSTAmount
	}
	return tb
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// couponWithinCaps returns true iff the coupon still has global +
// per-user redemptions available. Audit O10: GetCouponByCode already
// filters by is_active and expiry at the SQL layer, but the prior
// checkout never enforced max_uses or max_uses_per_user — a permissive
// public code could be redeemed unboundedly by anyone. max_uses == 0
// is treated as "unlimited"; same for max_uses_per_user.
func couponWithinCaps(ctx context.Context, s *Service, c *postgres.Coupon, userID uuid.UUID) bool {
	if c.MaxUses != nil && *c.MaxUses > 0 && c.UsesCount >= *c.MaxUses {
		return false
	}
	if c.MaxUsesPerUser > 0 {
		n, err := s.store.CountCouponUsagesByUser(ctx, c.ID, userID)
		if err != nil {
			// Fail closed on Postgres errors — reject the coupon rather
			// than risk over-redemption under a transient blip.
			return false
		}
		if n >= c.MaxUsesPerUser {
			return false
		}
	}
	return true
}

// ─── Checkout & Orders ───────────────────────────────────────

// QuoteInput is the request body for the server-authoritative checkout
// quote endpoint (POST /v1/commerce/checkout/quote — Phase 1.1).
type QuoteInput struct {
	UserID        uuid.UUID
	AddressID     uuid.UUID
	PaymentMethod string
	CouponCode    string
}

// QuoteItem is one line item in a checkout quote response.
type QuoteItem struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	SellerID     uuid.UUID `json:"seller_id"`
	ProductTitle string    `json:"product_title"`
	SKU          string    `json:"sku,omitempty"`
	Quantity     int       `json:"quantity"`
	UnitPrice    float64   `json:"unit_price"`
	LineSubtotal float64   `json:"line_subtotal"` // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
}

// UnavailableQuoteItem is one cart row whose requested quantity exceeds
// available stock — surfaced on the Quote response so the UI can warn
// before "Place order". Strict checkout still fails on these.
type UnavailableQuoteItem struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	ProductTitle string    `json:"product_title"`
	Available    int       `json:"available"`
	Requested    int       `json:"requested"`
}

// Quote is the server-authoritative pricing the client must render before
// placing an order. The same priceCart helper backs both Quote and
// Checkout, so what the customer sees in the quote is what they get on
// the order — there is no client-side recomputation.
type Quote struct {
	Subtotal         float64                `json:"subtotal"` // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	CouponDiscount   float64                `json:"coupon_discount"`
	CouponCode       string                 `json:"coupon_code,omitempty"`
	Shipping         float64                `json:"shipping"`
	Tax              float64                `json:"tax"`
	GrandTotal       float64                `json:"grand_total"`
	Currency         string                 `json:"currency"`
	Items            []QuoteItem            `json:"items"`
	UnavailableItems []UnavailableQuoteItem `json:"unavailable_items"`
	CODEligible      bool                   `json:"cod_eligible"`
	Serviceable      bool                   `json:"serviceable"`
	SellerIDs        []uuid.UUID            `json:"seller_ids"`
}

// pricingResult is the shared, untransformed output of the cart-pricing
// pipeline. Quote shapes it into a customer-facing DTO; Checkout consumes
// the OrderItems directly to persist the order.
type pricingResult struct {
	Cart             *postgres.Cart
	CartItems        []*postgres.CartItem
	OrderItems       []*postgres.OrderItem
	Subtotal         float64 // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	CouponDiscount   float64
	CouponCodePtr    *string
	Shipping         float64
	Tax              float64
	FinalAmount      float64
	UnavailableItems []UnavailableQuoteItem
}

// priceCart resolves the user's current cart into a fully-priced result
// using server-side product/variant/inventory data. strict=true (Checkout)
// rejects the call on any out-of-stock line; strict=false (Quote) returns
// the result with UnavailableItems populated and prices reflecting only
// the available rows. Coupon, shipping, and tax rules match Checkout 1:1.
//
// Caller must validate the user owns the cart; this helper trusts userID.
func (s *Service) priceCart(ctx context.Context, userID uuid.UUID, paymentMethod, couponCode string, strict bool) (*pricingResult, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	cartItems, err := s.store.GetCartItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}
	if len(cartItems) == 0 {
		return nil, fmt.Errorf("cart is empty")
	}

	// Phase F2.1 — preload tiered prices for every variant in the cart
	// in one round trip. The fast-path `resolveLinePrice` below uses the
	// resulting map; falls back to variant.selling_price when a variant
	// has no tier ladder.
	variantIDs := make([]uuid.UUID, 0, len(cartItems))
	for _, ci := range cartItems {
		variantIDs = append(variantIDs, ci.VariantID)
	}
	tiersByVariant, err := s.store.ListPriceTiersForVariants(ctx, variantIDs)
	if err != nil {
		// Tier lookup failure is non-fatal — we fall back to the
		// variant's catalog price so checkout still completes.
		slog.Warn("priceCart: tier lookup failed; using catalog price", "error", err)
		tiersByVariant = map[uuid.UUID][]*postgres.PriceTier{}
	}

	// HP2: batch variant/product/inventory fetches for the whole cart.
	// Was 3N round trips (per cart row); now 3 fixed regardless of cart
	// size. A 10-item cart drops from ~31 to 4 round trips at checkout.
	productIDs := make([]uuid.UUID, 0, len(cartItems))
	for _, ci := range cartItems {
		productIDs = append(productIDs, ci.ProductID)
	}
	variantsByID, err := s.store.GetVariantsByIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("batch variants: %w", err)
	}
	productsByID, err := s.store.GetProductsByIDs(ctx, productIDs)
	if err != nil {
		return nil, fmt.Errorf("batch products: %w", err)
	}
	inventoryByVariant, err := s.store.GetInventoryByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("batch inventory: %w", err)
	}

	var orderItems []*postgres.OrderItem
	var unavailable []UnavailableQuoteItem
	var subtotal float64 // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	totalTax := 0.0      // Phase 3+ will compute per-HSN GST; today the schema stores 0.

	for _, ci := range cartItems {
		variant, ok := variantsByID[ci.VariantID]
		if !ok {
			return nil, fmt.Errorf("variant %s not found", ci.VariantID)
		}
		product, ok := productsByID[ci.ProductID]
		if !ok {
			return nil, fmt.Errorf("product %s not found", ci.ProductID)
		}
		inv, ok := inventoryByVariant[ci.VariantID]
		if !ok {
			return nil, fmt.Errorf("inventory for %s not found", ci.VariantID)
		}
		if inv.AvailableQty() < ci.Quantity {
			if strict {
				return nil, fmt.Errorf("insufficient stock for %s: available %d", product.Title, inv.AvailableQty())
			}
			unavailable = append(unavailable, UnavailableQuoteItem{
				VariantID:    ci.VariantID,
				ProductID:    ci.ProductID,
				ProductTitle: product.Title,
				Available:    inv.AvailableQty(),
				Requested:    ci.Quantity,
			})
			continue
		}

		// Phase F2.1 — resolve unit price via tier ladder if one
		// exists. The walk is short (typical seller defines ≤5 tiers
		// per variant) and the slice is already sorted by min_qty.
		unitPrice := resolveTieredPrice(tiersByVariant[ci.VariantID], variant.SellingPrice, ci.Quantity)
		lineTotal := round2(unitPrice * float64(ci.Quantity))
		subtotal += lineTotal

		returnUntil := time.Now().AddDate(0, 0, product.ReturnPolicyDays)
		orderItems = append(orderItems, &postgres.OrderItem{
			ProductID:           ci.ProductID,
			VariantID:           ci.VariantID,
			SellerID:            product.SellerID,
			ProductTitle:        product.Title,
			SKU:                 variant.SKU,
			Quantity:            ci.Quantity,
			UnitMRP:             variant.MRP,
			UnitPrice:           unitPrice,
			DiscountAmount:      0,
			TaxAmount:           0,
			FinalPrice:          lineTotal,
			Status:              "confirmed",
			ReturnEligibleUntil: &returnUntil,
		})
	}

	// Coupon — Audit O10: enforces max_uses + max_uses_per_user caps.
	couponDiscount := 0.0
	var couponCodePtr *string
	if couponCode != "" {
		c, err := s.store.GetCouponByCode(ctx, couponCode)
		if err == nil && subtotal >= c.MinOrderAmount && couponWithinCaps(ctx, s, c, userID) {
			switch c.DiscountType {
			case "percentage":
				d := subtotal * c.DiscountValue / 100
				if c.MaxDiscountAmount != nil && d > *c.MaxDiscountAmount {
					d = *c.MaxDiscountAmount
				}
				couponDiscount = round2(d)
			case "flat":
				couponDiscount = c.DiscountValue
			}
		}
		cc := couponCode
		couponCodePtr = &cc
	}

	// Shipping: flat ₹40, free above ₹499. Phase 1.3 serviceability will
	// per-courier/per-pincode the real number.
	shipping := 40.0
	if subtotal > 499 {
		shipping = 0
	}

	final := subtotal - couponDiscount + shipping + totalTax
	return &pricingResult{
		Cart:             cart,
		CartItems:        cartItems,
		OrderItems:       orderItems,
		Subtotal:         subtotal,
		CouponDiscount:   couponDiscount,
		CouponCodePtr:    couponCodePtr,
		Shipping:         shipping,
		Tax:              totalTax,
		FinalAmount:      final,
		UnavailableItems: unavailable,
	}, nil
}

// ServiceabilityInput is the request for CheckServiceability — Phase 1.3.
// Pincode is the customer's drop pincode; ProductID resolves the seller
// (and pickup pincode) and the package weight unless overridden.
type ServiceabilityInput struct {
	Pincode       string
	ProductID     uuid.UUID
	VariantID     uuid.UUID
	SellerID      uuid.UUID
	PaymentMethod string
}

// CheckServiceability is the server-authoritative serviceability +
// COD-eligibility check that replaces the mobile pincode heuristic.
// Inputs come from the product's seller pickup address and weight; the
// courier adapter returns the authoritative answer (currently stub-ish
// for both adapters — the Shiprocket implementation is a follow-up).
func (s *Service) CheckServiceability(ctx context.Context, in ServiceabilityInput) (*courier.ServiceabilityResult, error) {
	if in.Pincode == "" {
		return &courier.ServiceabilityResult{Serviceable: false, Reason: "pincode required"}, nil
	}
	product, err := s.store.GetProductByID(ctx, in.ProductID)
	if err != nil {
		return nil, fmt.Errorf("product %s not found", in.ProductID)
	}
	sellerID := product.SellerID
	if in.SellerID != uuid.Nil {
		sellerID = in.SellerID
	}
	seller, err := s.store.GetSellerByID(ctx, sellerID)
	if err != nil {
		return nil, fmt.Errorf("seller %s not found", sellerID)
	}
	if seller.PostalCode == nil || *seller.PostalCode == "" {
		return &courier.ServiceabilityResult{
			Serviceable: false,
			Reason:      "seller pickup pincode not configured",
		}, nil
	}
	weightKg := 0.5 // sensible default until catalog enforces a weight
	if product.WeightGrams != nil && *product.WeightGrams > 0 {
		weightKg = float64(*product.WeightGrams) / 1000.0
	}
	pm := in.PaymentMethod
	if pm == "" {
		pm = "prepaid"
	}
	if s.courier == nil {
		// No courier wired (test/dev without provider): assume reachable
		// so the rest of the flow keeps working; production must have
		// COURIER_PROVIDER set.
		return &courier.ServiceabilityResult{
			Serviceable:   true,
			CODSupported:  true,
			EstimatedDays: 4,
			Courier:       "none",
			Reason:        "courier provider not configured",
		}, nil
	}
	return s.courier.CheckServiceability(ctx, courier.ServiceabilityRequest{
		PickupPincode: *seller.PostalCode,
		DropPincode:   in.Pincode,
		WeightKg:      weightKg,
		PaymentMethod: pm,
	})
}

// Quote returns the server-authoritative pricing for the user's current
// cart without persisting an order. The web + mobile checkout flows must
// render this before "Place order" so the customer sees the same numbers
// the server will use on Checkout.
//
// Serviceability and COD eligibility are placeholder true; Phase 1.3
// replaces them with the real courier + pincode check.
func (s *Service) Quote(ctx context.Context, in QuoteInput) (*Quote, error) {
	res, err := s.priceCart(ctx, in.UserID, in.PaymentMethod, in.CouponCode, false)
	if err != nil {
		return nil, err
	}
	items := make([]QuoteItem, 0, len(res.OrderItems))
	sellerSet := map[uuid.UUID]struct{}{}
	for _, oi := range res.OrderItems {
		items = append(items, QuoteItem{
			VariantID:    oi.VariantID,
			ProductID:    oi.ProductID,
			SellerID:     oi.SellerID,
			ProductTitle: oi.ProductTitle,
			SKU:          oi.SKU,
			Quantity:     oi.Quantity,
			UnitPrice:    oi.UnitPrice,
			LineSubtotal: oi.FinalPrice,
		})
		sellerSet[oi.SellerID] = struct{}{}
	}
	sellerIDs := make([]uuid.UUID, 0, len(sellerSet))
	for sid := range sellerSet {
		sellerIDs = append(sellerIDs, sid)
	}
	codeStr := ""
	if res.CouponCodePtr != nil {
		codeStr = *res.CouponCodePtr
	}
	return &Quote{
		Subtotal:         round2(res.Subtotal),
		CouponDiscount:   round2(res.CouponDiscount),
		CouponCode:       codeStr,
		Shipping:         round2(res.Shipping),
		Tax:              round2(res.Tax),
		GrandTotal:       round2(res.FinalAmount),
		Currency:         "INR",
		Items:            items,
		UnavailableItems: res.UnavailableItems,
		CODEligible:      true, // Phase 1.3 replaces with real check
		Serviceable:      true, // Phase 1.3 replaces with real check
		SellerIDs:        sellerIDs,
	}, nil
}
