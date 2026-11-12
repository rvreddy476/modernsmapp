package gst

import (
	"errors"
	"testing"
)

// The platform's own digital subscription (Momentum Premium): GST-exclusive
// prices, the platform supplies and is liable, never s.9(5).
func TestPlatformDigitalSubscription(t *testing.T) {
	cases := []struct {
		name       string
		pos        string
		taxable    Paise
		tax, total Paise
		interstate bool
	}{
		{"30 days, intra-state", "29", 14900, 2682, 17582, false},
		{"365 days, intra-state", "29", 149900, 26982, 176882, false},
		{"30 days, inter-state", "36", 14900, 2682, 17582, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Mode: ModeExclusive, InvoiceDate: onDate, Platform: Party{GSTIN: gstinFor("29", 'C')},
				PlaceOfSupplyState: c.pos, Lines: []Line{{Ref: "pass", Category: CategoryPlatformDigitalSubscription, Amount: c.taxable}}}
			res := mustCompute(t, in)
			l := res.Lines[0]
			if l.Taxable != c.taxable || l.Tax != c.tax || l.Gross != c.total || res.Total != c.total {
				t.Fatalf("got taxable %d tax %d gross %d total %d", l.Taxable, l.Tax, l.Gross, res.Total)
			}
			if l.Supplier != SupplierPlatform || l.LiableParty != SupplierPlatform || l.Liability != LiabilitySupplier || l.ECOCollectsTCS {
				t.Fatalf("liability %+v", l)
			}
			if l.SAC != "998439" || l.RateBP != 1800 || !res.NeedsAdviserConfirmation {
				t.Fatalf("row %s %d adviser %t", l.SAC, l.RateBP, res.NeedsAdviserConfirmation)
			}
			if c.interstate {
				if l.IGST != c.tax || l.CGST != 0 || l.SGST != 0 {
					t.Fatalf("inter-state split %d/%d/%d", l.CGST, l.SGST, l.IGST)
				}
			} else if l.CGST != c.tax/2 || l.SGST != c.tax-c.tax/2 || l.IGST != 0 {
				t.Fatalf("intra-state split %d/%d/%d", l.CGST, l.SGST, l.IGST)
			}
		})
	}
	// The platform is liable: without its GSTIN nothing is computed.
	in := Input{Mode: ModeExclusive, InvoiceDate: onDate, ThroughECO: true, PlaceOfSupplyState: "29",
		Lines: []Line{{Category: CategoryPlatformDigitalSubscription, Amount: 14900}}}
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrLiablePartyUnregistered) {
		t.Fatalf("no platform GSTIN: %v", err)
	}
}
