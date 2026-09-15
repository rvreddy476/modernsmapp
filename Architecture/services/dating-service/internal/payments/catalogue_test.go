package payments

import "testing"

func TestPremiumCatalogue_Prices(t *testing.T) {
	want := map[string]struct {
		kind   string
		amount int64
		days   int
	}{
		ProductPass30d:  {KindPass, 39900, 30},
		ProductPass90d:  {KindPass, 99900, 90},
		ProductPass365d: {KindPass, 249900, 365},
		ProductBoost:    {KindBoost, 4900, 0},
	}
	got := Catalogue()
	if len(got) != len(want) {
		t.Fatalf("catalogue has %d products, want %d", len(got), len(want))
	}
	for _, p := range got {
		w, ok := want[p.ID]
		if !ok || p.Kind != w.kind || p.AmountMinor != w.amount || p.DurationDays != w.days || p.Currency != "INR" {
			t.Fatalf("product %+v, want %+v", p, w)
		}
		if p.Kind == KindPass && len(p.Features) != len(PassFeatures) {
			t.Fatalf("%s features = %v", p.ID, p.Features)
		}
	}
	if _, ok := LookupProduct("monthly_399"); ok {
		t.Fatalf("a retired plan id resolved")
	}
	// Catalogue returns copies: mutating one never changes a price.
	got[0].AmountMinor = 1
	if p, _ := LookupProduct(got[0].ID); p.AmountMinor == 1 {
		t.Fatalf("catalogue is mutable through Catalogue()")
	}
}
