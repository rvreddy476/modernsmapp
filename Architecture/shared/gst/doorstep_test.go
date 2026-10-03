package gst

// Doorstep (home services): a service professional supplies through the
// platform. Each family has a _VIA_ECO category (unregistered professional,
// ECO liable under s.9(5)) and a _REGISTERED category (professional liable,
// GSTIN required, TCS marker); beauty has _REGISTERED only. Doorstep prices
// are GST-inclusive, so the goldens are inclusive.

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// Hyderabad, Telangana: GST state 36.
const hyd = "36"

func doorstepInput() Input {
	return Input{
		Mode:               ModeInclusive,
		InvoiceDate:        onDate,
		ThroughECO:         true,
		Platform:           Party{GSTIN: gstinFor(hyd, 'C')},
		PlaceOfSupplyState: hyd,
	}
}

// An individual (PAN holder type P): a gig professional is a person.
func registeredPro() Party { return Party{GSTIN: gstinFor(hyd, 'P')} }

type doorstepFamily struct {
	name     string
	via, reg Category // via is "" for beauty (registered only)
	sac      string
	rate     RateBP
	itcReg   bool
}

var doorstepFamilies = []doorstepFamily{
	{"home cleaning", CategoryHomeCleaningViaECO, CategoryHomeCleaningRegistered, "998533", 1800, true},
	{"pest control", CategoryPestControlViaECO, CategoryPestControlRegistered, "998531", 1800, true},
	{"appliance repair", CategoryApplianceRepairViaECO, CategoryApplianceRepairRegistered, "998715", 1800, true},
	{"installation and repair", CategoryInstallationRepairViaECO, CategoryInstallationRepairRegistered, "995469", 1800, true},
	{"painting", CategoryPaintingViaECO, CategoryPaintingRegistered, "995473", 1800, true},
	{"beauty / salon", "", CategoryBeautySalonRegistered, "999722", 500, false},
}

func TestSyntheticProfessionalGSTINIsValid(t *testing.T) {
	in := doorstepInput()
	in.ServiceProfessional = registeredPro()
	in.Lines = []Line{{Ref: "x", Category: CategoryHomeCleaningRegistered, Amount: 100}}
	mustCompute(t, in)
}

func TestDoorstep_RateRows(t *testing.T) {
	tab := DefaultRateTable()
	check := func(c Category, eco bool, rate RateBP, itc bool, sac string) {
		t.Helper()
		row, err := tab.Lookup(c, onDate)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if row.Supplier != SupplierServiceProfessional || row.ECOSection95 != eco || row.RateBP != rate || row.ITCAvailable != itc || row.SAC != sac {
			t.Errorf("%s = %+v", c, row)
		}
		if !row.NeedsAdviserConfirmation || !row.EffectiveFrom.Equal(rateRationalisationDate) || row.Note == "" {
			t.Errorf("%s: adviser flag %v, effective %v, note %q", c, row.NeedsAdviserConfirmation, row.EffectiveFrom, row.Note)
		}
	}
	for _, f := range doorstepFamilies {
		if f.via != "" {
			check(f.via, true, f.rate, false, f.sac)
		}
		check(f.reg, false, f.rate, f.itcReg, f.sac)
	}
	// Beauty is not under s.9(5): there must be no _VIA_ECO row for it.
	if _, err := tab.Lookup(Category("BEAUTY_SALON_VIA_ECO"), onDate); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("beauty via ECO must not exist: %v", err)
	}
}

// Inclusive goldens, one per family (amounts are what the customer pays).
// taxable = floor(amount * 10000 / (10000 + rate)), tax = amount - taxable,
// CGST = floor(tax / 2), SGST = the rest. Worked by hand / shell arithmetic,
// not by this package.
func TestDoorstep_InclusiveGoldens(t *testing.T) {
	cases := []struct {
		category                         Category
		amount, taxable, tax, cgst, sgst Paise
	}{
		// 1,499.00 incl 18%.
		{CategoryHomeCleaningViaECO, 149900, 127033, 22867, 11433, 11434},
		{CategoryHomeCleaningRegistered, 149900, 127033, 22867, 11433, 11434},
		// 1,199.00 incl 18%.
		{CategoryPestControlViaECO, 119900, 101610, 18290, 9145, 9145},
		{CategoryPestControlRegistered, 119900, 101610, 18290, 9145, 9145},
		// 599.00 incl 18%.
		{CategoryApplianceRepairViaECO, 59900, 50762, 9138, 4569, 4569},
		{CategoryApplianceRepairRegistered, 59900, 50762, 9138, 4569, 4569},
		// 299.00 incl 18%.
		{CategoryInstallationRepairViaECO, 29900, 25338, 4562, 2281, 2281},
		{CategoryInstallationRepairRegistered, 29900, 25338, 4562, 2281, 2281},
		// 12,500.00 incl 18%.
		{CategoryPaintingViaECO, 1250000, 1059322, 190678, 95339, 95339},
		{CategoryPaintingRegistered, 1250000, 1059322, 190678, 95339, 95339},
		// 999.00 incl 5%.
		{CategoryBeautySalonRegistered, 99900, 95142, 4758, 2379, 2379},
	}
	tab := DefaultRateTable()
	for _, c := range cases {
		row, _ := tab.Lookup(c.category, onDate)
		in := doorstepInput()
		if !row.ECOSection95 {
			in.ServiceProfessional = registeredPro()
		}
		in.Lines = []Line{{Ref: "svc", Category: c.category, Amount: c.amount}}
		res := mustCompute(t, in)
		l := res.Lines[0]
		if l.Gross != c.amount || l.Taxable != c.taxable || l.Tax != c.tax || l.CGST != c.cgst || l.SGST != c.sgst || l.IGST != 0 || l.Interstate {
			t.Errorf("%s: %+v", c.category, l)
		}
		if res.Total != c.amount || res.TotalTax != c.tax {
			t.Errorf("%s: total %d tax %d", c.category, res.Total, res.TotalTax)
		}
		if !l.NeedsAdviserConfirmation || !res.NeedsAdviserConfirmation {
			t.Errorf("%s: not flagged for adviser confirmation", c.category)
		}
		if l.Supplier != SupplierServiceProfessional {
			t.Errorf("%s: supplier %s", c.category, l.Supplier)
		}
	}
}

// _VIA_ECO: the ECO is liable under s.9(5), its GSTIN is on the line, no TCS.
func TestDoorstep_ViaECOMarksECOLiability(t *testing.T) {
	for _, f := range doorstepFamilies {
		if f.via == "" {
			continue
		}
		in := doorstepInput() // professional has no GSTIN at all
		in.Lines = []Line{{Ref: "svc", Category: f.via, Amount: 49900}}
		l := mustCompute(t, in).Lines[0]
		if l.Liability != LiabilityECOSection95 || l.LiableParty != SupplierPlatform || l.LiablePartyGSTIN != gstinFor(hyd, 'C') ||
			l.ECOCollectsTCS || l.ITCAvailable || l.SupplierState != hyd {
			t.Errorf("%s via ECO: %+v", f.name, l)
		}
	}
	// A state code without a GSTIN is still an unregistered professional.
	in := doorstepInput()
	in.ServiceProfessional = Party{StateCode: hyd}
	in.Lines = []Line{{Ref: "svc", Category: CategoryPaintingViaECO, Amount: 49900}}
	if l := mustCompute(t, in).Lines[0]; l.Liability != LiabilityECOSection95 {
		t.Fatalf("unregistered professional with a state code: %+v", l)
	}
}

// _REGISTERED: the professional is liable, its GSTIN is on the line, and the
// ECO collects TCS (marker).
func TestDoorstep_RegisteredIsSupplierLiableWithTCS(t *testing.T) {
	for _, f := range doorstepFamilies {
		in := doorstepInput()
		in.ServiceProfessional = registeredPro()
		in.Lines = []Line{{Ref: "svc", Category: f.reg, Amount: 49900}}
		l := mustCompute(t, in).Lines[0]
		if l.Liability != LiabilitySupplier || l.LiableParty != SupplierServiceProfessional ||
			l.LiablePartyGSTIN != gstinFor(hyd, 'P') || !l.ECOCollectsTCS || l.SupplierState != hyd {
			t.Errorf("%s registered: %+v", f.name, l)
		}
	}
	// Outside the ECO a registered professional is still liable (no refusal,
	// unlike the delivery partner and the driver), and there is no TCS.
	in := doorstepInput()
	in.ThroughECO = false
	in.ServiceProfessional = registeredPro()
	in.Lines = []Line{{Ref: "svc", Category: CategoryHomeCleaningRegistered, Amount: 49900}}
	if l := mustCompute(t, in).Lines[0]; l.Liability != LiabilitySupplier || l.ECOCollectsTCS || l.LiableParty != SupplierServiceProfessional {
		t.Fatalf("registered professional outside the ECO: %+v", l)
	}
}

func TestDoorstep_Refusals(t *testing.T) {
	cases := []struct {
		name     string
		category Category
		mutate   func(in *Input)
		want     error
	}{
		{"unregistered professional on a _REGISTERED line", CategoryHomeCleaningRegistered,
			func(in *Input) {}, ErrLiablePartyUnregistered},
		{"unregistered professional (state code only) on a _REGISTERED line", CategoryPaintingRegistered,
			func(in *Input) { in.ServiceProfessional = Party{StateCode: hyd} }, ErrLiablePartyUnregistered},
		{"unregistered beautician", CategoryBeautySalonRegistered,
			func(in *Input) {}, ErrLiablePartyUnregistered},
		{"registered professional on a _VIA_ECO line", CategoryApplianceRepairViaECO,
			func(in *Input) { in.ServiceProfessional = registeredPro() }, ErrUnsupportedSupply},
		{"registered professional on a _VIA_ECO line outside the ECO", CategoryApplianceRepairViaECO,
			func(in *Input) { in.ThroughECO = false; in.ServiceProfessional = registeredPro() }, ErrUnsupportedSupply},
		{"_VIA_ECO outside the ECO: the unregistered professional becomes liable", CategoryPestControlViaECO,
			func(in *Input) { in.ThroughECO = false }, ErrLiablePartyUnregistered},
		{"platform without GSTIN under 9(5)", CategoryHomeCleaningViaECO,
			func(in *Input) { in.Platform = Party{StateCode: hyd} }, ErrLiablePartyUnregistered},
		{"professional GSTIN disagrees with its state code", CategoryHomeCleaningRegistered,
			func(in *Input) { in.ServiceProfessional = Party{GSTIN: gstinFor(hyd, 'P'), StateCode: "29"} }, ErrPartyState},
		{"professional state code invalid", CategoryHomeCleaningViaECO,
			func(in *Input) { in.ServiceProfessional = Party{StateCode: "99"} }, ErrPartyState},
		{"before the seed date", CategoryHomeCleaningViaECO,
			func(in *Input) { in.InvoiceDate = rateRationalisationDate.AddDate(0, 0, -1) }, ErrNoRateInEffect},
	}
	for _, c := range cases {
		in := doorstepInput()
		in.Lines = []Line{{Ref: "svc", Category: c.category, Amount: 49900}}
		c.mutate(&in)
		if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

// A realistic booking: two services from one unregistered professional, plus
// the platform's fee, inclusive. The professional's lines group into one
// rounding group; the fee is the platform's own supply.
func TestDoorstep_BookingWithPlatformFee(t *testing.T) {
	in := doorstepInput()
	in.Lines = []Line{
		{Ref: "bathroom", Category: CategoryHomeCleaningViaECO, Amount: 49900},
		{Ref: "kitchen", Category: CategoryHomeCleaningViaECO, Amount: 99900},
		{Ref: "fee", Category: CategoryPlatformFee, Amount: 4900},
	}
	res := mustCompute(t, in)
	// Group 149800 incl 18%: taxable floor(1498000000/11800) = 126949, tax
	// 22851. Fee 4900 incl 18%: taxable floor(49000000/11800) = 4152, tax 748.
	clean := res.Lines[0].Tax + res.Lines[1].Tax
	if clean != 22851 || res.Lines[2].Tax != 748 || res.Total != 154700 || res.TotalTax != 22851+748 {
		t.Fatalf("booking: lines %+v total %d tax %d", res.Lines, res.Total, res.TotalTax)
	}
	// Both liabilities land on the platform.
	if len(res.ByLiableParty) != 2 {
		t.Fatalf("by liable party = %+v", res.ByLiableParty)
	}
	for _, pt := range res.ByLiableParty {
		if pt.LiableParty != SupplierPlatform {
			t.Fatalf("liable party %+v", pt)
		}
	}
}

// Interstate place of supply on a registered professional: IGST.
func TestDoorstep_InterstateUsesIGST(t *testing.T) {
	in := doorstepInput()
	in.ServiceProfessional = registeredPro()
	in.PlaceOfSupplyState = "29"
	in.Lines = []Line{{Ref: "svc", Category: CategoryHomeCleaningRegistered, Amount: 11800}}
	l := mustCompute(t, in).Lines[0]
	if !l.Interstate || l.IGST != 1800 || l.CGST != 0 || l.SGST != 0 || l.Taxable != 10000 {
		t.Fatalf("interstate %+v", l)
	}
}

// Random Doorstep baskets in both modes stay exact to the paise and classify
// every line the way the categories promise.
func TestDoorstep_PropertyRandomBaskets(t *testing.T) {
	iterations := 5000
	if testing.Short() {
		iterations = 500
	}
	rng := rand.New(rand.NewSource(20261004))
	tab := DefaultRateTable()
	states := []string{hyd, "29", "27"}
	for iter := 0; iter < iterations; iter++ {
		registered := rng.Intn(2) == 0
		in := Input{
			InvoiceDate:        onDate,
			ThroughECO:         true,
			Platform:           Party{GSTIN: gstinFor(states[rng.Intn(len(states))], 'C')},
			PlaceOfSupplyState: states[rng.Intn(len(states))],
			Mode:               ModeInclusive,
		}
		if rng.Intn(3) == 0 {
			in.Mode = ModeExclusive
		}
		if registered {
			in.ServiceProfessional = Party{GSTIN: gstinFor(states[rng.Intn(len(states))], 'P')}
		}
		n := 1 + rng.Intn(6)
		for i := 0; i < n; i++ {
			f := doorstepFamilies[rng.Intn(len(doorstepFamilies))]
			c := f.reg
			if !registered {
				c = f.via
				if c == "" { // beauty has no via-ECO row
					c = CategoryPlatformFee
				}
			}
			if rng.Intn(5) == 0 {
				c = CategoryPlatformFee
			}
			in.Lines = append(in.Lines, Line{Ref: fmt.Sprintf("l%d", i), Category: c, Amount: Paise(rng.Int63n(2_000_000))})
		}
		res, err := Compute(tab, in)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		var gross, tax Paise
		for i, l := range res.Lines {
			row, _ := tab.Lookup(in.Lines[i].Category, onDate)
			if row.Supplier == SupplierServiceProfessional {
				wantLiable, wantLiability := SupplierServiceProfessional, LiabilitySupplier
				if row.ECOSection95 {
					wantLiable, wantLiability = SupplierPlatform, LiabilityECOSection95
				}
				if l.LiableParty != wantLiable || l.Liability != wantLiability || l.ECOCollectsTCS != (wantLiability == LiabilitySupplier) {
					t.Fatalf("iter %d line %d: %+v", iter, i, l)
				}
			}
			if l.Taxable+l.Tax != l.Gross || l.CGST+l.SGST+l.IGST != l.Tax {
				t.Fatalf("iter %d line %d arithmetic: %+v", iter, i, l)
			}
			if (in.Mode == ModeInclusive && l.Gross != l.Amount) || (in.Mode == ModeExclusive && l.Taxable != l.Amount) {
				t.Fatalf("iter %d line %d net: %+v", iter, i, l)
			}
			gross += l.Gross
			tax += l.Tax
		}
		if res.Total != gross || res.TotalTax != tax || !res.NeedsAdviserConfirmation {
			t.Fatalf("iter %d totals", iter)
		}
	}
}
