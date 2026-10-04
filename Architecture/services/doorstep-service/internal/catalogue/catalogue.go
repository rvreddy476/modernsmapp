// Package catalogue is Doorstep's pure catalogue logic: which options and
// add-ons a customer may see and pick in a city, and how a selection is
// validated and priced. No I/O: the store loads a ServiceBundle and the
// professional's prices, these functions decide.
//
// The catalogue is the menu (B1, 4 Oct 2026): every bookable price is a
// professional's own approved row (doorstep.pro_service_prices). A city
// price is an optional "suggested" price shown beside the menu, never
// charged. One rule feeds both the service page and the quote, so they
// cannot drift: an option is offered when active; an add-on when active and
// in an active group; an add-on group when active and offering at least one
// add-on. Only offered groups impose their min/max on a selection.
package catalogue

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

// Units (doorstep.service_options.unit, migration 005). Add-ons are per job.
const (
	UnitPerJob   = "per_job"
	UnitPerHour  = "per_hour"
	UnitPerMonth = "per_month"
)

// Price is a city's suggested price row of an option or add-on (optional).
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
	RequiredSkill   string
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

// OptionRow is an option. Price is the city's suggested price (nil when
// none); FromPaise is the lowest approved professional price in the city
// now (nil when no professional offers it yet).
type OptionRow struct {
	ID              uuid.UUID
	Name            string
	Description     string
	DurationMinutes int
	MaxQuantity     int
	Unit            string
	IsDefault       bool
	Active          bool
	Price           *Price
	FromPaise       *int64
}

// AddonRow is an add-on (Price and FromPaise as on OptionRow).
type AddonRow struct {
	ID                   uuid.UUID
	Name                 string
	Description          string
	ExtraDurationMinutes int
	Active               bool
	Price                *Price
	FromPaise            *int64
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
// options and groups in display order (inactive rows included).
type ServiceBundle struct {
	City     model.CityRef
	Category CategoryRow
	Service  ServiceRow
	Options  []OptionRow
	Groups   []GroupRow
}

// Visible reports whether the service may be shown at all: active, in an
// active category, and of a family the tax computer can price (B1 families
// wait for the tax lane).
func (b *ServiceBundle) Visible() bool {
	return b.Service.Active && b.Category.Active && tax.Supported(b.Category.Family)
}

func (o OptionRow) offered() bool { return o.Active }

func (g GroupRow) offeredAddons() []AddonRow {
	if !g.Active {
		return nil
	}
	var out []AddonRow
	for _, a := range g.Addons {
		if a.Active {
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

func suggested(p *Price) (*int64, *int64) {
	if p == nil {
		return nil, nil
	}
	v := p.PricePaise
	return &v, p.MRPPaise
}

func unitOr(u string) string {
	if u == "" {
		return UnitPerJob
	}
	return u
}

// DetailView is the customer's service page, or ok=false when no option is
// offered (DOORSTEP_SERVICE_NOT_AVAILABLE).
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
		sp, mrp := suggested(o.Price)
		d.Options = append(d.Options, model.ServiceOption{
			ID: o.ID, Name: o.Name, Description: o.Description, DurationMinutes: o.DurationMinutes,
			MaxQuantity: o.MaxQuantity, Unit: unitOr(o.Unit), IsDefault: o.IsDefault,
			SuggestedPricePaise: sp, MRPPaise: mrp, FromPricePaise: o.FromPaise,
		})
	}
	for _, g := range b.Groups {
		addons := g.offeredAddons()
		if len(addons) == 0 {
			continue
		}
		mg := model.AddonGroup{ID: g.ID, Name: g.Name, MinSelect: g.MinSelect, MaxSelect: g.MaxSelect, IsRequired: g.IsRequired, Addons: []model.Addon{}}
		for _, a := range addons {
			sp, _ := suggested(a.Price)
			mg.Addons = append(mg.Addons, model.Addon{
				ID: a.ID, Name: a.Name, Description: a.Description, ExtraDurationMinutes: a.ExtraDurationMinutes,
				SuggestedPricePaise: sp, FromPricePaise: a.FromPaise,
			})
		}
		d.AddonGroups = append(d.AddonGroups, mg)
	}
	return d, len(d.Options) > 0
}

// Selection is a validated-shape selection.
type Selection struct {
	OptionID uuid.UUID
	Quantity int
	AddonIDs []uuid.UUID
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
	if !opt.offered() {
		return OptionRow{}, nil, optionInvalid("the option is not available")
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
		if !f.group.Active || !f.addon.Active {
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

// ItemPrice is one approved, live professional price row.
type ItemPrice struct {
	ID         uuid.UUID
	PricePaise int64
	Unit       string
}

// ProPrices are one professional's live approved prices by option/add-on id.
type ProPrices map[uuid.UUID]ItemPrice

// ItemIDs lists the items a selection needs a price for: the option, then
// every add-on.
func (sel Selection) ItemIDs() []uuid.UUID {
	return append([]uuid.UUID{sel.OptionID}, sel.AddonIDs...)
}

// Priced is a selection priced with one professional's prices: GST-inclusive
// lines split by the tax computer.
type Priced struct {
	Lines           []model.QuoteLine
	TotalPaise      int64
	TaxablePaise    int64
	TaxPaise        int64
	Provisional     bool
	Note            string
	DurationMinutes int
}

// PriceUnavailable is 422 DOORSTEP_PRICE_UNAVAILABLE: the professional has no
// approved price for an item of the selection.
func PriceUnavailable(missing []uuid.UUID) *apperr.Error {
	ids := make([]string, 0, len(missing))
	for _, m := range missing {
		ids = append(ids, m.String())
	}
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodePriceUnavailable,
		"this professional has no approved price for part of this selection").WithDetails(map[string]any{"item_ids": ids})
}

// TaxPending is 422 DOORSTEP_SERVICE_NOT_AVAILABLE for a family the tax
// computer cannot price yet.
func TaxPending() *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeServiceNotAvailable, "this service is not available yet").
		WithDetails(map[string]any{"reason": "tax_category_pending"})
}

// PriceSelection prices a validated selection with one professional's
// prices (only approved, live rows are ever passed in) and splits the tax.
// Every item must be priced: no city price ever fills a gap.
func PriceSelection(b *ServiceBundle, opt OptionRow, addons []AddonRow, quantity int, prices ProPrices, placeOfSupply string,
	at time.Time, tc tax.Computer) (*Priced, error) {
	var missing []uuid.UUID
	op, ok := prices[opt.ID]
	if !ok {
		missing = append(missing, opt.ID)
	}
	for _, a := range addons {
		if _, ok := prices[a.ID]; !ok {
			missing = append(missing, a.ID)
		}
	}
	if len(missing) > 0 {
		return nil, PriceUnavailable(missing)
	}
	lines := []model.QuoteLine{{
		Kind: "option", RefID: opt.ID, PriceID: op.ID, Name: b.Service.Name + " - " + opt.Name, Unit: unitOr(opt.Unit),
		Quantity: quantity, UnitPricePaise: op.PricePaise, LineTotalPaise: op.PricePaise * int64(quantity),
	}}
	duration := opt.DurationMinutes * quantity
	for _, a := range addons {
		ap := prices[a.ID]
		lines = append(lines, model.QuoteLine{
			Kind: "addon", RefID: a.ID, PriceID: ap.ID, Name: a.Name, Unit: UnitPerJob,
			Quantity: 1, UnitPricePaise: ap.PricePaise, LineTotalPaise: ap.PricePaise,
		})
		duration += a.ExtraDurationMinutes
	}
	taxIn := tax.Input{Family: b.Category.Family, PlaceOfSupplyState: placeOfSupply, At: at}
	for i, l := range lines {
		taxIn.Lines = append(taxIn.Lines, tax.Line{Ref: lineRef(i), GrossPaise: l.LineTotalPaise})
	}
	split, err := tc.SplitInclusive(taxIn)
	if errors.Is(err, tax.ErrUnknownFamily) {
		return nil, TaxPending()
	}
	if err != nil {
		return nil, err
	}
	if len(split.Lines) != len(lines) {
		return nil, apperr.Internal()
	}
	p := &Priced{Provisional: split.Provisional, Note: split.Note, DurationMinutes: duration}
	for i := range lines {
		s := split.Lines[i]
		if s.GrossPaise != lines[i].LineTotalPaise || s.TaxablePaise+s.TaxPaise != s.GrossPaise {
			return nil, apperr.Internal()
		}
		lines[i].TaxablePaise, lines[i].TaxPaise = s.TaxablePaise, s.TaxPaise
		lines[i].TaxRateBPS, lines[i].GSTCategory, lines[i].SAC = s.RateBPS, s.Category, s.SAC
		p.TotalPaise += lines[i].LineTotalPaise
		p.TaxablePaise += s.TaxablePaise
		p.TaxPaise += s.TaxPaise
	}
	p.Lines = lines
	return p, nil
}

// QuoteInput is everything BuildQuote needs besides the bundle.
type QuoteInput struct {
	QuoteID            uuid.UUID
	ProID              uuid.UUID
	ZoneID             uuid.UUID
	PlaceOfSupplyState string
	Now                time.Time
	TTL                time.Duration
}

// BuildQuote validates sel against b and prices it with the professional's
// approved prices. Prices are GST-inclusive.
func BuildQuote(b *ServiceBundle, sel Selection, prices ProPrices, in QuoteInput, tc tax.Computer) (*model.Quote, error) {
	opt, addons, verr := ValidateSelection(b, sel)
	if verr != nil {
		return nil, verr
	}
	p, err := PriceSelection(b, opt, addons, sel.Quantity, prices, in.PlaceOfSupplyState, in.Now, tc)
	if err != nil {
		return nil, err
	}
	return &model.Quote{
		ID: in.QuoteID, Status: model.QuoteOpen, ServiceID: b.Service.ID, OptionID: opt.ID, Quantity: sel.Quantity,
		ProID: in.ProID, CityCode: b.City.Code, ZoneID: in.ZoneID, Lines: p.Lines, TotalPaise: p.TotalPaise,
		TaxablePaise: p.TaxablePaise, TaxPaise: p.TaxPaise, PricesIncludeTax: true, TaxProvisional: p.Provisional,
		TaxNote: p.Note, DurationMinutes: p.DurationMinutes, ExpiresAt: in.Now.Add(in.TTL).UTC(), CreatedAt: in.Now.UTC(),
	}, nil
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
