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
	via, reg Category // via is "" for a family not under s.9(5) (registered only)
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
	// B1 families (4 Oct 2026): construction has both; the rest registered only.
	{"car care", "", CategoryCarCareRegistered, "998714", 1800, true},
	{"home staffing", "", CategoryHomeStaffingRegistered, "999800", 1800, true},
	{"relocation", "", CategoryRelocationRegistered, "996791", 1800, true},
	{"photography", "", CategoryPhotographyRegistered, "998383", 1800, true},
	{"fitness and wellness", "", CategoryFitnessWellnessRegistered, "999723", 500, false},
	{"construction", CategoryConstructionViaECO, CategoryConstructionRegistered, "995457", 1800, true},
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
	// Families not under s.9(5) (beauty, and the B1 families other than
	// construction) must have no _VIA_ECO row: the platform never takes on a
	// liability the notification may not create.
	for _, c := range []Category{"BEAUTY_SALON_VIA_ECO", "CAR_CARE_VIA_ECO", "HOME_STAFFING_VIA_ECO", "RELOCATION_VIA_ECO",
		"PHOTOGRAPHY_VIA_ECO", "FITNESS_WELLNESS_VIA_ECO"} {
		if _, err := tab.Lookup(c, onDate); !errors.Is(err, ErrUnknownCategory) {
			t.Errorf("%s must not exist: %v", c, err)
		}
	}
}

// The B1 category names are the strings doorstep-service maps its families
// to; renaming one silently hides that family again.
func TestDoorstep_B1CategoryNames(t *testing.T) {
	for c, want := range map[Category]string{
		CategoryCarCareRegistered:         "CAR_CARE_REGISTERED",
		CategoryHomeStaffingRegistered:    "HOME_STAFFING_REGISTERED",
		CategoryRelocationRegistered:      "RELOCATION_REGISTERED",
		CategoryPhotographyRegistered:     "PHOTOGRAPHY_REGISTERED",
		CategoryFitnessWellnessRegistered: "FITNESS_WELLNESS_REGISTERED",
		CategoryConstructionViaECO:        "CONSTRUCTION_VIA_ECO",
		CategoryConstructionRegistered:    "CONSTRUCTION_REGISTERED",
	} {
		if string(c) != want {
			t.Errorf("category %q, want %q", c, want)
		}
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
		// B1 families at their seed prices; amount x 10000 = 11800 x taxable
		// + remainder (remainder < 11800, or < 10500 at 5%).
		// 399.00 (hatchback exterior wash) incl 18%: 11800 x 33813 = 398,993,400 r 6,600.
		{CategoryCarCareRegistered, 39900, 33813, 6087, 3043, 3044},
		// 8,999.00 (a monthly cook) incl 18%: 11800 x 762627 = 8,998,998,600 r 1,400.
		{CategoryHomeStaffingRegistered, 899900, 762627, 137273, 68636, 68637},
		// 14,999.00 (2 BHK shifting) incl 18%: 11800 x 1271101 = 14,998,991,800 r 8,200.
		{CategoryRelocationRegistered, 1499900, 1271101, 228799, 114399, 114400},
		// 2,999.00 (portrait session) incl 18%: 11800 x 254152 = 2,998,993,600 r 6,400.
		{CategoryPhotographyRegistered, 299900, 254152, 45748, 22874, 22874},
		// 699.00 (yoga hour) incl 5%: 10500 x 66571 = 698,995,500 r 4,500.
		{CategoryFitnessWellnessRegistered, 69900, 66571, 3329, 1664, 1665},
		// 3,192.00 (8 hours of masonry) incl 18%: 11800 x 270508 = 3,191,994,400 r 5,600.
		{CategoryConstructionViaECO, 319200, 270508, 48692, 24346, 24346},
		{CategoryConstructionRegistered, 319200, 270508, 48692, 24346, 24346},
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
		{"unregistered car washer", CategoryCarCareRegistered, func(in *Input) {}, ErrLiablePartyUnregistered},
		{"unregistered domestic worker", CategoryHomeStaffingRegistered, func(in *Input) {}, ErrLiablePartyUnregistered},
		{"unregistered mover", CategoryRelocationRegistered, func(in *Input) {}, ErrLiablePartyUnregistered},
		{"unregistered photographer", CategoryPhotographyRegistered, func(in *Input) {}, ErrLiablePartyUnregistered},
		{"unregistered yoga trainer", CategoryFitnessWellnessRegistered, func(in *Input) {}, ErrLiablePartyUnregistered},
		{"registered mason on the _VIA_ECO line", CategoryConstructionViaECO,
			func(in *Input) { in.ServiceProfessional = registeredPro() }, ErrUnsupportedSupply},
		{"B1 family before the seed date", CategoryConstructionViaECO,
			func(in *Input) { in.InvoiceDate = rateRationalisationDate.AddDate(0, 0, -1) }, ErrNoRateInEffect},
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
