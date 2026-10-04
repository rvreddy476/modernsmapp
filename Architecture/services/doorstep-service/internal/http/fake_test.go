package http

import (
	"context"
	"sync"
	"time"

	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// fakeStore is an in-memory store for handler tests and contract fixtures.
// Ids are the dev seed's deterministic ids (devseed.ID), so the fixtures
// describe the same rows a dev database holds. Admin methods not overridden
// below panic through the nil embedded interface (gin.Recovery: 500), which
// is how a test proves a request never reached the store.
type fakeStore struct {
	service.AdminStore

	mu      sync.Mutex
	quotes  map[uuid.UUID]*model.Quote
	actors  []store.Actor
	adminFn map[string]any
}

func newFakeStore() *fakeStore {
	return &fakeStore{quotes: map[uuid.UUID]*model.Quote{}, adminFn: map[string]any{}}
}

func (f *fakeStore) record(a store.Actor) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actors = append(f.actors, a)
}

var (
	fixtureNow     = time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC)
	fixtureQuoteID = uuid.MustParse("3f6b8a52-0c1d-4e7f-9a2b-5c6d7e8f9a01")
	fixtureUser    = uuid.MustParse("2d598287-eee7-40b4-a7f5-b46b9412e4e7")
	hyd            = model.CityRef{Code: "HYD", Name: "Hyderabad"}
)

func strp(s string) *string { return &s }
func i64p(v int64) *int64   { return &v }

func price(key string, paise int64, mrp int64) *catalogue.Price {
	p := &catalogue.Price{ID: devseed.ID("price", key), PricePaise: paise}
	if mrp > 0 {
		p.MRPPaise = i64p(mrp)
	}
	return p
}

func (f *fakeStore) City(_ context.Context, code string) (model.CityRef, string, error) {
	if code != "HYD" {
		return model.CityRef{}, "", store.ErrNotFound
	}
	return hyd, "36", nil
}

func fakeCategories() []model.CategorySummary {
	return []model.CategorySummary{
		{ID: devseed.ID("category", "home-cleaning"), Slug: "home-cleaning", Name: "Home cleaning",
			Description: "Bathroom, kitchen, full home, sofa and carpet", Family: "HOME_CLEANING", GenderRule: "any",
			SortOrder: 10, ServiceCount: 4, StartingPricePaise: i64p(24900)},
		{ID: devseed.ID("category", "salon-women"), Slug: "salon-women", Name: "Salon for women",
			Description: "Waxing, facials, threading, mani-pedi at home", Family: "BEAUTY_SALON", GenderRule: "female_pros_only",
			SortOrder: 90, ServiceCount: 4, StartingPricePaise: i64p(9900)},
		{ID: devseed.ID("category", "salon-men"), Slug: "salon-men", Name: "Salon for men",
			Description: "Haircut, beard, face care and massage at home", Family: "BEAUTY_SALON", GenderRule: "male_pros_only",
			SortOrder: 100, ServiceCount: 3, StartingPricePaise: i64p(29900)},
	}
}

func (f *fakeStore) CategorySummaries(context.Context, string, time.Time) ([]model.CategorySummary, error) {
	return fakeCategories(), nil
}

func (f *fakeStore) CategoryBySlug(_ context.Context, _ string, slug string, _ time.Time) (*model.CategorySummary, error) {
	for _, c := range fakeCategories() {
		if c.Slug == slug {
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) ServiceSummaries(_ context.Context, _ string, categoryID uuid.UUID, _ time.Time) ([]model.ServiceSummary, error) {
	if categoryID != devseed.ID("category", "salon-women") {
		return []model.ServiceSummary{}, nil
	}
	cat := devseed.ID("category", "salon-women")
	return []model.ServiceSummary{
		{ID: devseed.ID("service", "salon-women/waxing"), CategoryID: cat, Slug: "waxing", Name: "Waxing",
			Description: "Single-use spatulas and sealed wax", DurationMinutes: 60, StartingPricePaise: i64p(79900), SuggestedPricePaise: i64p(99900)},
		{ID: devseed.ID("service", "salon-women/facial"), CategoryID: cat, Slug: "facial", Name: "Facial",
			Description: "Sealed single-use kit", DurationMinutes: 60, StartingPricePaise: i64p(69900)},
		{ID: devseed.ID("service", "salon-women/threading"), CategoryID: cat, Slug: "threading", Name: "Threading",
			Description: "Eyebrows, upper lip or full face", DurationMinutes: 15, StartingPricePaise: i64p(9900)},
		{ID: devseed.ID("service", "salon-women/manicure-pedicure"), CategoryID: cat, Slug: "manicure-pedicure", Name: "Manicure and pedicure",
			Description: "Sterilised tools", DurationMinutes: 75, StartingPricePaise: i64p(99900)},
	}, nil
}

// facialBundle is salon-women/facial: a required one-of mask group and an
// optional up-to-two add-ons group; one inactive option and one unpriced
// add-on that must never be offered.
func facialBundle() *catalogue.ServiceBundle {
	k := "salon-women/facial"
	return &catalogue.ServiceBundle{
		City: hyd,
		Category: catalogue.CategoryRow{ID: devseed.ID("category", "salon-women"), Slug: "salon-women", Name: "Salon for women",
			Family: "BEAUTY_SALON", GenderRule: "female_pros_only", ExtrasPolicy: "catalogue_addons_only", Active: true},
		Service: catalogue.ServiceRow{ID: devseed.ID("service", k), Slug: "facial", Name: "Facial", Description: "Sealed single-use kit",
			DurationMinutes: 60, Inclusions: []string{}, Exclusions: []string{}, CrewSize: 1, ReworkDays: 3,
			MinBeforePhotos: 2, MinAfterPhotos: 2, Active: true},
		Options: []catalogue.OptionRow{
			{ID: devseed.ID("option", k+"/cleanup"), Name: "Fruit cleanup", DurationMinutes: 45, MaxQuantity: 1, Active: true, Price: price(k+"/cleanup", 69900, 0)},
			{ID: devseed.ID("option", k+"/gold"), Name: "Gold facial", DurationMinutes: 60, MaxQuantity: 1, IsDefault: true, Active: true, Price: price(k+"/gold", 129900, 149900)},
			{ID: devseed.ID("option", k+"/o3"), Name: "O3+ facial", DurationMinutes: 75, MaxQuantity: 1, Active: false, Price: price(k+"/o3", 219900, 0)},
		},
		Groups: []catalogue.GroupRow{
			{ID: devseed.ID("addon_group", k+"/mask"), Name: "Choose a mask", MinSelect: 1, MaxSelect: 1, IsRequired: true, Active: true,
				Addons: []catalogue.AddonRow{
					{ID: devseed.ID("addon", k+"/mask/peel-off"), Name: "Peel-off mask", ExtraDurationMinutes: 5, Active: true, Price: price(k+"/mask/peel-off", 9900, 0)},
					{ID: devseed.ID("addon", k+"/mask/charcoal"), Name: "Charcoal mask", ExtraDurationMinutes: 5, Active: true, Price: price(k+"/mask/charcoal", 19900, 0)},
				}},
			{ID: devseed.ID("addon_group", k+"/add-ons"), Name: "Add-ons", MinSelect: 0, MaxSelect: 2, Active: true,
				Addons: []catalogue.AddonRow{
					{ID: devseed.ID("addon", k+"/add-ons/threading"), Name: "Threading (eyebrows and upper lip)", ExtraDurationMinutes: 10, Active: true, Price: price(k+"/add-ons/threading", 9900, 0)},
					{ID: devseed.ID("addon", k+"/add-ons/head-massage"), Name: "Head massage", ExtraDurationMinutes: 20, Active: true, Price: price(k+"/add-ons/head-massage", 29900, 0)},
					{ID: devseed.ID("addon", k+"/add-ons/unpriced"), Name: "Unpriced add-on", Active: true},
				}},
		},
	}
}

// kitchenBundle is home-cleaning/kitchen-deep-cleaning (HOME_CLEANING, 18%).
func kitchenBundle() *catalogue.ServiceBundle {
	k := "home-cleaning/kitchen-deep-cleaning"
	return &catalogue.ServiceBundle{
		City: hyd,
		Category: catalogue.CategoryRow{ID: devseed.ID("category", "home-cleaning"), Slug: "home-cleaning", Name: "Home cleaning",
			Family: "HOME_CLEANING", GenderRule: "any", ExtrasPolicy: "rate_card", Active: true},
		Service: catalogue.ServiceRow{ID: devseed.ID("service", k), Slug: "kitchen-deep-cleaning", Name: "Kitchen deep cleaning",
			Description: "Slab, tiles, sink and cabinets outside degreased", DurationMinutes: 150,
			Inclusions: []string{"Slab and tiles degreased", "Sink descaled", "Cabinet exteriors"},
			Exclusions: []string{"Inside appliances unless added"}, CrewSize: 1, ReworkDays: 7, MinBeforePhotos: 2, MinAfterPhotos: 2, Active: true},
		Options: []catalogue.OptionRow{
			{ID: devseed.ID("option", k+"/empty"), Name: "Empty kitchen", DurationMinutes: 150, MaxQuantity: 1, Active: true, Price: price(k+"/empty", 149900, 0)},
			{ID: devseed.ID("option", k+"/occupied"), Name: "Occupied kitchen", DurationMinutes: 180, MaxQuantity: 1, IsDefault: true, Active: true, Price: price(k+"/occupied", 179900, 199900)},
		},
		Groups: []catalogue.GroupRow{
			{ID: devseed.ID("addon_group", k+"/appliances"), Name: "Appliances", MinSelect: 0, MaxSelect: 3, Active: true,
				Addons: []catalogue.AddonRow{
					{ID: devseed.ID("addon", k+"/appliances/chimney"), Name: "Chimney cleaning", ExtraDurationMinutes: 30, Active: true, Price: price(k+"/appliances/chimney", 44900, 0)},
					{ID: devseed.ID("addon", k+"/appliances/fridge"), Name: "Fridge cleaning", ExtraDurationMinutes: 20, Active: true, Price: price(k+"/appliances/fridge", 29900, 0)},
				}},
		},
	}
}

func (f *fakeStore) ServiceBundle(_ context.Context, _ string, id uuid.UUID, _ time.Time) (*catalogue.ServiceBundle, error) {
	for _, b := range []*catalogue.ServiceBundle{facialBundle(), kitchenBundle()} {
		if b.Service.ID == id {
			return b, nil
		}
	}
	return nil, store.ErrNotFound
}

// LocateZone mirrors the seeded rectangles (west wins nothing on the shared
// edge: the store orders by name, and "Banjara" < "Gachibowli").
func (f *fakeStore) LocateZone(_ context.Context, lat, lng float64) (*catalogue.ZoneHit, error) {
	in := func(lng0, lng1, lat0, lat1 float64) bool {
		return lng >= lng0 && lng <= lng1 && lat >= lat0 && lat <= lat1
	}
	switch {
	case in(78.40, 78.47, 17.40, 17.45):
		return &catalogue.ZoneHit{Zone: model.ZoneRef{ID: devseed.ID("zone", "central-banjara-jubilee"), Name: "Banjara - Jubilee Hills"}, City: hyd, StateCode: "36"}, nil
	case in(78.33, 78.40, 17.40, 17.48):
		return &catalogue.ZoneHit{Zone: model.ZoneRef{ID: devseed.ID("zone", "west-hitec-gachibowli"), Name: "Gachibowli - HITEC City"}, City: hyd, StateCode: "36"}, nil
	}
	return nil, nil
}

func (f *fakeStore) InsertQuote(_ context.Context, q *model.Quote, customer uuid.UUID, _, _ float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *q
	f.quotes[q.ID] = &cp
	_ = customer
	return nil
}

func (f *fakeStore) Quote(_ context.Context, id, _ uuid.UUID) (*model.Quote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q, ok := f.quotes[id]; ok {
		cp := *q
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

// ---- admin overrides used by the fixtures and token tests ----

var fixtureTS = time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC)

func (f *fakeStore) ListCities(context.Context) ([]model.AdminCity, error) {
	return []model.AdminCity{{Code: "HYD", Name: "Hyderabad", StateCode: "36", Timezone: "Asia/Kolkata", Active: true,
		ExtrasChargeNowThresholdPaise: 300000, ExtrasGraceMinutes: 15, MaxJobsPerDay: 6, OfferWindowFarMinutes: 120,
		OfferWindowNearMinutes: 10, OfferFarThresholdMinutes: 720, CreatedAt: fixtureTS, UpdatedAt: fixtureTS}}, nil
}

func (f *fakeStore) CreateCategory(_ context.Context, a store.Actor, in model.AdminCategoryInput) (*model.AdminCategory, error) {
	f.record(a)
	c := &model.AdminCategory{ID: devseed.ID("category", in.Slug), Slug: in.Slug, Name: in.Name, Family: in.Family,
		GenderRule: "any", ExtrasPolicy: "rate_card", ImageURL: in.ImageURL, CreatedAt: fixtureTS, UpdatedAt: fixtureTS}
	if in.Description != nil {
		c.Description = *in.Description
	}
	if in.GenderRule != nil {
		c.GenderRule = *in.GenderRule
	}
	if in.ExtrasPolicy != nil {
		c.ExtrasPolicy = *in.ExtrasPolicy
	}
	if in.SortOrder != nil {
		c.SortOrder = *in.SortOrder
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
	return c, nil
}

func (f *fakeStore) CreateZone(_ context.Context, a store.Actor, in model.AdminZoneInput) (*model.AdminZone, error) {
	f.record(a)
	return &model.AdminZone{ID: devseed.ID("zone", in.Slug), CityCode: in.CityCode, Name: in.Name, Slug: in.Slug, Active: true,
		TravelBufferMinutes: 30, Boundary: in.Boundary, CreatedAt: fixtureTS, UpdatedAt: fixtureTS}, nil
}

func (f *fakeStore) CreatePrice(_ context.Context, a store.Actor, in model.AdminPriceInput, from time.Time) (*model.AdminPrice, error) {
	f.record(a)
	if in.PricePaise == 77700 {
		return nil, store.ErrOverlap
	}
	actor := a.UserID
	return &model.AdminPrice{ID: uuid.MustParse("8a1c2e3f-4b5d-4c6e-8f70-9a1b2c3d4e5f"), CityCode: in.CityCode, ItemKind: in.ItemKind,
		ItemID: *in.ItemID, PricePaise: in.PricePaise, MRPPaise: in.MRPPaise, EffectiveFrom: from, CreatedBy: &actor, CreatedAt: fixtureTS}, nil
}
