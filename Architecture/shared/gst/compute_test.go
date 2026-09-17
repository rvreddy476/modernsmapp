package gst

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/kyc"
)

// refCheck is an independent GSTIN mod-36 implementation (right to left,
// weight starting at 2) used only to build synthetic GSTINs for tests.
func refCheck(first14 string) byte {
	const alpha = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	w, sum := 2, 0
	for i := 13; i >= 0; i-- {
		d := w * strings.IndexByte(alpha, first14[i])
		sum += d/36 + d%36
		w = 3 - w
	}
	return alpha[(36-sum%36)%36]
}

// gstinFor builds a synthetic GSTIN (serial-0000 PAN) for a state and holder.
func gstinFor(state string, holder byte) string {
	base := state + "ZZZ" + string(holder) + "Z0000Z1Z"
	return base + string(refCheck(base))
}

var onDate = time.Date(2026, 9, 13, 12, 0, 0, 0, ist)

func baseInput() Input {
	return Input{
		Mode:               ModeExclusive,
		InvoiceDate:        onDate,
		Restaurant:         Party{GSTIN: gstinFor("29", 'P')},
		Platform:           Party{GSTIN: gstinFor("29", 'C')},
		PlaceOfSupplyState: "29",
	}
}

func mustCompute(t *testing.T, in Input) *Result {
	t.Helper()
	res, err := Compute(DefaultRateTable(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return res
}

func TestSyntheticGSTINsAreValid(t *testing.T) {
	if gstinFor("29", 'P') != "29ZZZPZ0000Z1Z6" {
		t.Fatalf("reference checksum drifted: %s", gstinFor("29", 'P'))
	}
	for _, s := range []string{"29", "27", "07", "33"} {
		for _, h := range []byte{'P', 'C'} {
			if _, err := kyc.ValidateGSTIN(gstinFor(s, h)); err != nil {
				t.Fatalf("%s%c: %v", s, h, err)
			}
		}
	}
}

func TestCompute_ExclusiveGolden(t *testing.T) {
	in := baseInput()
	in.Lines = []Line{{Ref: "food", Category: CategoryRestaurantStandalone, Amount: 100000}}
	l := mustCompute(t, in).Lines[0]
	if l.Tax != 5000 || l.CGST != 2500 || l.SGST != 2500 || l.IGST != 0 || l.Gross != 105000 || l.Taxable != 100000 {
		t.Fatalf("%+v", l)
	}
}

func TestCompute_ExclusiveHalfUp(t *testing.T) {
	for amount, want := range map[Paise]Paise{10: 1, 9: 0} {
		in := baseInput()
		in.Lines = []Line{{Ref: "x", Category: CategoryRestaurantStandalone, Amount: amount}}
		if l := mustCompute(t, in).Lines[0]; l.Tax != want || l.Gross != amount+want {
			t.Errorf("amount %d: tax %d gross %d, want tax %d", amount, l.Tax, l.Gross, want)
		}
	}
}

func TestCompute_InclusiveGolden(t *testing.T) {
	in := baseInput()
	in.Mode = ModeInclusive
	in.Lines = []Line{{Ref: "fee", Category: CategoryPlatformFee, Amount: 99}}
	l := mustCompute(t, in).Lines[0]
	if l.Taxable != 83 || l.Tax != 16 || l.Gross != 99 || l.CGST != 8 || l.SGST != 8 {
		t.Fatalf("%+v", l)
	}
}

func TestCompute_IntraVsInterstate(t *testing.T) {
	in := baseInput()
	in.Lines = []Line{{Ref: "fee", Category: CategoryPlatformFee, Amount: 50}} // tax 9, odd
	intra := mustCompute(t, in)
	l := intra.Lines[0]
	if l.Interstate || l.Tax != 9 || l.CGST != 4 || l.SGST != 5 || l.IGST != 0 {
		t.Fatalf("intrastate: %+v", l)
	}
	in.PlaceOfSupplyState = "27"
	l = mustCompute(t, in).Lines[0]
	if !l.Interstate || l.Tax != 9 || l.IGST != 9 || l.CGST != 0 || l.SGST != 0 || l.SupplierState != "29" || l.PlaceOfSupplyState != "27" {
		t.Fatalf("interstate: %+v", l)
	}
	// Per-line override.
	in.PlaceOfSupplyState = "29"
	in.Lines[0].PlaceOfSupplyState = "33"
	if l := mustCompute(t, in).Lines[0]; !l.Interstate || l.IGST != 9 {
		t.Fatalf("override: %+v", l)
	}
}

func TestCompute_GSTINStateBeatsConflictingStateCode(t *testing.T) {
	in := baseInput()
	in.Restaurant.StateCode = "27" // GSTIN says 29
	in.Lines = []Line{{Ref: "f", Category: CategoryRestaurantSpecifiedPremises, Amount: 1000}}
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrPartyState) {
		t.Fatalf("err = %v, want ErrPartyState", err)
	}
	in.Restaurant.StateCode = "29"
	mustCompute(t, in)
	in.Restaurant.StateCode = "99"
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrPartyState) {
		t.Fatalf("unassigned state code: %v", err)
	}
}

func TestCompute_Section95(t *testing.T) {
	in := baseInput()
	in.ThroughECO = true
	in.Lines = []Line{{Ref: "f", Category: CategoryRestaurantStandalone, Amount: 1000}}
	l := mustCompute(t, in).Lines[0]
	if l.Liability != LiabilityECOSection95 || l.LiableParty != SupplierPlatform || l.LiablePartyGSTIN != gstinFor("29", 'C') ||
		l.Supplier != SupplierRestaurant || l.ECOCollectsTCS {
		t.Fatalf("standalone via ECO: %+v", l)
	}

	in.ThroughECO = false
	l = mustCompute(t, in).Lines[0]
	if l.Liability != LiabilitySupplier || l.LiableParty != SupplierRestaurant || l.LiablePartyGSTIN != gstinFor("29", 'P') || l.ECOCollectsTCS {
		t.Fatalf("standalone direct: %+v", l)
	}

	in.ThroughECO = true
	in.Lines[0].Category = CategoryRestaurantSpecifiedPremises
	l = mustCompute(t, in).Lines[0]
	if l.Liability != LiabilitySupplier || l.LiableParty != SupplierRestaurant || !l.ECOCollectsTCS || l.RateBP != 1800 {
		t.Fatalf("specified premises via ECO: %+v", l)
	}

	for _, eco := range []bool{true, false} {
		in.ThroughECO = eco
		in.Lines[0].Category = CategoryPlatformFee
		l = mustCompute(t, in).Lines[0]
		if l.Liability != LiabilitySupplier || l.LiableParty != SupplierPlatform || l.ECOCollectsTCS {
			t.Fatalf("platform fee eco=%v: %+v", eco, l)
		}
	}

	in.ThroughECO = true
	in.Lines[0].Category = CategoryDeliveryFeePartnerViaECO
	l = mustCompute(t, in).Lines[0]
	if l.Liability != LiabilityECOSection95 || l.LiableParty != SupplierPlatform || l.Supplier != SupplierDeliveryPartner {
		t.Fatalf("partner delivery via ECO: %+v", l)
	}
}

func TestCompute_RegistrationRequirements(t *testing.T) {
	// Unregistered restaurant under 9(5) is fine: the platform is liable.
	in := baseInput()
	in.ThroughECO = true
	in.Restaurant = Party{StateCode: "29"}
	in.Lines = []Line{{Ref: "f", Category: CategoryRestaurantStandalone, Amount: 1000}}
	mustCompute(t, in)

	// The same restaurant supplying directly is liable and has no GSTIN.
	in.ThroughECO = false
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrLiablePartyUnregistered) {
		t.Fatalf("unregistered supplier-liable restaurant: %v", err)
	}

	// Under 9(5) the platform's GSTIN is required.
	in = baseInput()
	in.ThroughECO = true
	in.Platform = Party{StateCode: "29"}
	in.Lines = []Line{{Ref: "f", Category: CategoryRestaurantStandalone, Amount: 1000}}
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrLiablePartyUnregistered) {
		t.Fatalf("missing platform GSTIN under 9(5): %v", err)
	}
}

func TestCompute_DeliveryPartnerOutsideECOUnsupported(t *testing.T) {
	in := baseInput()
	in.Lines = []Line{{Ref: "d", Category: CategoryDeliveryFeePartnerViaECO, Amount: 3000}}
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrUnsupportedSupply) {
		t.Fatalf("err = %v", err)
	}
}

func TestCompute_ZeroRate(t *testing.T) {
	r := validRow()
	r.RateBP = 0
	if _, err := NewRateTable([]RateRow{r}); !errors.Is(err, ErrZeroRateNotExplicit) {
		t.Fatalf("silent zero: %v", err)
	}
	r.ExplicitZeroRate = true
	tab, err := NewRateTable([]RateRow{r})
	if err != nil {
		t.Fatal(err)
	}
	in := baseInput()
	in.Mode = ModeInclusive
	in.InvoiceDate = onDate
	in.Lines = []Line{{Ref: "z", Category: CategoryRestaurantStandalone, Amount: 12345}}
	res, err := Compute(tab, in)
	if err != nil {
		t.Fatal(err)
	}
	if l := res.Lines[0]; l.Tax != 0 || l.Taxable != 12345 || l.Gross != 12345 || res.Total != 12345 {
		t.Fatalf("%+v", l)
	}
	if res.NeedsAdviserConfirmation {
		t.Fatal("custom unflagged row set NeedsAdviserConfirmation")
	}
}

func TestCompute_NeedsAdviserConfirmation(t *testing.T) {
	in := baseInput()
	in.Lines = []Line{{Ref: "f", Category: CategoryRestaurantStandalone, Amount: 1000}}
	if !mustCompute(t, in).NeedsAdviserConfirmation {
		t.Fatal("default table result not flagged")
	}

	unflagged := validRow()
	flagged := validRow()
	flagged.Category = CategoryPlatformFee
	flagged.Supplier = SupplierPlatform
	flagged.ECOSection95 = false
	flagged.NeedsAdviserConfirmation = true
	tab, err := NewRateTable([]RateRow{unflagged, flagged})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compute(tab, in)
	if err != nil || res.NeedsAdviserConfirmation || res.Lines[0].NeedsAdviserConfirmation {
		t.Fatalf("only unflagged rows: %+v, %v", res, err)
	}
	in.Lines = append(in.Lines, Line{Ref: "p", Category: CategoryPlatformFee, Amount: 100})
	res, err = Compute(tab, in)
	if err != nil || !res.NeedsAdviserConfirmation || !res.Lines[1].NeedsAdviserConfirmation {
		t.Fatalf("one flagged row: %+v, %v", res, err)
	}
}

func TestCompute_EffectiveDateAndCategory(t *testing.T) {
	tab := effectiveDatedTable(t)
	in := baseInput()
	in.Lines = []Line{{Ref: "f", Category: CategoryRestaurantStandalone, Amount: 10000}}
	for on, want := range map[time.Time]Paise{
		time.Date(2026, 9, 30, 23, 0, 0, 0, ist):       500,
		time.Date(2026, 10, 1, 0, 0, 0, 0, ist):        1800,
		time.Date(2026, 9, 30, 18, 30, 0, 0, time.UTC): 1800,
	} {
		in.InvoiceDate = on
		res, err := Compute(tab, in)
		if err != nil {
			t.Errorf("%v: %v", on, err)
			continue
		}
		if res.Lines[0].RateBP != RateBP(want) {
			t.Errorf("%v: rate %d want %d", on, res.Lines[0].RateBP, want)
		}
	}
	in.InvoiceDate = time.Date(2023, 6, 1, 0, 0, 0, 0, ist)
	if _, err := Compute(tab, in); !errors.Is(err, ErrNoRateInEffect) {
		t.Errorf("before all rows: %v", err)
	}
	in.InvoiceDate = onDate
	in.Lines[0].Category = "SOMETHING_ELSE"
	if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("unknown category: %v", err)
	}
}

func TestCompute_Refusals(t *testing.T) {
	good := baseInput()
	good.Lines = []Line{{Ref: "f", Category: CategoryRestaurantStandalone, Amount: 1000}}
	cases := []struct {
		name string
		mut  func(in *Input)
		want error
	}{
		{"no lines", func(in *Input) { in.Lines = nil }, ErrNoLines},
		{"bad mode", func(in *Input) { in.Mode = "" }, ErrInvalidMode},
		{"negative amount", func(in *Input) { in.Lines[0].Amount = -1 }, ErrNegativeAmount},
		{"negative discount", func(in *Input) { in.RestaurantDiscount = -1 }, ErrNegativeAmount},
		{"amount too large", func(in *Input) { in.Lines[0].Amount = MaxAmount + 1 }, ErrAmountOutOfRange},
		{"PoS missing", func(in *Input) { in.PlaceOfSupplyState = "" }, ErrInvalidPlaceOfSupply},
		{"PoS unassigned", func(in *Input) { in.PlaceOfSupplyState = "00" }, ErrInvalidPlaceOfSupply},
		{"line PoS unassigned", func(in *Input) { in.Lines[0].PlaceOfSupplyState = "39" }, ErrInvalidPlaceOfSupply},
		{"discount exceeds restaurant subtotal", func(in *Input) {
			in.Lines = append(in.Lines, Line{Ref: "p", Category: CategoryPlatformFee, Amount: 5000})
			in.RestaurantDiscount = 1001
		}, ErrDiscountExceedsSubtotal},
		{"invalid GSTIN", func(in *Input) { in.Platform.GSTIN = "29ZZZCZ0000Z1Z0" }, kyc.ErrInvalidGSTIN},
	}
	for _, c := range cases {
		in := good
		in.Lines = append([]Line(nil), good.Lines...)
		c.mut(&in)
		_, err := Compute(DefaultRateTable(), in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "ZZZ") {
			t.Errorf("%s: error echoes a GSTIN: %q", c.name, err.Error())
		}
	}
	if _, err := Compute(nil, good); !errors.Is(err, ErrNoRateTable) {
		t.Errorf("nil table: %v", err)
	}
}

func TestCompute_DiscountOnlyOnRestaurantLines(t *testing.T) {
	in := baseInput()
	in.RestaurantDiscount = 100
	in.Lines = []Line{
		{Ref: "a", Category: CategoryRestaurantStandalone, Amount: 1000},
		{Ref: "fee", Category: CategoryPlatformFee, Amount: 1000},
		{Ref: "b", Category: CategoryRestaurantStandalone, Amount: 3000},
	}
	res := mustCompute(t, in)
	if res.Lines[0].AllocatedDiscount != 25 || res.Lines[1].AllocatedDiscount != 0 || res.Lines[2].AllocatedDiscount != 75 {
		t.Fatalf("%+v", res.Lines)
	}
	// Restaurant group taxable 3900 at 5% = 195; fee 1000 at 18% = 180.
	if res.TotalTax != 375 || res.Total != 3900+195+1000+180 {
		t.Fatalf("tax %d total %d", res.TotalTax, res.Total)
	}
	if len(res.ByLiableParty) != 2 || res.ByLiableParty[0].LiableParty != SupplierRestaurant || res.ByLiableParty[1].LiableParty != SupplierPlatform {
		t.Fatalf("party order: %+v", res.ByLiableParty)
	}
}

// ─── Property test ───────────────────────────────────────────────────
//
// Every check below is recomputed from the Input and the independent
// reference arithmetic here, NOT from the package's own invariant asserts,
// so disabling those asserts does not blind this test.

func refGroupTax(mode Mode, net int64, rate int64) int64 {
	if mode == ModeInclusive {
		if rate == 0 {
			return 0
		}
		return net - net*10000/(10000+rate)
	}
	return (net*rate + 5000) / 10000
}

func TestProperty_RandomBasketsAreExactToThePaise(t *testing.T) {
	iterations := 20000
	if testing.Short() {
		iterations = 2000
	}
	rng := rand.New(rand.NewSource(20260913))
	tab := DefaultRateTable()
	states := []string{"29", "27", "07", "33"}
	cats := []Category{
		CategoryRestaurantStandalone, CategoryRestaurantSpecifiedPremises, CategoryCloudKitchenTakeaway,
		CategoryOutdoorCatering, CategoryOutdoorCateringSpecifiedPremises, CategoryPlatformFee,
		CategoryDeliveryFeePlatform, CategoryDeliveryFeePartnerViaECO, CategoryPassengerTransportViaECO,
	}

	for iter := 0; iter < iterations; iter++ {
		in := Input{
			InvoiceDate:        onDate,
			ThroughECO:         rng.Intn(2) == 0,
			Restaurant:         Party{GSTIN: gstinFor(states[rng.Intn(len(states))], 'F')},
			Platform:           Party{GSTIN: gstinFor(states[rng.Intn(len(states))], 'C')},
			Driver:             Party{StateCode: states[rng.Intn(len(states))]},
			PlaceOfSupplyState: states[rng.Intn(len(states))],
		}
		if rng.Intn(2) == 0 {
			in.Mode = ModeInclusive
		} else {
			in.Mode = ModeExclusive
		}
		n := 1 + rng.Intn(8)
		var restSum Paise
		for i := 0; i < n; i++ {
			c := cats[rng.Intn(len(cats))]
			if (c == CategoryDeliveryFeePartnerViaECO || c == CategoryPassengerTransportViaECO) && !in.ThroughECO {
				c = CategoryDeliveryFeePlatform
			}
			amt := Paise(rng.Int63n(500_000))
			if rng.Intn(15) == 0 {
				amt = 0
			}
			l := Line{Ref: fmt.Sprintf("l%d", i), Category: c, Amount: amt}
			if rng.Intn(6) == 0 {
				l.PlaceOfSupplyState = states[rng.Intn(len(states))]
			}
			in.Lines = append(in.Lines, l)
			row, _ := tab.Lookup(c, onDate)
			if row.Supplier == SupplierRestaurant {
				restSum += amt
			}
		}
		if restSum > 0 {
			switch rng.Intn(10) {
			case 0:
				in.RestaurantDiscount = restSum
			case 1, 2, 3:
				in.RestaurantDiscount = 0
			default:
				in.RestaurantDiscount = Paise(rng.Int63n(int64(restSum) + 1))
			}
		}

		res, err := Compute(tab, in)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		checkBasket(t, iter, tab, in, res)
	}
}

func checkBasket(t *testing.T, iter int, tab *RateTable, in Input, res *Result) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("iter %d: "+format, append([]any{iter}, args...)...)
	}
	if len(res.Lines) != len(in.Lines) {
		fail("line count")
	}
	type gk struct {
		liable    SupplierRole
		liability Liability
		supplier  SupplierRole
		rate      RateBP
		sac       string
		pos       string
	}
	groupNet := map[gk]int64{}
	groupTax := map[gk]int64{}
	type pk struct {
		role      SupplierRole
		liability Liability
	}
	party := map[pk]PartyTotals{}

	var sumAmt, sumDisc, sumGross, sumTaxable, sumTax, sumC, sumS, sumI Paise
	for i, l := range res.Lines {
		src := in.Lines[i]
		row, _ := tab.Lookup(src.Category, in.InvoiceDate)
		wantLiability := LiabilitySupplier
		if in.ThroughECO && row.ECOSection95 {
			wantLiability = LiabilityECOSection95
		}
		wantLiable, wantGSTIN := row.Supplier, in.Restaurant.GSTIN
		if wantLiability == LiabilityECOSection95 || row.Supplier == SupplierPlatform {
			wantLiable, wantGSTIN = SupplierPlatform, in.Platform.GSTIN
		}
		wantPos := in.PlaceOfSupplyState
		if src.PlaceOfSupplyState != "" {
			wantPos = src.PlaceOfSupplyState
		}
		if l.Liability != wantLiability || l.LiableParty != wantLiable || l.LiablePartyGSTIN != wantGSTIN ||
			l.RateBP != row.RateBP || l.SAC != row.SAC || l.PlaceOfSupplyState != wantPos ||
			l.Interstate != (wantGSTIN[:2] != wantPos) || !l.NeedsAdviserConfirmation {
			fail("line %d classification %+v", i, l)
		}
		if l.ECOCollectsTCS != (in.ThroughECO && wantLiability == LiabilitySupplier && row.Supplier != SupplierPlatform) {
			fail("line %d TCS marker", i)
		}
		if l.Amount != src.Amount || (row.Supplier != SupplierRestaurant && l.AllocatedDiscount != 0) {
			fail("line %d discount on a non-restaurant line", i)
		}
		net := l.Amount - l.AllocatedDiscount
		if net < 0 || l.Taxable < 0 || l.Tax < 0 || l.CGST < 0 || l.SGST < 0 || l.IGST < 0 || l.Tax > l.Gross {
			fail("line %d negative component or tax > gross: %+v", i, l)
		}
		if l.Taxable+l.Tax != l.Gross {
			fail("line %d taxable+tax != gross", i)
		}
		if l.CGST+l.SGST+l.IGST != l.Tax {
			fail("line %d components != tax", i)
		}
		if l.Interstate {
			if l.CGST != 0 || l.SGST != 0 || l.IGST != l.Tax {
				fail("line %d interstate split", i)
			}
		} else if l.IGST != 0 || l.CGST != l.Tax/2 || l.SGST != l.Tax-l.Tax/2 {
			fail("line %d intrastate split", i)
		}
		if (in.Mode == ModeInclusive && l.Gross != net) || (in.Mode == ModeExclusive && l.Taxable != net) {
			fail("line %d net mismatch", i)
		}
		k := gk{l.LiableParty, l.Liability, l.Supplier, l.RateBP, l.SAC, l.PlaceOfSupplyState}
		groupNet[k] += int64(net)
		groupTax[k] += int64(l.Tax)

		p := party[pk{l.LiableParty, l.Liability}]
		p.LiableParty, p.Liability, p.GSTIN = l.LiableParty, l.Liability, l.LiablePartyGSTIN
		p.Taxable += l.Taxable
		p.Tax += l.Tax
		p.CGST += l.CGST
		p.SGST += l.SGST
		p.IGST += l.IGST
		p.Gross += l.Gross
		party[pk{l.LiableParty, l.Liability}] = p

		sumAmt += l.Amount
		sumDisc += l.AllocatedDiscount
		sumGross += l.Gross
		sumTaxable += l.Taxable
		sumTax += l.Tax
		sumC += l.CGST
		sumS += l.SGST
		sumI += l.IGST
	}
	// Group tax is rounded once, and each line's share is the largest-
	// remainder share of it.
	for k, gn := range groupNet {
		want := refGroupTax(in.Mode, gn, int64(k.rate))
		if groupTax[k] != want {
			fail("group %+v tax %d, want %d", k, groupTax[k], want)
		}
		for _, l := range res.Lines {
			if (gk{l.LiableParty, l.Liability, l.Supplier, l.RateBP, l.SAC, l.PlaceOfSupplyState}) != k || gn == 0 {
				continue
			}
			num := want * int64(l.Amount-l.AllocatedDiscount)
			q, r := num/gn, num%gn
			if (r == 0 && int64(l.Tax) != q) || (r != 0 && int64(l.Tax) != q && int64(l.Tax) != q+1) {
				fail("line %s tax %d is not a largest-remainder share (q=%d r=%d)", l.Ref, l.Tax, q, r)
			}
		}
	}
	if sumDisc != in.RestaurantDiscount {
		fail("allocated discount %d != %d", sumDisc, in.RestaurantDiscount)
	}
	if res.Total != sumGross || res.TotalTaxable != sumTaxable || res.TotalTax != sumTax ||
		res.TotalCGST != sumC || res.TotalSGST != sumS || res.TotalIGST != sumI {
		fail("totals != sum of lines")
	}
	wantTotal := sumAmt - in.RestaurantDiscount
	if in.Mode == ModeExclusive {
		wantTotal += sumTax
	}
	if res.Total != wantTotal {
		fail("total %d != expected %d", res.Total, wantTotal)
	}
	if len(res.ByLiableParty) != len(party) {
		fail("party count")
	}
	for _, pt := range res.ByLiableParty {
		if party[pk{pt.LiableParty, pt.Liability}] != pt {
			fail("party totals %+v != %+v", pt, party[pk{pt.LiableParty, pt.Liability}])
		}
	}
	if !res.NeedsAdviserConfirmation {
		fail("result not flagged")
	}
}
