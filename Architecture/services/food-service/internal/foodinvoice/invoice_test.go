package foodinvoice

import (
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/shared/kyc"
)

func synthGSTIN(t *testing.T, state, pan string) string {
	t.Helper()
	d, err := kyc.GSTINCheckDigit(state + pan + "1Z")
	if err != nil {
		t.Fatal(err)
	}
	return state + pan + "1Z" + string(d)
}

func quote(t *testing.T, category string) *pricing.Quote {
	t.Helper()
	cfg := pricing.DefaultConfig()
	cfg.PlatformGSTIN = synthGSTIN(t, "29", "ZZZCZ9999Z")
	q, err := pricing.Price(cfg, pricing.Restaurant{TaxCategory: category, State: "29", GSTIN: synthGSTIN(t, "29", "ZZZPZ0000Z")},
		pricing.Cart{
			Items: []pricing.ItemLine{{Ref: "item:a", Name: "Paneer Tikka", Quantity: 2, UnitPaise: 25000,
				Addons: []pricing.AddonLine{{Ref: "addon:b", Name: "Extra cheese", Quantity: 2, UnitPaise: 3000}}}},
			PackagingPaise: 2000,
		}, time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func data(t *testing.T, category string) Data {
	q := quote(t, category)
	b := q.Breakdown
	return Data{
		OrderNumber: "FG1", PlacedAt: time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
		Restaurant:            Party{Name: "Test Kitchen", LegalName: "Test Kitchens LLP", GSTIN: synthGSTIN(t, "29", "ZZZPZ0000Z"), State: "Karnataka"},
		Buyer:                 Party{Name: "Test Customer", State: "Karnataka"},
		Breakdown:             &b,
		FinalAmountPaise:      q.Totals.FinalAmountPaise,
		PlatformInvoiceNumber: "FP/2627/000001", RestaurantInvoiceNumber: "FR/2627/000001",
		Descriptions: map[string]LineDescription{
			"item:a":  {Description: "Paneer Tikka", Quantity: 2, UnitPricePaise: 25000},
			"addon:b": {Description: "Extra cheese", Quantity: 2, UnitPricePaise: 3000},
		},
	}
}

func TestSection95OrderIsOnePlatformSection(t *testing.T) {
	doc, err := Build(data(t, "RESTAURANT_STANDALONE"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].Issuer != IssuerPlatform {
		t.Fatalf("sections = %+v", doc.Sections)
	}
	s := doc.Sections[0]
	if s.InvoiceNumber != "FP/2627/000001" || s.IssuerGSTIN == "" || len(s.Lines) != 5 {
		t.Fatalf("platform section = %+v", s)
	}
	var s95 int
	for _, l := range s.Lines {
		if l.Liability == "ECO_SECTION_9_5" {
			s95++
			if l.SuppliedBy != "Test Kitchen" {
				t.Fatalf("s.9(5) line %s does not name the restaurant: %+v", l.Ref, l)
			}
		}
	}
	if s95 != 3 || doc.GrandTotalPaise != 64912 || s.TotalPaise != 64912 {
		t.Fatalf("s95 lines %d, grand %d, section %d", s95, doc.GrandTotalPaise, s.TotalPaise)
	}
}

func TestSupplierLiableOrderHasTwoSectionsAndRestaurantGSTIN(t *testing.T) {
	d := data(t, "RESTAURANT_SPECIFIED_PREMISES")
	doc, err := Build(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 2 || doc.Sections[0].Issuer != IssuerRestaurant || doc.Sections[1].Issuer != IssuerPlatform {
		t.Fatalf("sections = %+v", doc.Sections)
	}
	r, p := doc.Sections[0], doc.Sections[1]
	if r.IssuerGSTIN != d.Restaurant.GSTIN || r.InvoiceNumber != "FR/2627/000001" || r.IssuerName != "Test Kitchens LLP" {
		t.Fatalf("restaurant section = %+v", r)
	}
	if r.TaxablePaise != 58000 || r.TaxPaise != 10440 || p.TaxablePaise != 3400 || p.TaxPaise != 612 {
		t.Fatalf("section totals r=%+v p=%+v", r, p)
	}
	if r.TotalPaise+p.TotalPaise != doc.GrandTotalPaise || doc.GrandTotalPaise != d.FinalAmountPaise {
		t.Fatalf("sections %d + %d != grand %d", r.TotalPaise, p.TotalPaise, doc.GrandTotalPaise)
	}
	for _, l := range r.Lines {
		if l.TaxablePaise+l.TaxPaise != l.TotalPaise {
			t.Fatalf("line %s: %+v", l.Ref, l)
		}
	}
}

func TestAdviserMarkerIsVisible(t *testing.T) {
	doc, err := Build(data(t, "RESTAURANT_STANDALONE"))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.NeedsAdviserConfirmation || doc.AdviserMarker != AdviserMarker {
		t.Fatalf("marker = %q (%v)", doc.AdviserMarker, doc.NeedsAdviserConfirmation)
	}
	html, err := RenderHTML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Tax rates pending adviser confirmation") {
		t.Fatal("rendered invoice does not show the adviser marker")
	}
	if !strings.Contains(string(html), "FP/2627/000001") {
		t.Fatal("rendered invoice does not show the platform invoice number")
	}
}

// The legacy understatement: line_total is already pre-tax, so the taxable
// value is the line total itself, never line total minus tax.
func TestLegacyLineTaxableIsTheLineTotal(t *testing.T) {
	l := LegacyLine(LegacyItem{Name: "Dosa", Quantity: 2, UnitPricePaise: 10000, LineTotalPaise: 20000, TaxPaise: 1000, TaxPercent: 5})
	if l.TaxablePaise != 20000 || l.TaxPaise != 1000 || l.TotalPaise != 21000 || l.CGSTPaise+l.SGSTPaise != 1000 {
		t.Fatalf("legacy line = %+v", l)
	}
	doc, err := Build(Data{OrderNumber: "FG-old", PlacedAt: time.Now(), Restaurant: Party{Name: "Old"},
		Legacy: []LegacyItem{{Name: "Dosa", Quantity: 2, UnitPricePaise: 10000, LineTotalPaise: 20000, TaxPaise: 1000, TaxPercent: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Legacy || len(doc.Sections) != 1 || doc.Sections[0].TaxablePaise != 20000 || doc.AdviserMarker != AdviserMarker {
		t.Fatalf("legacy doc = %+v", doc)
	}
}
