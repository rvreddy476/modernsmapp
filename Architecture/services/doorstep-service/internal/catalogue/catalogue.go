// Package catalogue is Doorstep's pure catalogue logic: which options and
// add-ons a customer may see and pick in a city, and how a selection is
// validated and priced into a quote. No I/O: the store loads a ServiceBundle,
// these functions decide.
//
// One rule feeds both the service page and the quote, so they cannot drift:
// an option is offered when active and priced in the city; an add-on when
// active and priced; an add-on group when active and offering at least one
// add-on. Only offered groups impose their min/max on a quote.
package catalogue

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

// Price is the current city price row of an option or add-on.
type Price struct {
	ID         uuid.UUID
	PricePaise int64
	MRPPaise   *int64
}

// ServiceRow is a service as stored.
type ServiceRow struct {
	ID              uuid.UUID
	Slug            string
	Name            string
	Description     string
	DurationMinutes int
	Inclusions      []string
	Exclusions      []string
	ImageURL        *string
	CrewSize        int
	ReworkDays      int
	MinBeforePhotos int
	MinAfterPhotos  int
	Active          bool
}

// CategoryRow is the service's category as stored.
type CategoryRow struct {
	ID           uuid.UUID
	Slug         string
	Name         string
	Family       string
	GenderRule   string
	ExtrasPolicy string
	Active       bool
}

// OptionRow is an option with its current price (nil when unpriced).
type OptionRow struct {
	ID              uuid.UUID
	Name            string
	Description     string
	DurationMinutes int
	MaxQuantity     int
	IsDefault       bool
	Active          bool
	Price           *Price
}

// AddonRow is an add-on with its current price.
type AddonRow struct {
	ID                   uuid.UUID
	Name                 string
	Description          string
	ExtraDurationMinutes int
	Active               bool
	Price                *Price
}

// GroupRow is an add-on group with its add-ons.
type GroupRow struct {
	ID         uuid.UUID
	Name       string
	MinSelect  int
	MaxSelect  int
	IsRequired bool
	Active     bool
	Addons     []AddonRow
}

// ServiceBundle is everything about one service in one city at one instant,
// options and groups in display order (inactive and unpriced rows included).
type ServiceBundle struct {
	City     model.CityRef
	Category CategoryRow
	Service  ServiceRow
	Options  []OptionRow
	Groups   []GroupRow
}

// Visible reports whether the service may be shown at all.
func (b *ServiceBundle) Visible() bool { return b.Service.Active && b.Category.Active }

func (o OptionRow) offered() bool { return o.Active && o.Price != nil }
func (a AddonRow) offered() bool  { return a.Active && a.Price != nil }

func (g GroupRow) offeredAddons() []AddonRow {
	if !g.Active {
		return nil
	}
	var out []AddonRow
	for _, a := range g.Addons {
		if a.offered() {
			out = append(out, a)
		}
	}
	return out
}

// MinRequired is the food add-on rule: max(min_select, is_required ? 1 : 0).
func (g GroupRow) MinRequired() int {
	if g.IsRequired && g.MinSelect < 1 {
		return 1
	}
	return g.MinSelect
}

// DetailView is the customer's service page, or ok=false when no option is
// offered in the city (DOORSTEP_SERVICE_NOT_AVAILABLE).
func DetailView(b *ServiceBundle) (model.ServiceDetail, bool) {
	d := model.ServiceDetail{
		ID: b.Service.ID,
		Category: model.CategoryBrief{
			ID: b.Category.ID, Slug: b.Category.Slug, Name: b.Category.Name,
			Family: b.Category.Family, GenderRule: b.Category.GenderRule, ExtrasPolicy: b.Category.ExtrasPolicy,
		},
		Slug: b.Service.Slug, Name: b.Service.Name, Description: b.Service.Description,
		DurationMinutes: b.Service.DurationMinutes,
		Inclusions:      nonNil(b.Service.Inclusions), Exclusions: nonNil(b.Service.Exclusions),
		ImageURL: b.Service.ImageURL, CrewSize: b.Service.CrewSize, ReworkDays: b.Service.ReworkDays,
		MinBeforePhotos: b.Service.MinBeforePhotos, MinAfterPhotos: b.Service.MinAfterPhotos,
		Options: []model.ServiceOption{}, AddonGroups: []model.AddonGroup{},
	}
	for _, o := range b.Options {
		if !o.offered() {
			continue
		}
		d.Options = append(d.Options, model.ServiceOption{
			ID: o.ID, Name: o.Name, Description: o.Description, DurationMinutes: o.DurationMinutes,
			MaxQuantity: o.MaxQuantity, IsDefault: o.IsDefault, PricePaise: o.Price.PricePaise, MRPPaise: o.Price.MRPPaise,
		})
	}
	for _, g := range b.Groups {
		addons := g.offeredAddons()
		if len(addons) == 0 {
			continue
		}
		mg := model.AddonGroup{ID: g.ID, Name: g.Name, MinSelect: g.MinSelect, MaxSelect: g.MaxSelect, IsRequired: g.IsRequired, Addons: []model.Addon{}}
		for _, a := range addons {
			mg.Addons = append(mg.Addons, model.Addon{
				ID: a.ID, Name: a.Name, Description: a.Description, ExtraDurationMinutes: a.ExtraDurationMinutes, PricePaise: a.Price.PricePaise,
			})
		}
		d.AddonGroups = append(d.AddonGroups, mg)
	}
	return d, len(d.Options) > 0
}

// Selection is a validated-shape quote request.
type Selection struct {
	OptionID uuid.UUID
	Quantity int
	AddonIDs []uuid.UUID
}

// QuoteInput is everything BuildQuote needs besides the bundle.
type QuoteInput struct {
	QuoteID            uuid.UUID
	ZoneID             uuid.UUID
	PlaceOfSupplyState string
	Now                time.Time
	TTL                time.Duration
}

func optionInvalid(msg string) *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeOptionInvalid, msg)
}

func addonInvalid(msg string, details map[string]any) *apperr.Error {
	e := apperr.New(http.StatusUnprocessableEntity, apperr.CodeAddonInvalid, msg)
	e.Details = details
	return e
}

// ValidateSelection applies the option, quantity and add-on group rules and
// returns the chosen option and add-ons in display order.
func ValidateSelection(b *ServiceBundle, sel Selection) (OptionRow, []AddonRow, *apperr.Error) {
	var opt *OptionRow
	for i := range b.Options {
		if b.Options[i].ID == sel.OptionID {
			opt = &b.Options[i]
			break
		}
	}
	if opt == nil {
		return OptionRow{}, nil, optionInvalid("the option does not belong to this service")
	}
	if !opt.Active {
		return OptionRow{}, nil, optionInvalid("the option is not available")
	}
	if opt.Price == nil {
		return OptionRow{}, nil, optionInvalid("the option has no price in this city")
	}
	if sel.Quantity < 1 || sel.Quantity > opt.MaxQuantity {
		return OptionRow{}, nil, apperr.New(http.StatusUnprocessableEntity, apperr.CodeQuantityInvalid,
			"quantity is outside the allowed range").WithDetails(map[string]any{"min": 1, "max": opt.MaxQuantity})
	}

	// Index every add-on of this service by id, with its group.
	type found struct {
		addon AddonRow
		group *GroupRow
	}
	index := map[uuid.UUID]found{}
	for gi := range b.Groups {
		g := &b.Groups[gi]
		for _, a := range g.Addons {
			index[a.ID] = found{addon: a, group: g}
		}
	}
	seen := map[uuid.UUID]bool{}
	perGroup := map[uuid.UUID]int{}
	for _, id := range sel.AddonIDs {
		if seen[id] {
			return OptionRow{}, nil, addonInvalid("an add-on is listed twice", map[string]any{"addon_id": id.String()})
		}
		seen[id] = true
		f, ok := index[id]
		if !ok {
			return OptionRow{}, nil, addonInvalid("the add-on does not belong to this service", map[string]any{"addon_id": id.String()})
		}
		if !f.group.Active || !f.addon.offered() {
			return OptionRow{}, nil, addonInvalid("the add-on is not available", map[string]any{"addon_id": id.String()})
		}
		perGroup[f.group.ID]++
	}
	for _, g := range b.Groups {
		if len(g.offeredAddons()) == 0 {
			continue // not offered: imposes nothing (and nothing in it can be picked)
		}
		n := perGroup[g.ID]
		if min := g.MinRequired(); n < min {
			return OptionRow{}, nil, addonInvalid("\""+g.Name+"\" needs more choices", map[string]any{
				"group_id": g.ID.String(), "min_select": min, "max_select": g.MaxSelect, "selected": n,
			})
		}
		if n > g.MaxSelect {
			return OptionRow{}, nil, addonInvalid("\""+g.Name+"\" allows fewer choices", map[string]any{
				"group_id": g.ID.String(), "min_select": g.MinRequired(), "max_select": g.MaxSelect, "selected": n,
			})
		}
	}

	var addons []AddonRow
	for _, g := range b.Groups {
		for _, a := range g.Addons {
			if seen[a.ID] {
				addons = append(addons, a)
			}
		}
	}
	return *opt, addons, nil
}

// BuildQuote validates sel against b and prices it with tc. Prices are the
// city price rows current at in.Now and are GST-inclusive.
func BuildQuote(b *ServiceBundle, sel Selection, in QuoteInput, tc tax.Computer) (*model.Quote, error) {
	opt, addons, verr := ValidateSelection(b, sel)
	if verr != nil {
		return nil, verr
	}
	lines := []model.QuoteLine{{
		Kind: "option", RefID: opt.ID, PriceID: opt.Price.ID, Name: b.Service.Name + " - " + opt.Name,
		Quantity: sel.Quantity, UnitPricePaise: opt.Price.PricePaise, LineTotalPaise: opt.Price.PricePaise * int64(sel.Quantity),
	}}
	duration := opt.DurationMinutes * sel.Quantity
	for _, a := range addons {
		lines = append(lines, model.QuoteLine{
			Kind: "addon", RefID: a.ID, PriceID: a.Price.ID, Name: a.Name,
			Quantity: 1, UnitPricePaise: a.Price.PricePaise, LineTotalPaise: a.Price.PricePaise,
		})
		duration += a.ExtraDurationMinutes
	}
	taxIn := tax.Input{Family: b.Category.Family, PlaceOfSupplyState: in.PlaceOfSupplyState, At: in.Now}
	for i, l := range lines {
		taxIn.Lines = append(taxIn.Lines, tax.Line{Ref: lineRef(i), GrossPaise: l.LineTotalPaise})
	}
	split, err := tc.SplitInclusive(taxIn)
	if err != nil {
		return nil, err
	}
	if len(split.Lines) != len(lines) {
		return nil, apperr.Internal()
	}
	q := &model.Quote{
		ID: in.QuoteID, Status: model.QuoteOpen, ServiceID: b.Service.ID, OptionID: opt.ID, Quantity: sel.Quantity,
		CityCode: b.City.Code, ZoneID: in.ZoneID, PricesIncludeTax: true, TaxProvisional: split.Provisional,
		TaxNote: split.Note, DurationMinutes: duration,
		ExpiresAt: in.Now.Add(in.TTL).UTC(), CreatedAt: in.Now.UTC(),
	}
	for i := range lines {
		s := split.Lines[i]
		if s.GrossPaise != lines[i].LineTotalPaise || s.TaxablePaise+s.TaxPaise != s.GrossPaise {
			return nil, apperr.Internal()
		}
		lines[i].TaxablePaise, lines[i].TaxPaise = s.TaxablePaise, s.TaxPaise
		lines[i].TaxRateBPS, lines[i].GSTCategory, lines[i].SAC = s.RateBPS, s.Category, s.SAC
		q.TotalPaise += lines[i].LineTotalPaise
		q.TaxablePaise += s.TaxablePaise
		q.TaxPaise += s.TaxPaise
	}
	q.Lines = lines
	return q, nil
}

func lineRef(i int) string { return "line-" + strconv.Itoa(i+1) }

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ZoneHit is the zone (and its city) covering a point.
type ZoneHit struct {
	Zone      model.ZoneRef
	City      model.CityRef
	StateCode string
}
