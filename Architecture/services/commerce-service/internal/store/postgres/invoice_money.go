package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// OrderInvoiceMoney is the money an order STORED when it was charged, in
// paise, as an invoice must print it: the order header totals and, per line,
// the taxable value and the CGST/SGST/IGST split the P0 checkout extracted
// (migration 010).
//
// IssueInvoice used to read the NUMERIC rupee columns of order_items, which
// the P0 checkout writes as 0.00, so every invoice since that checkout went
// out at ₹0. Nothing here is recomputed: tax recomputed from a tax-class
// percentage can round differently from the tax the buyer actually paid.
type OrderInvoiceMoney struct {
	// PlaceOfSupplyState is set by the P0 checkout on every order it writes
	// and by nothing else (the RFQ conversion still writes rupee columns
	// through CreateOrder). It is what tells an order that stored a GST
	// split from one that did not.
	PlaceOfSupplyState *string
	IsInterstate       bool

	SubtotalMinor       int64 // sum of unit price × quantity, GST-inclusive
	DiscountMinor       int64 // order-level discount_amount_minor (0 on a P0 order)
	CouponDiscountMinor int64
	ShippingMinor       int64
	TaxMinor            int64
	FinalMinor          int64
	TaxableMinor        int64
	CGSTMinor           int64
	SGSTMinor           int64
	IGSTMinor           int64

	Lines []OrderInvoiceLine
}

// OrderInvoiceLine is one order_items row's stored money.
type OrderInvoiceLine struct {
	ItemID    uuid.UUID
	Title     string
	SKU       string
	HSN       string
	Quantity  int
	RateBP    int
	UnitMinor int64 // GST-inclusive unit price charged
	// DiscountMinor is the line's allocated share of the order discount (a
	// seller coupon's share lands only on the lines it applies to).
	DiscountMinor int64
	ShippingMinor int64 // the line's allocated share of delivery
	TaxableMinor  int64
	CGSTMinor     int64
	SGSTMinor     int64
	IGSTMinor     int64
	NetMinor      int64 // net_inclusive_minor: what this line contributed to the charge
	FinalMinor    int64 // final_price_minor; equal to NetMinor on a P0 line
}

// HasStoredSplit reports whether the order was written by the P0 checkout
// and so carries a stored per-line GST split.
func (m *OrderInvoiceMoney) HasStoredSplit() bool {
	return m.PlaceOfSupplyState != nil
}

// GetOrderInvoiceMoney reads the order's stored paise for its invoice.
func (s *Store) GetOrderInvoiceMoney(ctx context.Context, orderID uuid.UUID) (*OrderInvoiceMoney, error) {
	m := &OrderInvoiceMoney{}
	if err := s.db.QueryRow(ctx, `
		SELECT place_of_supply_state, is_interstate,
		       COALESCE(subtotal_minor,0), COALESCE(discount_amount_minor,0),
		       COALESCE(coupon_discount_minor,0), COALESCE(shipping_charges_minor,0),
		       COALESCE(tax_amount_minor,0), COALESCE(final_amount_minor,0),
		       taxable_minor, cgst_minor, sgst_minor, igst_minor
		  FROM orders WHERE id = $1`, orderID).Scan(
		&m.PlaceOfSupplyState, &m.IsInterstate,
		&m.SubtotalMinor, &m.DiscountMinor, &m.CouponDiscountMinor, &m.ShippingMinor,
		&m.TaxMinor, &m.FinalMinor, &m.TaxableMinor, &m.CGSTMinor, &m.SGSTMinor, &m.IGSTMinor); err != nil {
		return nil, fmt.Errorf("invoice money: order %s: %w", orderID, err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, COALESCE(product_title,''), COALESCE(sku,''), COALESCE(hsn_code,''), quantity, tax_rate_bp,
		       COALESCE(unit_price_minor,0), allocated_discount_minor, allocated_shipping_minor,
		       taxable_minor, cgst_minor, sgst_minor, igst_minor,
		       net_inclusive_minor, COALESCE(final_price_minor,0)
		  FROM order_items WHERE order_id = $1
		 ORDER BY created_at, variant_id, id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("invoice money: lines of %s: %w", orderID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var l OrderInvoiceLine
		if err := rows.Scan(&l.ItemID, &l.Title, &l.SKU, &l.HSN, &l.Quantity, &l.RateBP,
			&l.UnitMinor, &l.DiscountMinor, &l.ShippingMinor,
			&l.TaxableMinor, &l.CGSTMinor, &l.SGSTMinor, &l.IGSTMinor,
			&l.NetMinor, &l.FinalMinor); err != nil {
			return nil, err
		}
		m.Lines = append(m.Lines, l)
	}
	return m, rows.Err()
}
