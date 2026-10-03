package tax

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

var at = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// testPlatformGSTIN builds a synthetic, checksum-valid Telangana GSTIN.
func testPlatformGSTIN(t *testing.T) string {
	t.Helper()
	base := "36ZZZCZ0000Z1Z"
	d, err := kyc.GSTINCheckDigit(base)
	if err != nil {
		t.Fatal(err)
	}
	return base + string(d)
}

func TestQuoteCategory(t *testing.T) {
	for fam, want := range map[string]gst.Category{
		FamilyHomeCleaning:       gst.CategoryHomeCleaningViaECO,
		FamilyPestControl:        gst.CategoryPestControlViaECO,
		FamilyApplianceRepair:    gst.CategoryApplianceRepairViaECO,
		FamilyInstallationRepair: gst.CategoryInstallationRepairViaECO,
		FamilyPainting:           gst.CategoryPaintingViaECO,
		FamilyBeautySalon:        gst.CategoryBeautySalonRegistered,
	} {
		got, err := QuoteCategory(fam)
		if err != nil || got != want {
			t.Errorf("QuoteCategory(%s) = %s, %v; want %s", fam, got, err, want)
		}
	}
	if _, err := QuoteCategory("GARDENING"); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("unknown family: %v", err)
	}
}

func TestComputedPathUsesSharedGST(t *testing.T) {
	g, err := NewGST(nil, testPlatformGSTIN(t))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Computed() {
		t.Fatal("platform GSTIN set: want the computed path")
	}
	res, err := g.SplitInclusive(Input{Family: FamilyHomeCleaning, PlaceOfSupplyState: "36", At: at,
		Lines: []Line{{Ref: "line-1", GrossPaise: 49900}, {Ref: "line-2", GrossPaise: 19900}}})
	if err != nil {
		t.Fatal(err)
	}
	var sumGross, sumTax int64
	for _, l := range res.Lines {
		if l.Category != "HOME_CLEANING_VIA_ECO" || l.SAC != "998533" || l.RateBPS != 1800 {
			t.Errorf("line %+v: want HOME_CLEANING_VIA_ECO / 998533 / 1800", l)
		}
		if l.TaxablePaise+l.TaxPaise != l.GrossPaise {
			t.Errorf("line %s does not add up: %+v", l.Ref, l)
		}
		sumGross += l.GrossPaise
		sumTax += l.TaxPaise
	}
	if sumGross != 69800 {
		t.Fatalf("gross %d, want 69800", sumGross)
	}
	// 69800 * 1800 / 11800 = 10647.45... -> tax 10648 (taxable floored).
	if sumTax != 10648 {
		t.Fatalf("tax %d, want 10648", sumTax)
	}
	if !res.Provisional {
		t.Fatal("Doorstep rows are adviser-flagged: want provisional")
	}
}

func TestEstimatePathMatchesFloorSplit(t *testing.T) {
	g, err := NewGST(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.SplitInclusive(Input{Family: FamilyApplianceRepair, PlaceOfSupplyState: "36", At: at,
		Lines: []Line{{Ref: "a", GrossPaise: 59900}}})
	if err != nil {
		t.Fatal(err)
	}
	l := res.Lines[0]
	// floor(59900*10000/11800) = 50762; tax 9138.
	if l.TaxablePaise != 50762 || l.TaxPaise != 9138 || l.Category != "APPLIANCE_REPAIR_VIA_ECO" || l.SAC != "998715" {
		t.Fatalf("estimate = %+v", l)
	}
}

// Salon is registered-only in shared/gst; at quote time it is always the
// 5% estimate, even when the platform GSTIN is configured.
func TestSalonIsEstimatedAtFivePercent(t *testing.T) {
	g, err := NewGST(nil, testPlatformGSTIN(t))
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.SplitInclusive(Input{Family: FamilyBeautySalon, PlaceOfSupplyState: "36", At: at,
		Lines: []Line{{Ref: "a", GrossPaise: 129900}}})
	if err != nil {
		t.Fatal(err)
	}
	l := res.Lines[0]
	// floor(129900*10000/10500) = 123714; tax 6186.
	if l.RateBPS != 500 || l.Category != "BEAUTY_SALON_REGISTERED" || l.TaxablePaise != 123714 || l.TaxPaise != 6186 {
		t.Fatalf("salon = %+v", l)
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := NewGST(nil, "36ABCDE1234F1Z0"); !errors.Is(err, ErrPlatformGSTIN) {
		t.Fatalf("bad GSTIN: %v", err)
	}
	g, _ := NewGST(nil, "")
	if _, err := g.SplitInclusive(Input{Family: FamilyPainting, PlaceOfSupplyState: "99", At: at, Lines: []Line{{GrossPaise: 1}}}); err == nil {
		t.Fatal("invalid place of supply accepted")
	}
	if _, err := g.SplitInclusive(Input{Family: FamilyPainting, PlaceOfSupplyState: "36", At: at, Lines: []Line{{GrossPaise: -1}}}); err == nil {
		t.Fatal("negative amount accepted")
	}
}

func TestExtractInclusive(t *testing.T) {
	for _, c := range []struct {
		gross       int64
		rate        int
		taxable, tx int64
	}{
		{0, 1800, 0, 0}, {100, 0, 100, 0}, {118, 1800, 100, 18}, {105, 500, 100, 5}, {1, 1800, 0, 1},
	} {
		tb, tx := ExtractInclusive(c.gross, c.rate)
		if tb != c.taxable || tx != c.tx {
			t.Errorf("ExtractInclusive(%d,%d) = %d,%d want %d,%d", c.gross, c.rate, tb, tx, c.taxable, c.tx)
		}
	}
}
