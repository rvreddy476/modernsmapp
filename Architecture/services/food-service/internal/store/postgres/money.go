package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/settlement"
	"github.com/google/uuid"
)

// Wave 1 B3: order money in integer paise, priced by internal/pricing through
// shared/gst. The NUMERIC columns are still written, derived exactly from the
// paise in SQL (($n::bigint)::numeric / 100), and the CHECK
// ck_food_order_paise_match holds the two together.

// WithPricingConfig sets the fees, the platform GSTIN and the coupons flag.
func (s *Store) WithPricingConfig(cfg pricing.Config) *Store {
	s.pricingCfg = cfg
	return s
}

// WithSettlementRules sets the GST-on-commission and TCS rates.
func (s *Store) WithSettlementRules(r settlement.Rules) *Store {
	s.settlementRules = r
	return s
}

// PricingError tells a client why a cart cannot be checked out.
type PricingError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func pricingErrorFrom(err error) *PricingError {
	code := pricing.Code(err)
	if code == "" {
		code = "FOOD_PRICING_FAILED"
	}
	return &PricingError{Code: code, Message: err.Error()}
}

// OrderMoney is the order detail money block.
type OrderMoney struct {
	TotalsPaise pricing.Totals `json:"totals_paise"`
	// TaxesAndCharges is nil for an order placed before B3.
	TaxesAndCharges          *pricing.TaxesAndCharges `json:"taxes_and_charges"`
	NeedsAdviserConfirmation bool                     `json:"needs_adviser_confirmation"`
}

// OrderMoneyFrom builds the money block from stored totals and breakdown.
func OrderMoneyFrom(t pricing.Totals, b *pricing.Breakdown, needsAdviserConfirmation bool) *OrderMoney {
	m := &OrderMoney{TotalsPaise: t, NeedsAdviserConfirmation: needsAdviserConfirmation}
	if b != nil {
		view := pricing.TaxesAndChargesFrom(*b, t)
		m.TaxesAndCharges = &view
	}
	return m
}

func rupees(p int64) float64 { return pricing.PaiseToRupees(p) }

func cartItemRef(id uuid.UUID) string { return "item:" + id.String() }

func cartAddonRef(itemID, addonID uuid.UUID) string {
	return "addon:" + itemID.String() + ":" + addonID.String()
}

// orderItemRef and orderAddonRef name order_items / order_item_addons rows in
// orders.tax_breakdown, so the invoice can describe every line.
func orderItemRef(id uuid.UUID) string  { return "item:" + id.String() }
func orderAddonRef(id uuid.UUID) string { return "addon:" + id.String() }

func cartItemsPaise(cart *Cart) int64 {
	var total int64
	for _, it := range cart.Items {
		total += it.UnitPricePaise * int64(it.Quantity)
	}
	return total
}

// PriceCart fills a loaded cart's money from its items' paise prices: line
// totals, per-item GST, the legacy float totals, totals_paise and
// taxes_and_charges. When the cart cannot be priced it sets PricingError and
// leaves the tax-dependent figures unset.
func PriceCart(cfg pricing.Config, r pricing.Restaurant, cart *Cart, packagingPaise, discountPaise int64, at time.Time) {
	cart.TotalsPaise, cart.TaxesAndCharges, cart.PricingError = nil, nil, nil
	priced := pricing.Cart{PackagingPaise: packagingPaise, DiscountPaise: discountPaise}
	var itemsPaise, addonsPaise int64
	for i := range cart.Items {
		it := &cart.Items[i]
		it.LineTotalPaise = it.UnitPricePaise * int64(it.Quantity)
		it.UnitPrice, it.LineTotal = rupees(it.UnitPricePaise), rupees(it.LineTotalPaise)
		it.TaxAmount, it.TaxAmountPaise, it.TaxPercentage, it.AddonTotalPaise = 0, 0, 0, 0
		line := pricing.ItemLine{Ref: cartItemRef(it.ID), Name: it.Name, Quantity: int64(it.Quantity), UnitPaise: it.UnitPricePaise}
		for j := range it.Addons {
			a := &it.Addons[j]
			units := int64(a.Quantity) * int64(it.Quantity)
			a.LineTotalPaise = a.UnitPricePaise * units
			a.UnitPrice, a.LineTotal = rupees(a.UnitPricePaise), rupees(a.LineTotalPaise)
			it.AddonTotalPaise += a.LineTotalPaise
			line.Addons = append(line.Addons, pricing.AddonLine{Ref: cartAddonRef(it.ID, a.AddonID), Name: a.Name, Quantity: units, UnitPaise: a.UnitPricePaise})
		}
		it.AddonTotal = rupees(it.AddonTotalPaise)
		itemsPaise += it.LineTotalPaise
		addonsPaise += it.AddonTotalPaise
		priced.Items = append(priced.Items, line)
	}
	cart.Totals = PriceBreakdown{ItemSubtotal: rupees(itemsPaise), AddonTotal: rupees(addonsPaise)}
	if len(cart.Items) == 0 {
		return
	}
	cart.Totals.PackagingFee = rupees(packagingPaise)
	q, err := pricing.Price(cfg, r, priced, at)
	if err != nil {
		cart.PricingError = pricingErrorFrom(err)
		return
	}
	tax := q.TaxByItem()
	for i := range cart.Items {
		it := &cart.Items[i]
		ref := cartItemRef(it.ID)
		it.TaxAmountPaise = tax[ref]
		it.TaxAmount = rupees(it.TaxAmountPaise)
		if l, ok := q.Line(ref); ok {
			it.TaxPercentage = float64(l.RateBP) / 100
		}
	}
	t := q.Totals
	view := pricing.TaxesAndChargesFrom(q.Breakdown, t)
	cart.TotalsPaise, cart.TaxesAndCharges = &t, &view
	cart.Totals = PriceBreakdown{
		ItemSubtotal: rupees(t.ItemSubtotalPaise), AddonTotal: rupees(t.AddonTotalPaise), PackagingFee: rupees(t.PackagingFeePaise),
		TaxTotal: rupees(t.TaxTotalPaise), DeliveryFee: rupees(t.DeliveryFeePaise), PlatformFee: rupees(t.PlatformFeePaise),
		CouponDiscount: rupees(t.DiscountTotalPaise), FinalAmount: rupees(t.FinalAmountPaise),
	}
}

// loadRestaurantPricing reads the restaurant's tax identity and packaging fee.
func loadRestaurantPricing(ctx context.Context, q rowQuerier, restaurantID uuid.UUID) (pricing.Restaurant, int64, error) {
	var r pricing.Restaurant
	var packaging int64
	err := q.QueryRow(ctx, `
		SELECT COALESCE(tax_category, ''), COALESCE(gstin, ''), COALESCE(gstin_state_code, ''),
			COALESCE(state, ''), ROUND(packaging_fee * 100)::bigint
		FROM food.restaurants
		WHERE id = $1
	`, restaurantID).Scan(&r.TaxCategory, &r.GSTIN, &r.GSTINStateCode, &r.State, &packaging)
	return r, packaging, err
}

// loadOrderMoney reads the paise columns (falling back to NUMERIC for a row
// without them) and the stored breakdown.
func loadOrderMoney(ctx context.Context, q rowQuerier, orderID uuid.UUID) (*OrderMoney, error) {
	var t pricing.Totals
	var raw []byte
	var needs bool
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(item_subtotal_paise, ROUND(item_subtotal * 100)::bigint),
			COALESCE(addon_total_paise, ROUND(addon_total * 100)::bigint),
			COALESCE(packaging_fee_paise, ROUND(packaging_fee * 100)::bigint),
			COALESCE(delivery_fee_paise, ROUND(delivery_fee * 100)::bigint),
			COALESCE(platform_fee_paise, ROUND(platform_fee * 100)::bigint),
			COALESCE(tax_total_paise, ROUND(tax_total * 100)::bigint),
			COALESCE(discount_total_paise, ROUND((restaurant_discount + coupon_discount) * 100)::bigint),
			COALESCE(final_amount_paise, ROUND(final_amount * 100)::bigint),
			tax_breakdown, needs_adviser_confirmation
		FROM food.orders
		WHERE id = $1
	`, orderID).Scan(&t.ItemSubtotalPaise, &t.AddonTotalPaise, &t.PackagingFeePaise, &t.DeliveryFeePaise,
		&t.PlatformFeePaise, &t.TaxTotalPaise, &t.DiscountTotalPaise, &t.FinalAmountPaise, &raw, &needs); err != nil {
		return nil, err
	}
	var b *pricing.Breakdown
	if len(raw) > 0 {
		var decoded pricing.Breakdown
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("decode tax breakdown: %w", err)
		}
		b = &decoded
	}
	return OrderMoneyFrom(t, b, needs), nil
}
