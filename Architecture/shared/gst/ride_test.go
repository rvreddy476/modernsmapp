package gst

// Mopedu ride fares: passenger transport by motorcycle / auto / cab supplied
// by the driver through the platform (s.9(5), 5% without ITC, SAC 996412),
// plus the platform's own convenience fee (CategoryPlatformFee, 18%).

import (
	"errors"
	"testing"
)

func rideInput() Input {
	return Input{
		Mode:               ModeExclusive,
		InvoiceDate:        onDate,
		ThroughECO:         true,
		Platform:           Party{GSTIN: gstinFor("29", 'C')},
		Driver:             Party{StateCode: "29"},
		PlaceOfSupplyState: "29",
	}
}

func TestRide_RateRows(t *testing.T) {
	tab := DefaultRateTable()
	cases := []struct {
		name     string
		category Category
		supplier SupplierRole
		eco      bool
		rate     RateBP
		itc      bool
		sac      string
	}{
		{"passenger transport via ECO", CategoryPassengerTransportViaECO, SupplierDriver, true, 500, false, "996412"},
		{"platform convenience fee", CategoryPlatformFee, SupplierPlatform, false, 1800, true, "998599"},
	}
	for _, c := range cases {
		row, err := tab.Lookup(c.category, onDate)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if row.Supplier != c.supplier || row.ECOSection95 != c.eco || row.RateBP != c.rate || row.ITCAvailable != c.itc || row.SAC != c.sac {
			t.Errorf("%s = %+v", c.name, row)
		}
		if !row.NeedsAdviserConfirmation || !row.EffectiveFrom.Equal(rateRationalisationDate) {
			t.Errorf("%s: adviser flag %v, effective %v", c.name, row.NeedsAdviserConfirmation, row.EffectiveFrom)
		}
	}
}

func TestRide_DriverRowGuards(t *testing.T) {
	// A driver row is a valid supplier; a driver row that is NOT under s.9(5)
	// is accepted by the table (the guard is at Compute time).
	r := validRow()
	r.Category = CategoryPassengerTransportViaECO
	r.Supplier = SupplierDriver
	r.SAC = "996412"
	if _, err := NewRateTable([]RateRow{r}); err != nil {
		t.Fatalf("driver row refused: %v", err)
	}
}

func TestRide_FareAndFeeLines(t *testing.T) {
	cases := []struct {
		name       string
		mode       Mode
		fare, fee  Paise
		wantFareTx Paise
		wantFeeTx  Paise
		wantTotal  Paise
	}{
		// 5% of 120.00 = 6.00; 18% of 20.00 = 3.60.
		{"exclusive", ModeExclusive, 12000, 2000, 600, 360, 14960},
		// 120.00 incl 5%: taxable floors to 114.28, tax 5.72; 20.00 incl 18%:
		// taxable floors to 16.94, tax 3.06 (extract in money.go).
		{"inclusive", ModeInclusive, 12000, 2000, 572, 306, 14000},
	}
	for _, c := range cases {
		in := rideInput()
		in.Mode = c.mode
		in.Lines = []Line{
			{Ref: "fare", Category: CategoryPassengerTransportViaECO, Amount: c.fare},
			{Ref: "fee", Category: CategoryPlatformFee, Amount: c.fee},
		}
		res := mustCompute(t, in)
		fare, fee := res.Lines[0], res.Lines[1]
		if fare.Tax != c.wantFareTx || fee.Tax != c.wantFeeTx || res.Total != c.wantTotal {
			t.Errorf("%s: fare tax %d fee tax %d total %d, want %d %d %d", c.name, fare.Tax, fee.Tax, res.Total, c.wantFareTx, c.wantFeeTx, c.wantTotal)
		}
		if fare.Liability != LiabilityECOSection95 || fare.LiableParty != SupplierPlatform || fare.LiablePartyGSTIN != gstinFor("29", 'C') ||
			fare.Supplier != SupplierDriver || fare.ECOCollectsTCS || fare.ITCAvailable || fare.SAC != "996412" {
			t.Errorf("%s: fare line %+v", c.name, fare)
		}
		if fee.Liability != LiabilitySupplier || fee.LiableParty != SupplierPlatform || fee.ECOCollectsTCS || fee.SAC != "998599" {
			t.Errorf("%s: fee line %+v", c.name, fee)
		}
		if fare.Interstate || fare.CGST+fare.SGST != fare.Tax || fare.IGST != 0 {
			t.Errorf("%s: intrastate split %+v", c.name, fare)
		}
		if !res.NeedsAdviserConfirmation {
			t.Errorf("%s: ride result not flagged for adviser confirmation", c.name)
		}
		// Both lines land on the platform, under two liabilities.
		if len(res.ByLiableParty) != 2 {
			t.Errorf("%s: by liable party = %+v", c.name, res.ByLiableParty)
		}
	}
}

func TestRide_InterstateUsesIGST(t *testing.T) {
	in := rideInput()
	in.PlaceOfSupplyState = "27"
	in.Lines = []Line{{Ref: "fare", Category: CategoryPassengerTransportViaECO, Amount: 10000}}
	l := mustCompute(t, in).Lines[0]
	if !l.Interstate || l.IGST != 500 || l.CGST != 0 || l.SGST != 0 {
		t.Fatalf("interstate fare %+v", l)
	}
}

func TestRide_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *Input)
		want   error
	}{
		{"driver outside the ECO is not modelled", func(in *Input) { in.ThroughECO = false }, ErrUnsupportedSupply},
		{"platform without GSTIN under 9(5)", func(in *Input) { in.Platform = Party{StateCode: "29"} }, ErrLiablePartyUnregistered},
		{"driver state code invalid", func(in *Input) { in.Driver = Party{StateCode: "99"} }, ErrPartyState},
		{"fare before the seed date", func(in *Input) { in.InvoiceDate = rateRationalisationDate.AddDate(0, 0, -1) }, ErrNoRateInEffect},
	}
	for _, c := range cases {
		in := rideInput()
		in.Lines = []Line{{Ref: "fare", Category: CategoryPassengerTransportViaECO, Amount: 10000}}
		c.mutate(&in)
		if _, err := Compute(DefaultRateTable(), in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	// The driver needs no GSTIN under 9(5): the platform is liable.
	in := rideInput()
	in.Driver = Party{}
	in.Lines = []Line{{Ref: "fare", Category: CategoryPassengerTransportViaECO, Amount: 10000}}
	if l := mustCompute(t, in).Lines[0]; l.LiableParty != SupplierPlatform {
		t.Fatalf("unregistered driver: %+v", l)
	}
}
