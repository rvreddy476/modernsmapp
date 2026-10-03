package catalogue

import (
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

func priced(p int64) *Price { return &Price{ID: uuid.New(), PricePaise: p} }

type fixture struct {
	b                                   *ServiceBundle
	opt, optOff, optUnpriced            uuid.UUID
	pick1a, pick1b, opt2a, opt2b, opt2c uuid.UUID
	offAddon, unpricedAddon, offGroupA  uuid.UUID
	reqMin0A                            uuid.UUID
}

// bundle: option (max 3); "Pick one" required min1/max1 {a,b}; "Up to two"
// min0/max2 {a,b,c, inactive, unpriced}; an inactive group; a required group
// with min_select 0 (food: required => at least one); a required group
// whose only add-on is unpriced (not offered => imposes nothing).
func newFixture() fixture {
	f := fixture{opt: uuid.New(), optOff: uuid.New(), optUnpriced: uuid.New(),
		pick1a: uuid.New(), pick1b: uuid.New(), opt2a: uuid.New(), opt2b: uuid.New(), opt2c: uuid.New(),
		offAddon: uuid.New(), unpricedAddon: uuid.New(), offGroupA: uuid.New(), reqMin0A: uuid.New()}
	f.b = &ServiceBundle{
		City:     model.CityRef{Code: "HYD", Name: "Hyderabad"},
		Category: CategoryRow{ID: uuid.New(), Family: tax.FamilyHomeCleaning, Active: true},
		Service:  ServiceRow{ID: uuid.New(), Name: "Svc", Active: true},
		Options: []OptionRow{
			{ID: f.opt, Name: "Opt", DurationMinutes: 60, MaxQuantity: 3, Active: true, Price: priced(50000)},
			{ID: f.optOff, Name: "Off", DurationMinutes: 60, MaxQuantity: 1, Active: false, Price: priced(1)},
			{ID: f.optUnpriced, Name: "Unpriced", DurationMinutes: 60, MaxQuantity: 1, Active: true},
		},
		Groups: []GroupRow{
			{ID: uuid.New(), Name: "Pick one", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: true, Addons: []AddonRow{
				{ID: f.pick1a, Name: "A", ExtraDurationMinutes: 5, Active: true, Price: priced(1000)},
				{ID: f.pick1b, Name: "B", ExtraDurationMinutes: 5, Active: true, Price: priced(2000)},
			}},
			{ID: uuid.New(), Name: "Up to two", MinSelect: 0, MaxSelect: 2, Active: true, Addons: []AddonRow{
				{ID: f.opt2a, Name: "2a", ExtraDurationMinutes: 10, Active: true, Price: priced(300)},
				{ID: f.opt2b, Name: "2b", Active: true, Price: priced(400)},
				{ID: f.opt2c, Name: "2c", Active: true, Price: priced(500)},
				{ID: f.offAddon, Name: "off", Active: false, Price: priced(1)},
				{ID: f.unpricedAddon, Name: "unpriced", Active: true},
			}},
			{ID: uuid.New(), Name: "Inactive group", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: false, Addons: []AddonRow{
				{ID: f.offGroupA, Name: "x", Active: true, Price: priced(1)},
			}},
			{ID: uuid.New(), Name: "Required min0", MinSelect: 0, MaxSelect: 1, IsRequired: true, Active: true, Addons: []AddonRow{
				{ID: f.reqMin0A, Name: "r", Active: true, Price: priced(700)},
			}},
			{ID: uuid.New(), Name: "Nothing offered", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: true, Addons: []AddonRow{
				{ID: uuid.New(), Name: "no price", Active: true},
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
		{"required group empty (min_select 1)", Selection{f.opt, 1, []uuid.UUID{f.reqMin0A}}, apperr.CodeAddonInvalid},
		{"required group with min_select 0 still needs one", Selection{f.opt, 1, []uuid.UUID{f.pick1a}}, apperr.CodeAddonInvalid},
		{"pick-one group over max", Selection{f.opt, 1, append(ok, f.pick1b)}, apperr.CodeAddonInvalid},
		{"optional group over max", Selection{f.opt, 1, append(ok, f.opt2a, f.opt2b, f.opt2c)}, apperr.CodeAddonInvalid},
		{"duplicate add-on", Selection{f.opt, 1, append(ok, f.opt2a, f.opt2a)}, apperr.CodeAddonInvalid},
		{"foreign add-on", Selection{f.opt, 1, append(ok, uuid.New())}, apperr.CodeAddonInvalid},
		{"inactive add-on", Selection{f.opt, 1, append(ok, f.offAddon)}, apperr.CodeAddonInvalid},
		{"unpriced add-on", Selection{f.opt, 1, append(ok, f.unpricedAddon)}, apperr.CodeAddonInvalid},
		{"add-on of an inactive group", Selection{f.opt, 1, append(ok, f.offGroupA)}, apperr.CodeAddonInvalid},
		{"foreign option", Selection{uuid.New(), 1, ok}, apperr.CodeOptionInvalid},
		{"inactive option", Selection{f.optOff, 1, ok}, apperr.CodeOptionInvalid},
		{"unpriced option", Selection{f.optUnpriced, 1, ok}, apperr.CodeOptionInvalid},
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

func TestDetailViewOffersOnlySellableItems(t *testing.T) {
	f := newFixture()
	d, ok := DetailView(f.b)
	if !ok || len(d.Options) != 1 || d.Options[0].ID != f.opt {
		t.Fatalf("options %+v", d.Options)
	}
	names := []string{}
	for _, g := range d.AddonGroups {
		names = append(names, g.Name)
		for _, a := range g.Addons {
			if a.ID == f.offAddon || a.ID == f.unpricedAddon {
				t.Errorf("not-for-sale add-on offered: %s", a.Name)
			}
		}
	}
	if len(names) != 3 || names[0] != "Pick one" || names[1] != "Up to two" || names[2] != "Required min0" {
		t.Fatalf("groups %v (inactive and nothing-offered groups must be hidden)", names)
	}
	f.b.Options = f.b.Options[1:] // only inactive/unpriced left
	if _, ok := DetailView(f.b); ok {
		t.Fatal("a service with no sellable option must be unavailable")
	}
}

type flatTax struct{}

func (flatTax) SplitInclusive(in tax.Input) (tax.Result, error) {
	out := tax.Result{Provisional: true, Note: "n"}
	for _, l := range in.Lines {
		tb, tx := tax.ExtractInclusive(l.GrossPaise, 1800)
		out.Lines = append(out.Lines, tax.LineResult{Ref: l.Ref, Category: "C", SAC: "S", RateBPS: 1800, TaxablePaise: tb, TaxPaise: tx, GrossPaise: l.GrossPaise})
	}
	return out, nil
}

func TestBuildQuote(t *testing.T) {
	f := newFixture()
	now := time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC)
	q, err := BuildQuote(f.b, Selection{f.opt, 2, []uuid.UUID{f.opt2a, f.pick1b, f.reqMin0A}},
		QuoteInput{QuoteID: uuid.New(), ZoneID: uuid.New(), PlaceOfSupplyState: "36", Now: now, TTL: 15 * time.Minute}, flatTax{})
	if err != nil {
		t.Fatal(err)
	}
	// Lines: option x2, then add-ons in display order (pick1b, opt2a, reqMin0A).
	if len(q.Lines) != 4 || q.Lines[0].LineTotalPaise != 100000 || q.Lines[1].RefID != f.pick1b || q.Lines[2].RefID != f.opt2a {
		t.Fatalf("lines %+v", q.Lines)
	}
	if q.TotalPaise != 100000+2000+300+700 || q.TaxablePaise+q.TaxPaise != q.TotalPaise {
		t.Fatalf("totals %d %d %d", q.TotalPaise, q.TaxablePaise, q.TaxPaise)
	}
	if q.DurationMinutes != 60*2+5+10 || !q.ExpiresAt.Equal(now.Add(15*time.Minute)) || q.CityCode != "HYD" || !q.PricesIncludeTax {
		t.Fatalf("quote %+v", q)
	}
	for _, l := range q.Lines {
		if l.PriceID == uuid.Nil {
			t.Fatalf("line without the price row it used: %+v", l)
		}
	}
}
