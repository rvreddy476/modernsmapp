package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/commerce-service/internal/tax"
	"github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

// storedMoneyFixture is what the P0 checkout stores for a two-line order:
// 2 × ₹1,199 at 5% and 1 × ₹1,180 at 18%, a ₹150 seller coupon on the 18%
// line only (the 5% line is CouponExcluded), ₹49 delivery spread across
// both. The split comes from tax.Compute — the same function the checkout
// stores from — so the fixture is a real stored order, not a hand guess.
func storedMoneyFixture(t *testing.T, interstate bool) *postgres.OrderInvoiceMoney {
	t.Helper()
	lines := []tax.Line{
		{Ref: "a", GrossInclusive: money.Paise(2 * 119900), Rate: tax.Rate5, CouponExcluded: true},
		{Ref: "b", GrossInclusive: money.Paise(118000), Rate: tax.Rate18},
	}
	out, err := tax.Compute(tax.Input{Lines: lines, OrderDiscount: 15000, Shipping: 4900, Interstate: interstate})
	if err != nil {
		t.Fatal(err)
	}
	place := "KA"
	m := &postgres.OrderInvoiceMoney{
		PlaceOfSupplyState: &place, IsInterstate: interstate,
		SubtotalMinor: out.GrossInclusive.Int64(), CouponDiscountMinor: out.OrderDiscount.Int64(),
		ShippingMinor: out.Shipping.Int64(), TaxMinor: out.TotalTax.Int64(), FinalMinor: out.Total.Int64(),
		TaxableMinor: out.TotalTaxable.Int64(), CGSTMinor: out.TotalCGST.Int64(),
		SGSTMinor: out.TotalSGST.Int64(), IGSTMinor: out.TotalIGST.Int64(),
	}
	meta := []struct {
		title, sku, hsn string
		qty             int
		unit            int64
	}{{"Notebook", "NB-1", "4820", 2, 119900}, {"Shirt", "SH-1", "6205", 1, 118000}}
	for i, lt := range out.Lines {
		m.Lines = append(m.Lines, postgres.OrderInvoiceLine{
			ItemID: uuid.New(), Title: meta[i].title, SKU: meta[i].sku, HSN: meta[i].hsn,
			Quantity: meta[i].qty, RateBP: int(lt.Rate), UnitMinor: meta[i].unit,
			DiscountMinor: lt.AllocatedDiscount.Int64(), ShippingMinor: lt.AllocatedShipping.Int64(),
			TaxableMinor: lt.Taxable.Int64(), CGSTMinor: lt.CGST.Int64(), SGSTMinor: lt.SGST.Int64(),
			IGSTMinor: lt.IGST.Int64(), NetMinor: lt.NetInclusive.Int64(), FinalMinor: lt.NetInclusive.Int64(),
		})
	}
	return m
}

func toPaise(r float64) int64 { return int64(math.Round(r * 100)) }

func TestInvoiceFromStoredMoney_EveryLineAndTotalIsTheStoredPaise(t *testing.T) {
	for _, interstate := range []bool{false, true} {
		t.Run(fmt.Sprintf("interstate=%v", interstate), func(t *testing.T) {
			m := storedMoneyFixture(t, interstate)
			// The fixture is the hard case: coupon on exactly one line.
			if m.Lines[0].DiscountMinor != 0 || m.Lines[1].DiscountMinor != 15000 {
				t.Fatalf("coupon allocation %d/%d, want 0/15000", m.Lines[0].DiscountMinor, m.Lines[1].DiscountMinor)
			}
			var inv invoice.Invoice
			if err := invoiceFromStoredMoney(&inv, m); err != nil {
				t.Fatalf("map: %v", err)
			}
			// A later ApplyGST/ComputeTotals must not recompute any of it.
			inv.ApplyGST()
			inv.ComputeTotals()

			if !inv.TaxInclusive || inv.IsInterstate != interstate {
				t.Fatalf("TaxInclusive=%v IsInterstate=%v", inv.TaxInclusive, inv.IsInterstate)
			}
			if len(inv.Items) != len(m.Lines) {
				t.Fatalf("%d items, want %d", len(inv.Items), len(m.Lines))
			}
			for i, l := range m.Lines {
				it := inv.Items[i]
				got := []int64{toPaise(it.UnitPrice), int64(it.Quantity), toPaise(it.Discount), toPaise(it.Shipping),
					toPaise(it.Taxable), toPaise(it.CGSTAmount), toPaise(it.SGSTAmount), toPaise(it.IGSTAmount), toPaise(it.LineTotal)}
				want := []int64{l.UnitMinor, int64(l.Quantity), l.DiscountMinor, l.ShippingMinor,
					l.TaxableMinor, l.CGSTMinor, l.SGSTMinor, l.IGSTMinor, l.NetMinor}
				for k := range want {
					if got[k] != want[k] {
						t.Errorf("line %s field %d = %d, want %d (got %v want %v)", l.SKU, k, got[k], want[k], got, want)
					}
				}
				if it.Title != l.Title || it.SKU != l.SKU || it.HSN != l.HSN {
					t.Errorf("line identity %q/%q/%q, want %q/%q/%q", it.Title, it.SKU, it.HSN, l.Title, l.SKU, l.HSN)
				}
				pct := []float64{it.CGSTPct, it.SGSTPct, it.IGSTPct}
				wantPct := []float64{float64(l.RateBP) / 200, float64(l.RateBP) / 200, 0}
				if interstate {
					wantPct = []float64{0, 0, float64(l.RateBP) / 100}
				}
				for k := range pct {
					if pct[k] != wantPct[k] {
						t.Errorf("line %s pct %v, want %v", l.SKU, pct, wantPct)
					}
				}
				if l.NetMinor == 0 || toPaise(it.LineTotal) == 0 {
					t.Errorf("line %s totals zero", l.SKU)
				}
			}
			totals := []int64{toPaise(inv.Subtotal), toPaise(inv.CouponDiscount), toPaise(inv.ShippingCharges),
				toPaise(inv.TotalTaxable), toPaise(inv.TotalCGST), toPaise(inv.TotalSGST), toPaise(inv.TotalIGST), toPaise(inv.GrandTotal)}
			wantTotals := []int64{m.SubtotalMinor, m.CouponDiscountMinor, m.ShippingMinor,
				m.TaxableMinor, m.CGSTMinor, m.SGSTMinor, m.IGSTMinor, m.FinalMinor}
			for k := range wantTotals {
				if totals[k] != wantTotals[k] {
					t.Errorf("total %d = %d, want %d (got %v want %v)", k, totals[k], wantTotals[k], totals, wantTotals)
				}
			}
			if inv.GrandTotal == 0 {
				t.Fatal("grand total is zero")
			}

			// And the rendered document prints those paise.
			body, _, err := invoice.HTMLRenderer{}.Render(inv)
			if err != nil {
				t.Fatal(err)
			}
			doc := string(body)
			for _, s := range []string{
				"₹" + postgresRupee(m.FinalMinor), "₹" + postgresRupee(m.TaxableMinor),
				"₹" + postgresRupee(m.Lines[1].DiscountMinor), "−₹" + postgresRupee(m.CouponDiscountMinor),
			} {
				if !strings.Contains(doc, s) {
					t.Errorf("rendered invoice is missing %q", s)
				}
			}
		})
	}
}

// postgresRupee is p/100 with two decimals, in integers.
func postgresRupee(p int64) string { return fmt.Sprintf("%d.%02d", p/100, p%100) }

func TestInvoiceFromStoredMoney_RefusesMoneyThatDoesNotReconcile(t *testing.T) {
	// Each corruption is built so that exactly ONE guard sees it — sums are
	// preserved where a per-line identity is the target — so removing any
	// single guard fails its case.
	cases := []struct {
		name       string
		interstate bool
		corrupt    func(m *postgres.OrderInvoiceMoney)
	}{
		{"no lines on an all-zero order", false, func(m *postgres.OrderInvoiceMoney) {
			*m = postgres.OrderInvoiceMoney{PlaceOfSupplyState: m.PlaceOfSupplyState}
		}},
		{"line taxable + GST != net", false, func(m *postgres.OrderInvoiceMoney) {
			m.Lines[0].TaxableMinor++
			m.Lines[1].TaxableMinor--
		}},
		{"line net != final price", false, func(m *postgres.OrderInvoiceMoney) { m.Lines[0].FinalMinor++ }},
		{"line gross − discount + delivery != net", false, func(m *postgres.OrderInvoiceMoney) {
			m.Lines[0].DiscountMinor++
			m.Lines[1].DiscountMinor--
		}},
		{"IGST on an intra-state order", false, func(m *postgres.OrderInvoiceMoney) {
			l := &m.Lines[0]
			moved := l.CGSTMinor + l.SGSTMinor
			m.CGSTMinor -= l.CGSTMinor
			m.SGSTMinor -= l.SGSTMinor
			m.IGSTMinor += moved
			l.IGSTMinor, l.CGSTMinor, l.SGSTMinor = moved, 0, 0
		}},
		{"CGST on an inter-state order", true, func(m *postgres.OrderInvoiceMoney) {
			l := &m.Lines[0]
			c, s := l.IGSTMinor/2, l.IGSTMinor-l.IGSTMinor/2
			m.IGSTMinor -= l.IGSTMinor
			m.CGSTMinor += c
			m.SGSTMinor += s
			l.CGSTMinor, l.SGSTMinor, l.IGSTMinor = c, s, 0
		}},
		{"order subtotal", false, func(m *postgres.OrderInvoiceMoney) { m.SubtotalMinor++ }},
		{"order coupon discount", false, func(m *postgres.OrderInvoiceMoney) { m.CouponDiscountMinor++ }},
		{"order shipping", false, func(m *postgres.OrderInvoiceMoney) { m.ShippingMinor++ }},
		{"order taxable", false, func(m *postgres.OrderInvoiceMoney) { m.TaxableMinor++ }},
		{"order CGST", false, func(m *postgres.OrderInvoiceMoney) { m.CGSTMinor++ }},
		{"order SGST", false, func(m *postgres.OrderInvoiceMoney) { m.SGSTMinor++ }},
		{"order IGST", true, func(m *postgres.OrderInvoiceMoney) { m.IGSTMinor++ }},
		{"order final", false, func(m *postgres.OrderInvoiceMoney) { m.FinalMinor++ }},
		{"order tax != split", false, func(m *postgres.OrderInvoiceMoney) { m.TaxMinor++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := storedMoneyFixture(t, tc.interstate)
			tc.corrupt(m)
			inv := invoice.Invoice{OrderNumber: "ORD-X"}
			err := invoiceFromStoredMoney(&inv, m)
			if !errors.Is(err, errInvoiceMoneyUnreconciled) {
				t.Fatalf("err = %v, want errInvoiceMoneyUnreconciled", err)
			}
			if inv.TaxInclusive || len(inv.Items) != 0 || inv.GrandTotal != 0 {
				t.Fatalf("a refused order still filled the invoice: %+v", inv)
			}
		})
	}
}
