package foodinvoice

import (
	"strings"
	"testing"
)

// Audit 2026-10-30 M2: the invoice names what was supplied and shows the
// refunded substitution credit.
func TestInvoiceShowsSubstitutionCredit(t *testing.T) {
	d := data(t, "RESTAURANT_STANDALONE")
	d.Substitutions = []Substitution{{ItemRef: "item:a", OriginalName: "Paneer Tikka", SuggestedName: "Soya Chaap",
		SupplyDiffPaise: -10000, TaxDiffPaise: -500, PriceDiffPaise: -10500}}
	doc, err := Build(d)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range doc.Sections {
		for _, l := range s.Lines {
			if l.Ref == "item:a" {
				found = l.Description == "Soya Chaap (substituted for Paneer Tikka)"
			}
		}
	}
	if !found {
		t.Fatalf("substituted line not renamed: %+v", doc.Sections)
	}
	if len(doc.Adjustments) != 1 || doc.Adjustments[0].TotalPaise != -10500 || !doc.Adjustments[0].RefundedToBuyer {
		t.Fatalf("adjustments = %+v", doc.Adjustments)
	}
	if doc.NetPayablePaise != doc.GrandTotalPaise-10500 {
		t.Fatalf("net payable %d, grand %d", doc.NetPayablePaise, doc.GrandTotalPaise)
	}
	html, err := RenderHTML(doc)
	if err != nil || !strings.Contains(string(html), "Net payable after substitution credits") {
		t.Fatalf("html missing the credit: %v", err)
	}
}

func TestInvoiceWithoutSubstitutionsIsUnchanged(t *testing.T) {
	doc, err := Build(data(t, "RESTAURANT_STANDALONE"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Adjustments) != 0 || doc.NetPayablePaise != 0 {
		t.Fatalf("adjustments %+v net %d grand %d", doc.Adjustments, doc.NetPayablePaise, doc.GrandTotalPaise)
	}
}
