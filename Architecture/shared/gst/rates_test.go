package gst

import (
	"errors"
	"testing"
	"time"
)

func validRow() RateRow {
	return RateRow{
		Category:      CategoryRestaurantStandalone,
		Supplier:      SupplierRestaurant,
		ECOSection95:  true,
		RateBP:        500,
		SAC:           "996331",
		EffectiveFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, ist),
	}
}

func TestNewRateTable_Guards(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(r *RateRow)
		want   error
	}{
		{"empty category", func(r *RateRow) { r.Category = " " }, ErrInvalidRateRow},
		{"unknown supplier", func(r *RateRow) { r.Supplier = "CUSTOMER" }, ErrInvalidRateRow},
		{"rate above 10000", func(r *RateRow) { r.RateBP = 10001 }, ErrInvalidRateRow},
		{"negative rate", func(r *RateRow) { r.RateBP = -1 }, ErrInvalidRateRow},
		{"zero rate not explicit", func(r *RateRow) { r.RateBP = 0 }, ErrZeroRateNotExplicit},
		{"explicit zero on non-zero", func(r *RateRow) { r.ExplicitZeroRate = true }, ErrInvalidRateRow},
		{"SAC five digits", func(r *RateRow) { r.SAC = "99633" }, ErrInvalidRateRow},
		{"SAC letter", func(r *RateRow) { r.SAC = "99633A" }, ErrInvalidRateRow},
		{"SAC seven digits", func(r *RateRow) { r.SAC = "9963310" }, ErrInvalidRateRow},
		{"zero EffectiveFrom", func(r *RateRow) { r.EffectiveFrom = time.Time{} }, ErrInvalidRateRow},
		{"platform with 9(5)", func(r *RateRow) { r.Supplier = SupplierPlatform; r.ECOSection95 = true }, ErrInvalidRateRow},
	}
	for _, c := range cases {
		r := validRow()
		c.mutate(&r)
		if _, err := NewRateTable([]RateRow{r}); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := NewRateTable([]RateRow{validRow()}); err != nil {
		t.Fatalf("valid row refused: %v", err)
	}
	if _, err := NewRateTable(nil); !errors.Is(err, ErrInvalidRateRow) {
		t.Errorf("empty table: %v", err)
	}
	// Explicit zero is allowed.
	z := validRow()
	z.RateBP, z.ExplicitZeroRate = 0, true
	if _, err := NewRateTable([]RateRow{z}); err != nil {
		t.Errorf("explicit zero refused: %v", err)
	}
}

func TestNewRateTable_DuplicateISTDate(t *testing.T) {
	a := validRow()
	b := validRow()
	// 2023-12-31T20:00Z is 2024-01-01 01:30 IST: the same IST date as a.
	b.EffectiveFrom = time.Date(2023, 12, 31, 20, 0, 0, 0, time.UTC)
	b.RateBP = 1800
	if _, err := NewRateTable([]RateRow{a, b}); !errors.Is(err, ErrInvalidRateRow) {
		t.Fatalf("duplicate IST date: %v", err)
	}
	c := validRow()
	c.Category = CategoryCloudKitchenTakeaway
	if _, err := NewRateTable([]RateRow{a, c}); err != nil {
		t.Fatalf("same date, different category refused: %v", err)
	}
}

func effectiveDatedTable(t *testing.T) *RateTable {
	t.Helper()
	old := validRow()
	newer := validRow()
	newer.RateBP = 1800
	newer.EffectiveFrom = time.Date(2026, 10, 1, 0, 0, 0, 0, ist)
	tab, err := NewRateTable([]RateRow{newer, old}) // deliberately unsorted
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestLookup_EffectiveDateInIST(t *testing.T) {
	tab := effectiveDatedTable(t)
	cases := []struct {
		name string
		on   time.Time
		want RateBP
	}{
		{"day before, IST noon", time.Date(2026, 9, 30, 12, 0, 0, 0, ist), 500},
		{"day before, IST 23:59", time.Date(2026, 9, 30, 23, 59, 59, 0, ist), 500},
		{"effective day, IST midnight", time.Date(2026, 10, 1, 0, 0, 0, 0, ist), 1800},
		{"effective day as UTC 18:30 the evening before", time.Date(2026, 9, 30, 18, 30, 0, 0, time.UTC), 1800},
		{"UTC 18:29 the evening before", time.Date(2026, 9, 30, 18, 29, 59, 0, time.UTC), 500},
		{"much later", time.Date(2030, 1, 1, 0, 0, 0, 0, ist), 1800},
		{"first row's own day", time.Date(2024, 1, 1, 0, 0, 0, 0, ist), 500},
	}
	for _, c := range cases {
		row, err := tab.Lookup(CategoryRestaurantStandalone, c.on)
		if err != nil || row.RateBP != c.want {
			t.Errorf("%s: %d, %v; want %d", c.name, row.RateBP, err, c.want)
		}
	}
	if _, err := tab.Lookup(CategoryRestaurantStandalone, time.Date(2023, 12, 31, 23, 59, 0, 0, ist)); !errors.Is(err, ErrNoRateInEffect) {
		t.Errorf("before all rows: %v", err)
	}
	if _, err := tab.Lookup(CategoryPlatformFee, time.Date(2026, 1, 1, 0, 0, 0, 0, ist)); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("unknown category: %v", err)
	}
}

func TestDefaultRateTable_Seed(t *testing.T) {
	type want struct {
		supplier SupplierRole
		eco      bool
		rate     RateBP
		itc      bool
		sac      string
	}
	expected := map[Category]want{
		CategoryRestaurantStandalone:             {SupplierRestaurant, true, 500, false, "996331"},
		CategoryRestaurantSpecifiedPremises:      {SupplierRestaurant, false, 1800, true, "996331"},
		CategoryCloudKitchenTakeaway:             {SupplierRestaurant, true, 500, false, "996331"},
		CategoryOutdoorCatering:                  {SupplierRestaurant, false, 500, false, "996334"},
		CategoryOutdoorCateringSpecifiedPremises: {SupplierRestaurant, false, 1800, true, "996334"},
		CategoryPlatformFee:                      {SupplierPlatform, false, 1800, true, "998599"},
		CategoryDeliveryFeePlatform:              {SupplierPlatform, false, 1800, true, "996813"},
		CategoryDeliveryFeePartnerViaECO:         {SupplierDeliveryPartner, true, 1800, false, "996813"},
	}
	tab := DefaultRateTable()
	rows := tab.Rows()
	if len(rows) != len(expected) {
		t.Fatalf("%d rows, want %d", len(rows), len(expected))
	}
	start := time.Date(2025, 9, 22, 0, 0, 0, 0, ist)
	for _, r := range rows {
		w, ok := expected[r.Category]
		if !ok {
			t.Errorf("unexpected category %s", r.Category)
			continue
		}
		if !r.NeedsAdviserConfirmation {
			t.Errorf("%s is not flagged for adviser confirmation", r.Category)
		}
		if r.Supplier != w.supplier || r.ECOSection95 != w.eco || r.RateBP != w.rate || r.ITCAvailable != w.itc || r.SAC != w.sac {
			t.Errorf("%s = %+v", r.Category, r)
		}
		if !r.EffectiveFrom.Equal(start) || r.Note == "" {
			t.Errorf("%s: effective %v note %q", r.Category, r.EffectiveFrom, r.Note)
		}
	}
	if _, err := tab.Lookup(CategoryRestaurantStandalone, start.Add(-time.Minute)); !errors.Is(err, ErrNoRateInEffect) {
		t.Errorf("invoice before the seed date: %v", err)
	}
	// Rows returns a copy.
	rows[0].RateBP = 1
	rows[0].NeedsAdviserConfirmation = false
	if again := tab.Rows(); again[0].RateBP == 1 || !again[0].NeedsAdviserConfirmation {
		t.Error("Rows() exposed internal state")
	}
}
