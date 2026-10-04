package catalogue

import (
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

func suggestedPrice(p int64) *Price { return &Price{ID: uuid.New(), PricePaise: p} }

type fixture struct {
	b                                   *ServiceBundle
	opt, optOff, optNoSuggestion        uuid.UUID
	pick1a, pick1b, opt2a, opt2b, opt2c uuid.UUID
	offAddon, noSuggestionAddon         uuid.UUID
	offGroupA, reqMin0A                 uuid.UUID
}

// bundle: option (max 3, per hour); an inactive option; an option with no
// city suggestion (still on the menu: prices come from professionals);
// "Pick one" required min1/max1 {a,b}; "Up to two" min0/max2 {a,b,c,
// inactive, no suggestion}; an inactive group; a required group with
// min_select 0 (food: required => at least one); a required group whose
// only add-on is inactive (not offered => imposes nothing).
func newFixture() fixture {
	f := fixture{opt: uuid.New(), optOff: uuid.New(), optNoSuggestion: uuid.New(),
		pick1a: uuid.New(), pick1b: uuid.New(), opt2a: uuid.New(), opt2b: uuid.New(), opt2c: uuid.New(),
		offAddon: uuid.New(), noSuggestionAddon: uuid.New(), offGroupA: uuid.New(), reqMin0A: uuid.New()}
	from := int64(45000)
	f.b = &ServiceBundle{
		City:     model.CityRef{Code: "HYD", Name: "Hyderabad"},
		Category: CategoryRow{ID: uuid.New(), Family: tax.FamilyHomeCleaning, Active: true},
		Service:  ServiceRow{ID: uuid.New(), Name: "Svc", Active: true},
		Options: []OptionRow{
			{ID: f.opt, Name: "Opt", DurationMinutes: 60, MaxQuantity: 3, Unit: UnitPerHour, Active: true, Price: suggestedPrice(50000), FromPaise: &from},
			{ID: f.optOff, Name: "Off", DurationMinutes: 60, MaxQuantity: 1, Active: false, Price: suggestedPrice(1)},
			{ID: f.optNoSuggestion, Name: "No suggestion", DurationMinutes: 60, MaxQuantity: 1, Active: true},
		},
		Groups: []GroupRow{
			{ID: uuid.New(), Name: "Pick one", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: true, Addons: []AddonRow{
				{ID: f.pick1a, Name: "A", ExtraDurationMinutes: 5, Active: true, Price: suggestedPrice(1000)},
				{ID: f.pick1b, Name: "B", ExtraDurationMinutes: 5, Active: true, Price: suggestedPrice(2000)},
			}},
			{ID: uuid.New(), Name: "Up to two", MinSelect: 0, MaxSelect: 2, Active: true, Addons: []AddonRow{
				{ID: f.opt2a, Name: "2a", ExtraDurationMinutes: 10, Active: true, Price: suggestedPrice(300)},
				{ID: f.opt2b, Name: "2b", Active: true, Price: suggestedPrice(400)},
				{ID: f.opt2c, Name: "2c", Active: true, Price: suggestedPrice(500)},
				{ID: f.offAddon, Name: "off", Active: false, Price: suggestedPrice(1)},
				{ID: f.noSuggestionAddon, Name: "no suggestion", Active: true},
			}},
			{ID: uuid.New(), Name: "Inactive group", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: false, Addons: []AddonRow{
				{ID: f.offGroupA, Name: "x", Active: true, Price: suggestedPrice(1)},
			}},
			{ID: uuid.New(), Name: "Required min0", MinSelect: 0, MaxSelect: 1, IsRequired: true, Active: true, Addons: []AddonRow{
				{ID: f.reqMin0A, Name: "r", Active: true, Price: suggestedPrice(700)},
			}},
			{ID: uuid.New(), Name: "Nothing offered", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: true, Addons: []AddonRow{
				{ID: uuid.New(), Name: "inactive", Active: false},
			}},
		},
	}
	return f
}

func code(err *apperr.Error) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func TestValidateSelection(t *testing.T) {
	f := newFixture()
	ok := []uuid.UUID{f.pick1a, f.reqMin0A}
	for _, c := range []struct {
		name string
		sel  Selection
		want string
	}{
		{"valid minimum", Selection{f.opt, 1, ok}, ""},
		{"valid with optional group at max", Selection{f.opt, 3, append(ok, f.opt2a, f.opt2b)}, ""},
		{"no suggested price is still on the menu", Selection{f.optNoSuggestion, 1, append(ok, f.noSuggestionAddon)}, ""},
		{"required group empty (min_select 1)", Selection{f.opt, 1, []uuid.UUID{f.reqMin0A}}, apperr.CodeAddonInvalid},
		{"required group with min_select 0 still needs one", Selection{f.opt, 1, []uuid.UUID{f.pick1a}}, apperr.CodeAddonInvalid},
		{"pick-one group over max", Selection{f.opt, 1, append(ok, f.pick1b)}, apperr.CodeAddonInvalid},
		{"optional group over max", Selection{f.opt, 1, append(ok, f.opt2a, f.opt2b, f.opt2c)}, apperr.CodeAddonInvalid},
		{"duplicate add-on", Selection{f.opt, 1, append(ok, f.opt2a, f.opt2a)}, apperr.CodeAddonInvalid},
		{"foreign add-on", Selection{f.opt, 1, append(ok, uuid.New())}, apperr.CodeAddonInvalid},
		{"inactive add-on", Selection{f.opt, 1, append(ok, f.offAddon)}, apperr.CodeAddonInvalid},
		{"add-on of an inactive group", Selection{f.opt, 1, append(ok, f.offGroupA)}, apperr.CodeAddonInvalid},
		{"foreign option", Selection{uuid.New(), 1, ok}, apperr.CodeOptionInvalid},
		{"inactive option", Selection{f.optOff, 1, ok}, apperr.CodeOptionInvalid},
		{"quantity zero", Selection{f.opt, 0, ok}, apperr.CodeQuantityInvalid},
		{"quantity over max", Selection{f.opt, 4, ok}, apperr.CodeQuantityInvalid},
	} {
		_, _, err := ValidateSelection(f.b, c.sel)
		if code(err) != c.want {
			t.Errorf("%s: got %q (%v), want %q", c.name, code(err), err, c.want)
		}
	}
}

// The group rule's details name the group and its bounds (contract).
func TestAddonGroupErrorDetails(t *testing.T) {
	f := newFixture()
	_, _, err := ValidateSelection(f.b, Selection{f.opt, 1, []uuid.UUID{f.pick1a, f.pick1b, f.reqMin0A}})
	if err == nil || err.Details["min_select"] != 1 || err.Details["max_select"] != 1 || err.Details["selected"] != 2 {
		t.Fatalf("details %+v", err)
	}
}

func TestDetailViewIsTheMenu(t *testing.T) {
	f := newFixture()
	d, ok := DetailView(f.b)
	if !ok || len(d.Options) != 2 || d.Options[0].ID != f.opt || d.Options[1].ID != f.optNoSuggestion {
		t.Fatalf("options %+v", d.Options)
	}
	o := d.Options[0]
	if o.Unit != UnitPerHour || o.SuggestedPricePaise == nil || *o.SuggestedPricePaise != 50000 || o.FromPricePaise == nil || *o.FromPricePaise != 45000 {
		t.Fatalf("option prices %+v", o)
	}
	if d.Options[1].SuggestedPricePaise != nil || d.Options[1].FromPricePaise != nil || d.Options[1].Unit != UnitPerJob {
		t.Fatalf("an option nobody prices yet: %+v", d.Options[1])
	}
	names := []string{}
	for _, g := range d.AddonGroups {
		names = append(names, g.Name)
		for _, a := range g.Addons {
			if a.ID == f.offAddon {
				t.Errorf("inactive add-on offered: %s", a.Name)
			}
		}
	}
	if len(names) != 3 || names[0] != "Pick one" || names[1] != "Up to two" || names[2] != "Required min0" {
		t.Fatalf("groups %v (inactive and nothing-offered groups must be hidden)", names)
	}
	f.b.Options = f.b.Options[1:2] // only the inactive one left
	if _, ok := DetailView(f.b); ok {
		t.Fatal("a service with no active option must be unavailable")
	}
}

type flatTax struct{}

func (flatTax) SplitInclusive(in tax.Input) (tax.Result, error) {
	if !tax.Supported(in.Family) {
		return tax.Result{}, tax.ErrUnknownFamily
	}
	out := tax.Result{Provisional: true, Note: "n"}
	for _, l := range in.Lines {
		tb, tx := tax.ExtractInclusive(l.GrossPaise, 1800)
		out.Lines = append(out.Lines, tax.LineResult{Ref: l.Ref, Category: "C", SAC: "S", RateBPS: 1800, TaxablePaise: tb, TaxPaise: tx, GrossPaise: l.GrossPaise})
	}
	return out, nil
}

func proPrices(f fixture) ProPrices {
	p := ProPrices{}
	for id, paise := range map[uuid.UUID]int64{f.opt: 40000, f.pick1b: 1500, f.opt2a: 250, f.reqMin0A: 600} {
		p[id] = ItemPrice{ID: uuid.New(), PricePaise: paise, Unit: UnitPerJob}
	}
	p[f.opt] = ItemPrice{ID: p[f.opt].ID, PricePaise: 40000, Unit: UnitPerHour}
	return p
}

// A quote prices with the professional's approved prices only: the city's
// suggested prices (50000 for the option here) are never charged.
func TestBuildQuoteUsesTheProfessionalsPrices(t *testing.T) {
	f := newFixture()
	now := time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC)
	prices := proPrices(f)
	pro := uuid.New()
	q, err := BuildQuote(f.b, Selection{f.opt, 2, []uuid.UUID{f.opt2a, f.pick1b, f.reqMin0A}}, prices,
		QuoteInput{QuoteID: uuid.New(), ProID: pro, ZoneID: uuid.New(), PlaceOfSupplyState: "36", Now: now, TTL: 15 * time.Minute}, flatTax{})
	if err != nil {
		t.Fatal(err)
	}
	// Lines: option x2, then add-ons in display order (pick1b, opt2a, reqMin0A).
	if len(q.Lines) != 4 || q.Lines[0].LineTotalPaise != 80000 || q.Lines[0].Unit != UnitPerHour || q.Lines[1].RefID != f.pick1b ||
		q.Lines[2].RefID != f.opt2a {
		t.Fatalf("lines %+v", q.Lines)
	}
	if q.TotalPaise != 80000+1500+250+600 || q.TaxablePaise+q.TaxPaise != q.TotalPaise || q.ProID != pro {
		t.Fatalf("totals %d %d %d pro %s", q.TotalPaise, q.TaxablePaise, q.TaxPaise, q.ProID)
	}
	if q.DurationMinutes != 60*2+5+10 || !q.ExpiresAt.Equal(now.Add(15*time.Minute)) || q.CityCode != "HYD" || !q.PricesIncludeTax {
		t.Fatalf("quote %+v", q)
	}
	for _, l := range q.Lines {
		if l.PriceID != prices[l.RefID].ID {
			t.Fatalf("line not priced from the professional's row: %+v", l)
		}
	}
}

// An item the professional has no approved price for is refused: no city
// price fills the gap.
func TestBuildQuoteRefusesAnUnpricedItem(t *testing.T) {
	f := newFixture()
	prices := proPrices(f)
	delete(prices, f.pick1b)
	_, err := BuildQuote(f.b, Selection{f.opt, 1, []uuid.UUID{f.pick1b, f.reqMin0A}}, prices,
		QuoteInput{QuoteID: uuid.New(), ProID: uuid.New(), PlaceOfSupplyState: "36", Now: time.Now(), TTL: time.Minute}, flatTax{})
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Code != apperr.CodePriceUnavailable {
		t.Fatalf("err %v", err)
	}
	if ids := ae.Details["item_ids"].([]string); len(ids) != 1 || ids[0] != f.pick1b.String() {
		t.Fatalf("details %+v", ae.Details)
	}
	if _, err := BuildQuote(f.b, Selection{f.opt, 1, []uuid.UUID{f.pick1a, f.reqMin0A}}, nil,
		QuoteInput{QuoteID: uuid.New(), PlaceOfSupplyState: "36", Now: time.Now(), TTL: time.Minute}, flatTax{}); err == nil {
		t.Fatal("a professional with no prices quoted")
	}
}

// A B1 family the tax lane has not mapped is not visible and not priced.
func TestUnmappedFamilyIsNotOffered(t *testing.T) {
	f := newFixture()
	f.b.Category.Family = tax.FamilyHomeStaffing
	if f.b.Visible() {
		t.Fatal("an unmapped family is visible")
	}
	opt, addons, verr := ValidateSelection(f.b, Selection{f.opt, 1, []uuid.UUID{f.pick1b, f.reqMin0A}})
	if verr != nil {
		t.Fatal(verr)
	}
	_, err := PriceSelection(f.b, opt, addons, 1, proPrices(f), "36", time.Now(), flatTax{})
	if ae, ok := err.(*apperr.Error); !ok || ae.Code != apperr.CodeServiceNotAvailable || ae.Details["reason"] != "tax_category_pending" {
		t.Fatalf("err %v", err)
	}
}
