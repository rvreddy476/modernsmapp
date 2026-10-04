// Package service is doorstep-service's application layer: it validates
// requests, applies the catalogue rules and maps store errors to the stable
// codes of contracts/doorstep/openapi.yaml.
package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

// CatalogueStore is what the customer catalogue and quotes need.
type CatalogueStore interface {
	City(ctx context.Context, code string) (model.CityRef, string, error)
	CategorySummaries(ctx context.Context, city string, at time.Time) ([]model.CategorySummary, error)
	CategoryBySlug(ctx context.Context, city, slug string, at time.Time) (*model.CategorySummary, error)
	ServiceSummaries(ctx context.Context, city string, categoryID uuid.UUID, at time.Time) ([]model.ServiceSummary, error)
	ServiceBundle(ctx context.Context, city string, serviceID uuid.UUID, at time.Time) (*catalogue.ServiceBundle, error)
	LocateZone(ctx context.Context, lat, lng float64) (*catalogue.ZoneHit, error)
	InsertQuote(ctx context.Context, q *model.Quote, customer uuid.UUID, lat, lng float64) error
	Quote(ctx context.Context, id, customer uuid.UUID) (*model.Quote, error)
}

// AdminStore is the admin-internal catalogue and config CRUD.
type AdminStore interface {
	ListCities(ctx context.Context) ([]model.AdminCity, error)
	CreateCity(ctx context.Context, a store.Actor, in model.AdminCityInput) (*model.AdminCity, error)
	UpdateCity(ctx context.Context, a store.Actor, code string, p model.AdminCityPatch) (*model.AdminCity, error)
	ListZones(ctx context.Context, city string) ([]model.AdminZone, error)
	CreateZone(ctx context.Context, a store.Actor, in model.AdminZoneInput) (*model.AdminZone, error)
	UpdateZone(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminZonePatch) (*model.AdminZone, error)
	ListCategories(ctx context.Context) ([]model.AdminCategory, error)
	CreateCategory(ctx context.Context, a store.Actor, in model.AdminCategoryInput) (*model.AdminCategory, error)
	UpdateCategory(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminCategoryPatch) (*model.AdminCategory, error)
	ListSkills(ctx context.Context) ([]model.Skill, error)
	CreateSkill(ctx context.Context, a store.Actor, in model.SkillInput) (*model.Skill, error)
	ListServices(ctx context.Context, categoryID *uuid.UUID) ([]model.AdminService, error)
	CreateService(ctx context.Context, a store.Actor, in model.AdminServiceInput) (*model.AdminService, error)
	ServiceTree(ctx context.Context, id uuid.UUID) (*model.AdminServiceTree, error)
	UpdateService(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminServicePatch) (*model.AdminService, error)
	CreateOption(ctx context.Context, a store.Actor, serviceID uuid.UUID, in model.AdminOptionInput) (*model.AdminOption, error)
	UpdateOption(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminOptionPatch) (*model.AdminOption, error)
	CreateAddonGroup(ctx context.Context, a store.Actor, serviceID uuid.UUID, in model.AdminAddonGroupInput) (*model.AdminAddonGroup, error)
	UpdateAddonGroup(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminAddonGroupPatch) (*model.AdminAddonGroup, error)
	CreateAddon(ctx context.Context, a store.Actor, groupID uuid.UUID, in model.AdminAddonInput) (*model.AdminAddon, error)
	UpdateAddon(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminAddonPatch) (*model.AdminAddon, error)
	ListPrices(ctx context.Context, city string, itemID *uuid.UUID) ([]model.AdminPrice, error)
	CreatePrice(ctx context.Context, a store.Actor, in model.AdminPriceInput, from time.Time) (*model.AdminPrice, error)
	ListRateCards(ctx context.Context, city string, categoryID *uuid.UUID) ([]model.AdminRateCard, error)
	CreateRateCard(ctx context.Context, a store.Actor, in model.AdminRateCardInput) (*model.AdminRateCard, error)
	UpdateRateCard(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminRateCardPatch) (*model.AdminRateCard, error)
	ListSlotConfigs(ctx context.Context, city string) ([]model.AdminSlotConfig, error)
	CreateSlotConfig(ctx context.Context, a store.Actor, in model.AdminSlotConfigInput) (*model.AdminSlotConfig, error)
	UpdateSlotConfig(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminSlotConfigPatch) (*model.AdminSlotConfig, error)
	ListCancellationRules(ctx context.Context, city string) ([]model.AdminCancellationRule, error)
	CreateCancellationRule(ctx context.Context, a store.Actor, in model.AdminCancellationRuleInput) (*model.AdminCancellationRule, error)
	UpdateCancellationRule(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminCancellationRulePatch) (*model.AdminCancellationRule, error)
	ListCommissionRules(ctx context.Context, city string) ([]model.AdminCommissionRule, error)
	CreateCommissionRule(ctx context.Context, a store.Actor, in model.AdminCommissionRuleInput, from time.Time) (*model.AdminCommissionRule, error)
	UpdateCommissionRule(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminCommissionRulePatch) (*model.AdminCommissionRule, error)
	ListAuditLogs(ctx context.Context, entity string, limit int) ([]model.AuditLog, error)
}

// Store is everything the service needs.
type Store interface {
	CatalogueStore
	AdminStore
}

// Service is the application layer.
type Service struct {
	store    Store
	tax      tax.Computer
	now      func() time.Time
	newID    func() uuid.UUID
	quoteTTL time.Duration
	// pro is professional onboarding (A2), wired by WithPro.
	pro ProDeps
	// bk is bookings and payments (A3), wired by WithBookings.
	bk BookingDeps
	// ds is dispatch, presence and realtime (A4), wired by WithDispatch.
	ds DispatchDeps
}

// New builds the service.
func New(st Store, tc tax.Computer, quoteTTL time.Duration) *Service {
	return &Service{store: st, tax: tc, now: time.Now, newID: uuid.New, quoteTTL: quoteTTL}
}

// WithClock pins the clock and id source (contract fixtures, tests).
func (s *Service) WithClock(now func() time.Time, newID func() uuid.UUID) *Service {
	s.now, s.newID = now, newID
	return s
}

var cityCodeRe = regexp.MustCompile(`^[A-Z]{3}$`)

func normaliseCity(raw string) (string, *apperr.Error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if !cityCodeRe.MatchString(code) {
		return "", apperr.Invalid("city", "city must be a three-letter city code such as HYD")
	}
	return code, nil
}

func internal(ctx context.Context, op string, err error) *apperr.Error {
	slog.ErrorContext(ctx, "doorstep: "+op, "error", err)
	return apperr.Internal()
}

func (s *Service) city(ctx context.Context, raw string) (model.CityRef, *apperr.Error) {
	code, aerr := normaliseCity(raw)
	if aerr != nil {
		return model.CityRef{}, aerr
	}
	c, _, err := s.store.City(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return model.CityRef{}, apperr.New(http.StatusNotFound, apperr.CodeCityNotFound, "Doorstep does not serve this city")
	}
	if err != nil {
		return model.CityRef{}, internal(ctx, "city", err)
	}
	return c, nil
}

// Catalogue lists a city's categories.
func (s *Service) Catalogue(ctx context.Context, city string) (*model.Catalogue, error) {
	c, aerr := s.city(ctx, city)
	if aerr != nil {
		return nil, aerr
	}
	cats, err := s.store.CategorySummaries(ctx, c.Code, s.now())
	if err != nil {
		return nil, internal(ctx, "catalogue", err)
	}
	return &model.Catalogue{City: c, Categories: cats}, nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Category is one category page.
func (s *Service) Category(ctx context.Context, city, slug string) (*model.CategoryPage, error) {
	c, aerr := s.city(ctx, city)
	if aerr != nil {
		return nil, aerr
	}
	notFound := apperr.New(http.StatusNotFound, apperr.CodeCategoryNotFound, "category not found")
	if !slugRe.MatchString(slug) {
		return nil, notFound
	}
	now := s.now()
	cat, err := s.store.CategoryBySlug(ctx, c.Code, slug, now)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound
	}
	if err != nil {
		return nil, internal(ctx, "category", err)
	}
	svcs, err := s.store.ServiceSummaries(ctx, c.Code, cat.ID, now)
	if err != nil {
		return nil, internal(ctx, "category services", err)
	}
	return &model.CategoryPage{City: c, Category: *cat, Services: svcs}, nil
}

func (s *Service) bundle(ctx context.Context, city string, id uuid.UUID, at time.Time) (*catalogue.ServiceBundle, *apperr.Error) {
	b, err := s.store.ServiceBundle(ctx, city, id, at)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !b.Visible()) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeServiceNotFound, "service not found")
	}
	if err != nil {
		return nil, internal(ctx, "service bundle", err)
	}
	return b, nil
}

func notAvailable() *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeServiceNotAvailable, "this service is not available in this city")
}

// ServiceDetail is one service page.
func (s *Service) ServiceDetail(ctx context.Context, city string, id uuid.UUID) (*model.ServicePage, error) {
	c, aerr := s.city(ctx, city)
	if aerr != nil {
		return nil, aerr
	}
	b, aerr := s.bundle(ctx, c.Code, id, s.now())
	if aerr != nil {
		return nil, aerr
	}
	d, ok := catalogue.DetailView(b)
	if !ok {
		return nil, notAvailable()
	}
	return &model.ServicePage{City: c, Service: d}, nil
}

func checkPoint(lat, lng *float64) (float64, float64, *apperr.Error) {
	if lat == nil || lng == nil {
		return 0, 0, apperr.Invalid("lat", "lat and lng are required")
	}
	if math.IsNaN(*lat) || *lat < -90 || *lat > 90 {
		return 0, 0, apperr.Invalid("lat", "lat must be between -90 and 90")
	}
	if math.IsNaN(*lng) || *lng < -180 || *lng > 180 {
		return 0, 0, apperr.Invalid("lng", "lng must be between -180 and 180")
	}
	return *lat, *lng, nil
}

// Serviceability answers whether a point is in an active zone.
func (s *Service) Serviceability(ctx context.Context, req model.ServiceabilityRequest) (*model.Serviceability, error) {
	lat, lng, aerr := checkPoint(req.Lat, req.Lng)
	if aerr != nil {
		return nil, aerr
	}
	hit, err := s.store.LocateZone(ctx, lat, lng)
	if err != nil {
		return nil, internal(ctx, "locate zone", err)
	}
	if hit == nil {
		reason := apperr.ReasonOutsideServiceArea
		return &model.Serviceability{Reason: &reason}, nil
	}
	city, zone := hit.City, hit.Zone
	return &model.Serviceability{Serviceable: true, City: &city, Zone: &zone}, nil
}

// CreateQuote validates and prices a selection for the customer.
func (s *Service) CreateQuote(ctx context.Context, customer uuid.UUID, req model.QuoteRequest) (*model.Quote, error) {
	if req.ServiceID == nil || *req.ServiceID == uuid.Nil {
		return nil, apperr.Invalid("service_id", "service_id is required")
	}
	if req.OptionID == nil || *req.OptionID == uuid.Nil {
		return nil, apperr.Invalid("option_id", "option_id is required")
	}
	lat, lng, aerr := checkPoint(req.Lat, req.Lng)
	if aerr != nil {
		return nil, aerr
	}
	sel := catalogue.Selection{OptionID: *req.OptionID, Quantity: 1}
	if req.Quantity != nil {
		sel.Quantity = *req.Quantity
	}
	if len(req.Addons) > 50 {
		return nil, apperr.Invalid("addons", "too many add-ons")
	}
	for _, a := range req.Addons {
		if a.AddonID == nil || *a.AddonID == uuid.Nil {
			return nil, apperr.Invalid("addons", "every add-on needs an addon_id")
		}
		sel.AddonIDs = append(sel.AddonIDs, *a.AddonID)
	}

	hit, err := s.store.LocateZone(ctx, lat, lng)
	if err != nil {
		return nil, internal(ctx, "locate zone", err)
	}
	if hit == nil {
		return nil, apperr.New(http.StatusUnprocessableEntity, apperr.CodeOutsideServiceArea, "this address is outside the service area")
	}
	now := s.now()
	b, aerr := s.bundle(ctx, hit.City.Code, *req.ServiceID, now)
	if aerr != nil {
		return nil, aerr
	}
	if _, ok := catalogue.DetailView(b); !ok {
		return nil, notAvailable()
	}
	q, err := catalogue.BuildQuote(b, sel, catalogue.QuoteInput{
		QuoteID: s.newID(), ZoneID: hit.Zone.ID, PlaceOfSupplyState: hit.StateCode, Now: now, TTL: s.quoteTTL,
	}, s.tax)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return nil, ae
		}
		return nil, internal(ctx, "price quote", err)
	}
	if err := s.store.InsertQuote(ctx, q, customer, lat, lng); err != nil {
		return nil, internal(ctx, "insert quote", err)
	}
	return q, nil
}

// Quote returns one of the customer's quotes.
func (s *Service) Quote(ctx context.Context, customer, id uuid.UUID) (*model.Quote, error) {
	q, err := s.store.Quote(ctx, id, customer)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeQuoteNotFound, "quote not found")
	}
	if err != nil {
		return nil, internal(ctx, "quote", err)
	}
	if q.Status == model.QuoteOpen && !s.now().Before(q.ExpiresAt) {
		q.Status = model.QuoteExpired
	}
	return q, nil
}
