package service

import (
	"errors"
	"fmt"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/invoice"
)

// errInvoiceMoneyUnreconciled refuses an invoice for an order whose stored
// money does not add up. A GST invoice is a legal document: one that
// disagrees with what was charged is worse than none, and the order needs
// looking at before anything is issued against it.
var errInvoiceMoneyUnreconciled = errors.New("invoice: the order's stored money does not reconcile")

// paiseToRupees converts exactly: every paise amount an order can hold is
// far below 2^53, so float64(p)/100 is the double nearest p/100 and the
// renderer's %.2f prints p/100 to the paisa.
func paiseToRupees(p int64) float64 { return float64(p) / 100 }

// invoiceFromStoredMoney fills the invoice's lines and totals from the paise
// the P0 checkout STORED for the order: per line the GST-inclusive unit
// price, quantity, the allocated discount (a seller coupon's share lands only
// on the lines it applies to — tax.Line.CouponExcluded), the allocated
// delivery, the taxable value and the CGST/SGST/IGST split; and the order's
// own header totals. Nothing is recomputed from a tax-class percentage.
//
// The stored values are checked against each other first — every identity
// the checkout guarantees — and an order that fails one is refused rather
// than invoiced.
func invoiceFromStoredMoney(inv *invoice.Invoice, m *postgres.OrderInvoiceMoney) error {
	if len(m.Lines) == 0 {
		return fmt.Errorf("%w: the order has no lines", errInvoiceMoneyUnreconciled)
	}

	type sums struct {
		gross, disc, ship, taxable, cgst, sgst, igst, net int64
	}
	var got sums
	items := make([]invoice.LineItem, 0, len(m.Lines))
	for _, l := range m.Lines {
		gross := l.UnitMinor * int64(l.Quantity)
		if l.TaxableMinor+l.CGSTMinor+l.SGSTMinor+l.IGSTMinor != l.NetMinor {
			return fmt.Errorf("%w: line %s taxable %d + GST %d/%d/%d != net %d", errInvoiceMoneyUnreconciled,
				l.ItemID, l.TaxableMinor, l.CGSTMinor, l.SGSTMinor, l.IGSTMinor, l.NetMinor)
		}
		if l.NetMinor != l.FinalMinor {
			return fmt.Errorf("%w: line %s net %d != final price %d", errInvoiceMoneyUnreconciled,
				l.ItemID, l.NetMinor, l.FinalMinor)
		}
		if gross-l.DiscountMinor+l.ShippingMinor != l.NetMinor {
			return fmt.Errorf("%w: line %s %d × %d − discount %d + delivery %d != net %d", errInvoiceMoneyUnreconciled,
				l.ItemID, l.UnitMinor, l.Quantity, l.DiscountMinor, l.ShippingMinor, l.NetMinor)
		}
		if (m.IsInterstate && l.CGSTMinor+l.SGSTMinor != 0) || (!m.IsInterstate && l.IGSTMinor != 0) {
			return fmt.Errorf("%w: line %s GST split %d/%d/%d disagrees with the order's interstate=%v",
				errInvoiceMoneyUnreconciled, l.ItemID, l.CGSTMinor, l.SGSTMinor, l.IGSTMinor, m.IsInterstate)
		}
		got.gross += gross
		got.disc += l.DiscountMinor
		got.ship += l.ShippingMinor
		got.taxable += l.TaxableMinor
		got.cgst += l.CGSTMinor
		got.sgst += l.SGSTMinor
		got.igst += l.IGSTMinor
		got.net += l.NetMinor

		it := invoice.LineItem{
			Title: l.Title, SKU: l.SKU, HSN: l.HSN, Quantity: l.Quantity,
			UnitPrice:  paiseToRupees(l.UnitMinor),
			Discount:   paiseToRupees(l.DiscountMinor),
			Shipping:   paiseToRupees(l.ShippingMinor),
			Taxable:    paiseToRupees(l.TaxableMinor),
			CGSTAmount: paiseToRupees(l.CGSTMinor),
			SGSTAmount: paiseToRupees(l.SGSTMinor),
			IGSTAmount: paiseToRupees(l.IGSTMinor),
			LineTotal:  paiseToRupees(l.NetMinor),
		}
		// The stored rate in basis points; CGST takes the floor half, as the
		// checkout split the amount (tax.Compute).
		if m.IsInterstate {
			it.IGSTPct = float64(l.RateBP) / 100
		} else {
			it.CGSTPct = float64(l.RateBP/2) / 100
			it.SGSTPct = float64(l.RateBP-l.RateBP/2) / 100
		}
		items = append(items, it)
	}

	want := sums{
		gross: m.SubtotalMinor, disc: m.CouponDiscountMinor + m.DiscountMinor, ship: m.ShippingMinor,
		taxable: m.TaxableMinor, cgst: m.CGSTMinor, sgst: m.SGSTMinor, igst: m.IGSTMinor, net: m.FinalMinor,
	}
	if got != want {
		return fmt.Errorf("%w: lines sum to %+v, the order stored %+v", errInvoiceMoneyUnreconciled, got, want)
	}
	if m.CGSTMinor+m.SGSTMinor+m.IGSTMinor != m.TaxMinor {
		return fmt.Errorf("%w: GST split %d/%d/%d != order tax %d", errInvoiceMoneyUnreconciled,
			m.CGSTMinor, m.SGSTMinor, m.IGSTMinor, m.TaxMinor)
	}

	inv.TaxInclusive = true
	inv.Items = items
	inv.IsInterstate = m.IsInterstate
	inv.Subtotal = paiseToRupees(m.SubtotalMinor)
	inv.CouponDiscount = paiseToRupees(m.CouponDiscountMinor + m.DiscountMinor)
	inv.ShippingCharges = paiseToRupees(m.ShippingMinor)
	inv.TotalTaxable = paiseToRupees(m.TaxableMinor)
	inv.TotalCGST = paiseToRupees(m.CGSTMinor)
	inv.TotalSGST = paiseToRupees(m.SGSTMinor)
	inv.TotalIGST = paiseToRupees(m.IGSTMinor)
	inv.GrandTotal = paiseToRupees(m.FinalMinor)
	return nil
}
