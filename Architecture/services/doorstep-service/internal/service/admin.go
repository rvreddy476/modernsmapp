package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Admin-internal catalogue and config CRUD. The HTTP layer has already
// admitted an admin-service token carrying the route's permission; the
// store writes each change and its audit row in one transaction.

var (
	families      = set(tax.Families...)
	optionUnits   = set("per_job", "per_hour", "per_month")
	genderRules   = set("any", "female_pros_only", "male_pros_only")
	extrasPolices = set("rate_card", "catalogue_addons_only")
	units         = set("per_item", "per_metre", "per_hour", "per_visit")
	stages        = set("unassigned", "assigned", "en_route", "arrived", "in_progress")
	skillRe       = regexp.MustCompile(`^[a-z][a-z0-9_]{1,47}$`)
	rateCodeRe    = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)
	hhmmRe        = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

// adminErr maps store errors for the admin family.
func adminErr(ctx context.Context, op string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return apperr.New(http.StatusNotFound, apperr.CodeNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		return apperr.New(http.StatusConflict, apperr.CodeConflict, "conflicts with an existing row (slug, code or default option already taken)")
	case errors.Is(err, store.ErrOverlap):
		return apperr.New(http.StatusConflict, apperr.CodePriceOverlap, "a new price must start after the current one; price periods may not overlap")
	case errors.Is(err, store.ErrZoneInvalid):
		return apperr.New(http.StatusUnprocessableEntity, apperr.CodeZoneInvalid, "boundary is not a valid GeoJSON Polygon or MultiPolygon")
	case errors.Is(err, store.ErrBadReference):
		return apperr.Invalid("", "a referenced city, category, skill or item does not exist")
	case errors.Is(err, store.ErrInvalid):
		return apperr.Invalid("", "a value is outside its allowed range")
	}
	return internal(ctx, op, err)
}

func required(field, v string) *apperr.Error {
	if strings.TrimSpace(v) == "" {
		return apperr.Invalid(field, field+" is required")
	}
	return nil
}

func oneOf(field string, v *string, allowed map[string]bool) *apperr.Error {
	if v != nil && !allowed[*v] {
		return apperr.Invalid(field, field+" has an unsupported value")
	}
	return nil
}

func firstErr(errs ...*apperr.Error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

// ---- cities ----

func (s *Service) AdminListCities(ctx context.Context) ([]model.AdminCity, error) {
	v, err := s.store.ListCities(ctx)
	return v, adminErr(ctx, "list cities", err)
}

func validStateCode(field string, v *string) *apperr.Error {
	if v != nil && !kyc.IsValidGSTStateCode(*v) {
		return apperr.Invalid(field, "state_code must be a two-digit GST state code")
	}
	return nil
}

func (s *Service) AdminCreateCity(ctx context.Context, a store.Actor, in model.AdminCityInput) (*model.AdminCity, error) {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	if err := firstErr(
		func() *apperr.Error {
			if !cityCodeRe.MatchString(in.Code) {
				return apperr.Invalid("code", "code must be three capital letters")
			}
			return nil
		}(),
		required("name", in.Name), validStateCode("state_code", &in.StateCode),
	); err != nil {
		return nil, err
	}
	v, err := s.store.CreateCity(ctx, a, in)
	return v, adminErr(ctx, "create city", err)
}

func (s *Service) AdminUpdateCity(ctx context.Context, a store.Actor, code string, p model.AdminCityPatch) (*model.AdminCity, error) {
	code, aerr := normaliseCity(code)
	if aerr != nil {
		return nil, aerr
	}
	if err := validStateCode("state_code", p.StateCode); err != nil {
		return nil, err
	}
	v, err := s.store.UpdateCity(ctx, a, code, p)
	return v, adminErr(ctx, "update city", err)
}

// ---- zones ----

func (s *Service) AdminListZones(ctx context.Context, city string) ([]model.AdminZone, error) {
	if city != "" {
		code, aerr := normaliseCity(city)
		if aerr != nil {
			return nil, aerr
		}
		city = code
	}
	v, err := s.store.ListZones(ctx, city)
	return v, adminErr(ctx, "list zones", err)
}

func zoneInvalid(msg string) *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeZoneInvalid, msg)
}

// ValidateBoundary checks the GeoJSON shape before PostGIS sees it: a
// Polygon or MultiPolygon of closed rings of at least four [lng, lat]
// positions within range.
func ValidateBoundary(raw json.RawMessage) *apperr.Error {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return zoneInvalid("boundary must be a GeoJSON object")
	}
	var polys [][][][]float64
	switch g.Type {
	case "Polygon":
		var p [][][]float64
		if json.Unmarshal(g.Coordinates, &p) != nil {
			return zoneInvalid("Polygon coordinates must be rings of [lng, lat] positions")
		}
		polys = [][][][]float64{p}
	case "MultiPolygon":
		if json.Unmarshal(g.Coordinates, &polys) != nil {
			return zoneInvalid("MultiPolygon coordinates must be polygons of rings of [lng, lat] positions")
		}
	default:
		return zoneInvalid("boundary type must be Polygon or MultiPolygon")
	}
	if len(polys) == 0 {
		return zoneInvalid("boundary has no polygon")
	}
	for _, p := range polys {
		if len(p) == 0 {
			return zoneInvalid("a polygon has no ring")
		}
		for _, ring := range p {
			if len(ring) < 4 {
				return zoneInvalid("every ring needs at least four positions")
			}
			for _, pos := range ring {
				if len(pos) < 2 || pos[0] < -180 || pos[0] > 180 || pos[1] < -90 || pos[1] > 90 {
					return zoneInvalid("positions must be [lng, lat] within range")
				}
			}
			first, last := ring[0], ring[len(ring)-1]
			if first[0] != last[0] || first[1] != last[1] {
				return zoneInvalid("every ring must be closed (first position == last)")
			}
		}
	}
	return nil
}

func (s *Service) AdminCreateZone(ctx context.Context, a store.Actor, in model.AdminZoneInput) (*model.AdminZone, error) {
	code, aerr := normaliseCity(in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	in.CityCode = code
	if err := firstErr(required("name", in.Name), func() *apperr.Error {
		if !slugRe.MatchString(in.Slug) {
			return apperr.Invalid("slug", "slug must be lower-case words joined by hyphens")
		}
		return nil
	}(), ValidateBoundary(in.Boundary)); err != nil {
		return nil, err
	}
	v, err := s.store.CreateZone(ctx, a, in)
	return v, adminErr(ctx, "create zone", err)
}

func (s *Service) AdminUpdateZone(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminZonePatch) (*model.AdminZone, error) {
	if len(p.Boundary) > 0 && string(p.Boundary) != "null" {
		if err := ValidateBoundary(p.Boundary); err != nil {
			return nil, err
		}
	}
	v, err := s.store.UpdateZone(ctx, a, id, p)
	return v, adminErr(ctx, "update zone", err)
}

// ---- categories, skills ----

func (s *Service) AdminListCategories(ctx context.Context) ([]model.AdminCategory, error) {
	v, err := s.store.ListCategories(ctx)
	return v, adminErr(ctx, "list categories", err)
}

func (s *Service) AdminCreateCategory(ctx context.Context, a store.Actor, in model.AdminCategoryInput) (*model.AdminCategory, error) {
	if in.Family == "BEAUTY_SALON" && in.ExtrasPolicy == nil {
		in.ExtrasPolicy = ptr("catalogue_addons_only")
	}
	if err := firstErr(
		func() *apperr.Error {
			if !slugRe.MatchString(in.Slug) {
				return apperr.Invalid("slug", "slug must be lower-case words joined by hyphens")
			}
			return nil
		}(),
		required("name", in.Name), oneOf("family", &in.Family, families),
		oneOf("gender_rule", in.GenderRule, genderRules), oneOf("extras_policy", in.ExtrasPolicy, extrasPolices),
	); err != nil {
		return nil, err
	}
	v, err := s.store.CreateCategory(ctx, a, in)
	return v, adminErr(ctx, "create category", err)
}

func (s *Service) AdminUpdateCategory(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminCategoryPatch) (*model.AdminCategory, error) {
	if err := firstErr(oneOf("family", p.Family, families), oneOf("gender_rule", p.GenderRule, genderRules),
		oneOf("extras_policy", p.ExtrasPolicy, extrasPolices)); err != nil {
		return nil, err
	}
	v, err := s.store.UpdateCategory(ctx, a, id, p)
	return v, adminErr(ctx, "update category", err)
}

func (s *Service) AdminListSkills(ctx context.Context) ([]model.Skill, error) {
	v, err := s.store.ListSkills(ctx)
	return v, adminErr(ctx, "list skills", err)
}

func (s *Service) AdminCreateSkill(ctx context.Context, a store.Actor, in model.SkillInput) (*model.Skill, error) {
	if !skillRe.MatchString(in.Code) {
		return nil, apperr.Invalid("code", "code must be lower_snake_case")
	}
	if err := required("name", in.Name); err != nil {
		return nil, err
	}
	v, err := s.store.CreateSkill(ctx, a, in)
	return v, adminErr(ctx, "create skill", err)
}

// ---- services, options, add-ons ----

func (s *Service) AdminListServices(ctx context.Context, categoryID *uuid.UUID) ([]model.AdminService, error) {
	v, err := s.store.ListServices(ctx, categoryID)
	return v, adminErr(ctx, "list services", err)
}

func (s *Service) AdminCreateService(ctx context.Context, a store.Actor, in model.AdminServiceInput) (*model.AdminService, error) {
	if in.CategoryID == nil || *in.CategoryID == uuid.Nil {
		return nil, apperr.Invalid("category_id", "category_id is required")
	}
	if !slugRe.MatchString(in.Slug) {
		return nil, apperr.Invalid("slug", "slug must be lower-case words joined by hyphens")
	}
	if err := firstErr(required("name", in.Name), required("required_skill", in.RequiredSkill)); err != nil {
		return nil, err
	}
	if in.DurationMinutes < 15 || in.DurationMinutes > 720 {
		return nil, apperr.Invalid("duration_minutes", "duration_minutes must be between 15 and 720")
	}
	if in.CrewSize != nil && *in.CrewSize != 1 {
		return nil, apperr.Invalid("crew_size", "crew bookings are switched off at launch: crew_size must be 1")
	}
	v, err := s.store.CreateService(ctx, a, in)
	return v, adminErr(ctx, "create service", err)
}

func (s *Service) AdminServiceTree(ctx context.Context, id uuid.UUID) (*model.AdminServiceTree, error) {
	v, err := s.store.ServiceTree(ctx, id)
	return v, adminErr(ctx, "service tree", err)
}

func (s *Service) AdminUpdateService(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminServicePatch) (*model.AdminService, error) {
	if p.DurationMinutes != nil && (*p.DurationMinutes < 15 || *p.DurationMinutes > 720) {
		return nil, apperr.Invalid("duration_minutes", "duration_minutes must be between 15 and 720")
	}
	v, err := s.store.UpdateService(ctx, a, id, p)
	return v, adminErr(ctx, "update service", err)
}

func (s *Service) AdminCreateOption(ctx context.Context, a store.Actor, serviceID uuid.UUID, in model.AdminOptionInput) (*model.AdminOption, error) {
	if err := required("name", in.Name); err != nil {
		return nil, err
	}
	if in.DurationMinutes < 5 || in.DurationMinutes > 720 {
		return nil, apperr.Invalid("duration_minutes", "duration_minutes must be between 5 and 720")
	}
	if in.MaxQuantity != nil && (*in.MaxQuantity < 1 || *in.MaxQuantity > 20) {
		return nil, apperr.Invalid("max_quantity", "max_quantity must be between 1 and 20")
	}
	if err := oneOf("unit", in.Unit, optionUnits); err != nil {
		return nil, err
	}
	v, err := s.store.CreateOption(ctx, a, serviceID, in)
	return v, adminErr(ctx, "create option", err)
}

func (s *Service) AdminUpdateOption(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminOptionPatch) (*model.AdminOption, error) {
	if err := oneOf("unit", p.Unit, optionUnits); err != nil {
		return nil, err
	}
	v, err := s.store.UpdateOption(ctx, a, id, p)
	return v, adminErr(ctx, "update option", err)
}

func (s *Service) AdminCreateAddonGroup(ctx context.Context, a store.Actor, serviceID uuid.UUID, in model.AdminAddonGroupInput) (*model.AdminAddonGroup, error) {
	if err := required("name", in.Name); err != nil {
		return nil, err
	}
	min := 0
	if in.MinSelect != nil {
		min = *in.MinSelect
	}
	if min < 0 || in.MaxSelect < 1 || min > in.MaxSelect {
		return nil, apperr.Invalid("max_select", "need 0 <= min_select <= max_select and max_select >= 1")
	}
	v, err := s.store.CreateAddonGroup(ctx, a, serviceID, in)
	return v, adminErr(ctx, "create addon group", err)
}

func (s *Service) AdminUpdateAddonGroup(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminAddonGroupPatch) (*model.AdminAddonGroup, error) {
	v, err := s.store.UpdateAddonGroup(ctx, a, id, p)
	return v, adminErr(ctx, "update addon group", err)
}

func (s *Service) AdminCreateAddon(ctx context.Context, a store.Actor, groupID uuid.UUID, in model.AdminAddonInput) (*model.AdminAddon, error) {
	if err := required("name", in.Name); err != nil {
		return nil, err
	}
	v, err := s.store.CreateAddon(ctx, a, groupID, in)
	return v, adminErr(ctx, "create addon", err)
}

func (s *Service) AdminUpdateAddon(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminAddonPatch) (*model.AdminAddon, error) {
	v, err := s.store.UpdateAddon(ctx, a, id, p)
	return v, adminErr(ctx, "update addon", err)
}

// ---- prices, rate cards ----

func (s *Service) AdminListPrices(ctx context.Context, city string, itemID *uuid.UUID) ([]model.AdminPrice, error) {
	if city != "" {
		code, aerr := normaliseCity(city)
		if aerr != nil {
			return nil, aerr
		}
		city = code
	}
	v, err := s.store.ListPrices(ctx, city, itemID)
	return v, adminErr(ctx, "list prices", err)
}

// backdateTolerance absorbs clock skew between the console and the service.
const backdateTolerance = time.Minute

func (s *Service) AdminCreatePrice(ctx context.Context, a store.Actor, in model.AdminPriceInput) (*model.AdminPrice, error) {
	code, aerr := normaliseCity(in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	in.CityCode = code
	if in.ItemKind != "option" && in.ItemKind != "addon" {
		return nil, apperr.Invalid("item_kind", "item_kind must be option or addon")
	}
	if in.ItemID == nil || *in.ItemID == uuid.Nil {
		return nil, apperr.Invalid("item_id", "item_id is required")
	}
	if in.PricePaise <= 0 {
		return nil, apperr.Invalid("price_paise", "price_paise must be a positive whole number of paise")
	}
	if in.MRPPaise != nil && *in.MRPPaise < in.PricePaise {
		return nil, apperr.Invalid("mrp_paise", "mrp_paise must be at least price_paise")
	}
	now := s.now()
	from := now
	if in.EffectiveFrom != nil {
		from = *in.EffectiveFrom
		if from.Before(now.Add(-backdateTolerance)) {
			return nil, apperr.Invalid("effective_from", "prices cannot be backdated")
		}
	}
	v, err := s.store.CreatePrice(ctx, a, in, from.UTC())
	return v, adminErr(ctx, "create price", err)
}

func (s *Service) AdminListRateCards(ctx context.Context, city string, categoryID *uuid.UUID) ([]model.AdminRateCard, error) {
	if city != "" {
		code, aerr := normaliseCity(city)
		if aerr != nil {
			return nil, aerr
		}
		city = code
	}
	v, err := s.store.ListRateCards(ctx, city, categoryID)
	return v, adminErr(ctx, "list rate cards", err)
}

func (s *Service) AdminCreateRateCard(ctx context.Context, a store.Actor, in model.AdminRateCardInput) (*model.AdminRateCard, error) {
	code, aerr := normaliseCity(in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	in.CityCode = code
	if in.CategoryID == nil || *in.CategoryID == uuid.Nil {
		return nil, apperr.Invalid("category_id", "category_id is required")
	}
	if !rateCodeRe.MatchString(in.Code) {
		return nil, apperr.Invalid("code", "code must be lower_snake_case")
	}
	if err := firstErr(required("name", in.Name), oneOf("unit", &in.Unit, units)); err != nil {
		return nil, err
	}
	if in.PricePaise <= 0 {
		return nil, apperr.Invalid("price_paise", "price_paise must be positive")
	}
	v, err := s.store.CreateRateCard(ctx, a, in)
	return v, adminErr(ctx, "create rate card", err)
}

func (s *Service) AdminUpdateRateCard(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminRateCardPatch) (*model.AdminRateCard, error) {
	if err := oneOf("unit", p.Unit, units); err != nil {
		return nil, err
	}
	if p.PricePaise != nil && *p.PricePaise <= 0 {
		return nil, apperr.Invalid("price_paise", "price_paise must be positive")
	}
	v, err := s.store.UpdateRateCard(ctx, a, id, p)
	return v, adminErr(ctx, "update rate card", err)
}

// ---- config ----

func validTimes(open, close *string) *apperr.Error {
	if open != nil && !hhmmRe.MatchString(*open) {
		return apperr.Invalid("open_time", "open_time must be HH:MM")
	}
	if close != nil && !hhmmRe.MatchString(*close) {
		return apperr.Invalid("close_time", "close_time must be HH:MM")
	}
	if open != nil && close != nil && *close <= *open {
		return apperr.Invalid("close_time", "close_time must be after open_time")
	}
	return nil
}

func (s *Service) AdminListSlotConfigs(ctx context.Context, city string) ([]model.AdminSlotConfig, error) {
	v, err := s.store.ListSlotConfigs(ctx, strings.ToUpper(strings.TrimSpace(city)))
	return v, adminErr(ctx, "list slot configs", err)
}

func (s *Service) AdminCreateSlotConfig(ctx context.Context, a store.Actor, in model.AdminSlotConfigInput) (*model.AdminSlotConfig, error) {
	code, aerr := normaliseCity(in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	in.CityCode = code
	if err := validTimes(&in.OpenTime, &in.CloseTime); err != nil {
		return nil, err
	}
	v, err := s.store.CreateSlotConfig(ctx, a, in)
	return v, adminErr(ctx, "create slot config", err)
}

func (s *Service) AdminUpdateSlotConfig(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminSlotConfigPatch) (*model.AdminSlotConfig, error) {
	if err := validTimes(p.OpenTime, p.CloseTime); err != nil {
		return nil, err
	}
	v, err := s.store.UpdateSlotConfig(ctx, a, id, p)
	return v, adminErr(ctx, "update slot config", err)
}

func (s *Service) AdminListCancellationRules(ctx context.Context, city string) ([]model.AdminCancellationRule, error) {
	v, err := s.store.ListCancellationRules(ctx, strings.ToUpper(strings.TrimSpace(city)))
	return v, adminErr(ctx, "list cancellation rules", err)
}

func (s *Service) AdminCreateCancellationRule(ctx context.Context, a store.Actor, in model.AdminCancellationRuleInput) (*model.AdminCancellationRule, error) {
	code, aerr := normaliseCity(in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	in.CityCode = code
	if err := oneOf("stage", &in.Stage, stages); err != nil {
		return nil, err
	}
	if in.FeePaise < 0 {
		return nil, apperr.Invalid("fee_paise", "fee_paise cannot be negative")
	}
	if in.MinutesBeforeLT != nil && *in.MinutesBeforeLT <= 0 {
		return nil, apperr.Invalid("minutes_before_lt", "minutes_before_lt must be positive")
	}
	v, err := s.store.CreateCancellationRule(ctx, a, in)
	return v, adminErr(ctx, "create cancellation rule", err)
}

func (s *Service) AdminUpdateCancellationRule(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminCancellationRulePatch) (*model.AdminCancellationRule, error) {
	if p.FeePaise != nil && *p.FeePaise < 0 {
		return nil, apperr.Invalid("fee_paise", "fee_paise cannot be negative")
	}
	if m := p.MinutesBeforeLT.Ptr(); m != nil && *m <= 0 {
		return nil, apperr.Invalid("minutes_before_lt", "minutes_before_lt must be positive")
	}
	v, err := s.store.UpdateCancellationRule(ctx, a, id, p)
	return v, adminErr(ctx, "update cancellation rule", err)
}

func (s *Service) AdminListCommissionRules(ctx context.Context, city string) ([]model.AdminCommissionRule, error) {
	v, err := s.store.ListCommissionRules(ctx, strings.ToUpper(strings.TrimSpace(city)))
	return v, adminErr(ctx, "list commission rules", err)
}

func validBPS(v *int) *apperr.Error {
	if v != nil && (*v < 0 || *v > 5000) {
		return apperr.Invalid("commission_bps", "commission_bps must be between 0 and 5000")
	}
	return nil
}

func (s *Service) AdminCreateCommissionRule(ctx context.Context, a store.Actor, in model.AdminCommissionRuleInput) (*model.AdminCommissionRule, error) {
	code, aerr := normaliseCity(in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	in.CityCode = code
	if in.CommissionBPS == nil {
		return nil, apperr.Invalid("commission_bps", "commission_bps is required")
	}
	if err := validBPS(in.CommissionBPS); err != nil {
		return nil, err
	}
	from := s.now().UTC()
	if in.EffectiveFrom != nil {
		from = in.EffectiveFrom.UTC()
	}
	if in.EffectiveTo != nil && !in.EffectiveTo.After(from) {
		return nil, apperr.Invalid("effective_to", "effective_to must be after effective_from")
	}
	v, err := s.store.CreateCommissionRule(ctx, a, in, from)
	return v, adminErr(ctx, "create commission rule", err)
}

func (s *Service) AdminUpdateCommissionRule(ctx context.Context, a store.Actor, id uuid.UUID, p model.AdminCommissionRulePatch) (*model.AdminCommissionRule, error) {
	if err := validBPS(p.CommissionBPS); err != nil {
		return nil, err
	}
	v, err := s.store.UpdateCommissionRule(ctx, a, id, p)
	return v, adminErr(ctx, "update commission rule", err)
}

// ---- audit ----

func (s *Service) AdminAuditLogs(ctx context.Context, entity string, limit int) ([]model.AuditLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	v, err := s.store.ListAuditLogs(ctx, strings.TrimSpace(entity), limit)
	return v, adminErr(ctx, "audit logs", err)
}
