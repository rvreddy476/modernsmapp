package invoice

import (
	"strings"
	"testing"
	"time"
)

func inclusiveInvoice() Invoice {
	return Invoice{
		Number: "PBK/2026-27/000001", Date: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		OrderNumber: "ORD-1", OrderDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Seller: Party{Address: Address{State: "KA"}}, ShipTo: Address{State: "MH"},
		TaxInclusive: true, IsInterstate: false, CouponCode: "SAVE10",
		Items: []LineItem{{
			Title: "Notebook", SKU: "NB-1", HSN: "4820", Quantity: 2,
			UnitPrice: 1199, Discount: 0, Shipping: 49, Taxable: 2330.47,
			CGSTPct: 2.5, SGSTPct: 2.5, CGSTAmount: 58.26, SGSTAmount: 58.27, LineTotal: 2447,
		}},
		Subtotal: 2398, ShippingCharges: 49, CouponDiscount: 0,
		TotalTaxable: 2330.47, TotalCGST: 58.26, TotalSGST: 58.27, GrandTotal: 2447,
	}
}

// A TaxInclusive invoice carries the GST the order charged; ApplyGST and
// ComputeTotals must not recompute any of it — not the interstate flag
// (Seller KA vs ShipTo MH here would flip it), not a line, not a total.
func TestTaxInclusiveInvoiceIsNeverRecomputed(t *testing.T) {
	inv := inclusiveInvoice()
	want := inclusiveInvoice()
	inv.ApplyGST()
	inv.ComputeTotals()
	if inv.IsInterstate != want.IsInterstate || inv.Subtotal != want.Subtotal || inv.GrandTotal != want.GrandTotal ||
		inv.TotalCGST != want.TotalCGST || inv.TotalSGST != want.TotalSGST || inv.TotalIGST != want.TotalIGST ||
		inv.Items[0] != want.Items[0] {
		t.Fatalf("a TaxInclusive invoice was recomputed:\n got %+v\nwant %+v", inv, want)
	}
}

// The legacy (tax-exclusive) computation is unchanged.
func TestLegacyInvoiceStillComputes(t *testing.T) {
	inv := Invoice{
		Seller: Party{Address: Address{State: "KA"}}, ShipTo: Address{State: "KA"},
		Items:           []LineItem{{Quantity: 1, UnitPrice: 900, Taxable: 900, CGSTPct: 9, SGSTPct: 9, IGSTPct: 18}},
		ShippingCharges: 0,
	}
	inv.ApplyGST()
	inv.ComputeTotals()
	if inv.IsInterstate || inv.TotalCGST != 81 || inv.TotalSGST != 81 || inv.Subtotal != 900 || inv.GrandTotal != 1062 {
		t.Fatalf("legacy computation changed: %+v", inv)
	}
}

func TestTaxInclusiveRendering(t *testing.T) {
	body, _, err := HTMLRenderer{}.Render(inclusiveInvoice())
	if err != nil {
		t.Fatal(err)
	}
	doc := string(body)
	for _, s := range []string{
		"Rate (incl. GST)", "Taxable value", "Delivery", // columns
		"₹1199.00", "₹49.00", "₹2330.47", "2.50%<br/>₹58.26", "2.50%<br/>₹58.27", "₹2447.00", // the line
		"<td>Items (incl. GST)</td><td class=\"num\">₹2398.00</td>",
		"<td>Delivery (incl. GST)</td><td class=\"num\">₹49.00</td>",
		"<td>Grand Total</td><td class=\"num\">₹2447.00</td>",
		"<td>Taxable value</td><td class=\"num\">₹2330.47</td>",
		"<td>CGST</td><td class=\"num\">₹58.26</td>",
		"<td>SGST</td><td class=\"num\">₹58.27</td>",
	} {
		if !strings.Contains(doc, s) {
			t.Errorf("inclusive invoice is missing %q", s)
		}
	}
	// The line row itself carries its delivery share and taxable value (the
	// same figures appear once more in the totals of a one-line invoice).
	for _, s := range []string{"₹49.00", "₹2330.47"} {
		if n := strings.Count(doc, s); n != 2 {
			t.Errorf("%q appears %d times, want 2 (line row + totals)", s, n)
		}
	}
	// The exclusive layout would print the taxable value as the subtotal and
	// add delivery on top of a total that already contains it.
	if strings.Contains(doc, "Subtotal (Taxable)") {
		t.Error("inclusive invoice rendered the tax-exclusive totals")
	}

	inv := inclusiveInvoice()
	inv.CouponDiscount = 150
	body, _, _ = HTMLRenderer{}.Render(inv)
	if !strings.Contains(string(body), "<td>Discount (SAVE10)</td><td class=\"num\">−₹150.00</td>") {
		t.Error("inclusive invoice does not show the coupon discount")
	}

	legacy := inclusiveInvoice()
	legacy.TaxInclusive = false
	body, _, _ = HTMLRenderer{}.Render(legacy)
	if !strings.Contains(string(body), "Subtotal (Taxable)") || strings.Contains(string(body), "Rate (incl. GST)") {
		t.Error("the legacy layout changed")
	}
}
