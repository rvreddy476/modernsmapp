package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Nullable is a PATCH field that distinguishes absent (Set=false), null
// (Set, Null) and a value (Set, Value).
type Nullable[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON implements json.Unmarshaler (called for null too).
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// Ptr returns nil for absent/null, else a pointer to the value.
func (n Nullable[T]) Ptr() *T {
	if !n.Set || n.Null {
		return nil
	}
	v := n.Value
	return &v
}

// List wraps admin list responses.
type List[T any] struct {
	Items []T `json:"items"`
}

// ---- cities ----

type AdminCity struct {
	Code                          string    `json:"code"`
	Name                          string    `json:"name"`
	StateCode                     string    `json:"state_code"`
	Timezone                      string    `json:"timezone"`
	Active                        bool      `json:"active"`
	ExtrasChargeNowThresholdPaise int64     `json:"extras_charge_now_threshold_paise"`
	ExtrasGraceMinutes            int       `json:"extras_grace_minutes"`
	MaxJobsPerDay                 int       `json:"max_jobs_per_day"`
	OfferWindowFarMinutes         int       `json:"offer_window_far_minutes"`
	OfferWindowNearMinutes        int       `json:"offer_window_near_minutes"`
	OfferFarThresholdMinutes      int       `json:"offer_far_threshold_minutes"`
	CreatedAt                     time.Time `json:"created_at"`
	UpdatedAt                     time.Time `json:"updated_at"`
}

type AdminCityInput struct {
	Code                          string  `json:"code"`
	Name                          string  `json:"name"`
	StateCode                     string  `json:"state_code"`
	Timezone                      *string `json:"timezone"`
	Active                        *bool   `json:"active"`
	ExtrasChargeNowThresholdPaise *int64  `json:"extras_charge_now_threshold_paise"`
	ExtrasGraceMinutes            *int    `json:"extras_grace_minutes"`
	MaxJobsPerDay                 *int    `json:"max_jobs_per_day"`
	OfferWindowFarMinutes         *int    `json:"offer_window_far_minutes"`
	OfferWindowNearMinutes        *int    `json:"offer_window_near_minutes"`
	OfferFarThresholdMinutes      *int    `json:"offer_far_threshold_minutes"`
}

type AdminCityPatch struct {
	Name                          *string `json:"name"`
	StateCode                     *string `json:"state_code"`
	Timezone                      *string `json:"timezone"`
	Active                        *bool   `json:"active"`
	ExtrasChargeNowThresholdPaise *int64  `json:"extras_charge_now_threshold_paise"`
	ExtrasGraceMinutes            *int    `json:"extras_grace_minutes"`
	MaxJobsPerDay                 *int    `json:"max_jobs_per_day"`
	OfferWindowFarMinutes         *int    `json:"offer_window_far_minutes"`
	OfferWindowNearMinutes        *int    `json:"offer_window_near_minutes"`
	OfferFarThresholdMinutes      *int    `json:"offer_far_threshold_minutes"`
}

// ---- zones ----

type AdminZone struct {
	ID                  uuid.UUID       `json:"id"`
	CityCode            string          `json:"city_code"`
	Name                string          `json:"name"`
	Slug                string          `json:"slug"`
	Active              bool            `json:"active"`
	TravelBufferMinutes int             `json:"travel_buffer_minutes"`
	Boundary            json.RawMessage `json:"boundary"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type AdminZoneInput struct {
	CityCode            string          `json:"city_code"`
	Name                string          `json:"name"`
	Slug                string          `json:"slug"`
	Active              *bool           `json:"active"`
	TravelBufferMinutes *int            `json:"travel_buffer_minutes"`
	Boundary            json.RawMessage `json:"boundary"`
}

type AdminZonePatch struct {
	Name                *string         `json:"name"`
	Active              *bool           `json:"active"`
	TravelBufferMinutes *int            `json:"travel_buffer_minutes"`
	Boundary            json.RawMessage `json:"boundary"`
}

// ---- categories, skills ----

type AdminCategory struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Family       string    `json:"family"`
	GenderRule   string    `json:"gender_rule"`
	ExtrasPolicy string    `json:"extras_policy"`
	ImageURL     *string   `json:"image_url"`
	SortOrder    int       `json:"sort_order"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type AdminCategoryInput struct {
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	Family       string  `json:"family"`
	GenderRule   *string `json:"gender_rule"`
	ExtrasPolicy *string `json:"extras_policy"`
	ImageURL     *string `json:"image_url"`
	SortOrder    *int    `json:"sort_order"`
	Active       *bool   `json:"active"`
}

type AdminCategoryPatch struct {
	Name         *string          `json:"name"`
	Description  *string          `json:"description"`
	Family       *string          `json:"family"`
	GenderRule   *string          `json:"gender_rule"`
	ExtrasPolicy *string          `json:"extras_policy"`
	ImageURL     Nullable[string] `json:"image_url"`
	SortOrder    *int             `json:"sort_order"`
	Active       *bool            `json:"active"`
}

type Skill struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SkillInput struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// ---- services, options, add-ons ----

type AdminService struct {
	ID              uuid.UUID `json:"id"`
	CategoryID      uuid.UUID `json:"category_id"`
	Slug            string    `json:"slug"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DurationMinutes int       `json:"duration_minutes"`
	RequiredSkill   string    `json:"required_skill"`
	Inclusions      []string  `json:"inclusions"`
	Exclusions      []string  `json:"exclusions"`
	ImageURL        *string   `json:"image_url"`
	CrewSize        int       `json:"crew_size"`
	MinBeforePhotos int       `json:"min_before_photos"`
	MinAfterPhotos  int       `json:"min_after_photos"`
	ReworkDays      int       `json:"rework_days"`
	SortOrder       int       `json:"sort_order"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AdminServiceInput struct {
	CategoryID      *uuid.UUID `json:"category_id"`
	Slug            string     `json:"slug"`
	Name            string     `json:"name"`
	Description     *string    `json:"description"`
	DurationMinutes int        `json:"duration_minutes"`
	RequiredSkill   string     `json:"required_skill"`
	Inclusions      []string   `json:"inclusions"`
	Exclusions      []string   `json:"exclusions"`
	ImageURL        *string    `json:"image_url"`
	CrewSize        *int       `json:"crew_size"`
	MinBeforePhotos *int       `json:"min_before_photos"`
	MinAfterPhotos  *int       `json:"min_after_photos"`
	ReworkDays      *int       `json:"rework_days"`
	SortOrder       *int       `json:"sort_order"`
	Active          *bool      `json:"active"`
}

type AdminServicePatch struct {
	Name            *string          `json:"name"`
	Description     *string          `json:"description"`
	DurationMinutes *int             `json:"duration_minutes"`
	RequiredSkill   *string          `json:"required_skill"`
	Inclusions      *[]string        `json:"inclusions"`
	Exclusions      *[]string        `json:"exclusions"`
	ImageURL        Nullable[string] `json:"image_url"`
	MinBeforePhotos *int             `json:"min_before_photos"`
	MinAfterPhotos  *int             `json:"min_after_photos"`
	ReworkDays      *int             `json:"rework_days"`
	SortOrder       *int             `json:"sort_order"`
	Active          *bool            `json:"active"`
}

type AdminOption struct {
	ID              uuid.UUID `json:"id"`
	ServiceID       uuid.UUID `json:"service_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DurationMinutes int       `json:"duration_minutes"`
	MaxQuantity     int       `json:"max_quantity"`
	IsDefault       bool      `json:"is_default"`
	SortOrder       int       `json:"sort_order"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AdminOptionInput struct {
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	DurationMinutes int     `json:"duration_minutes"`
	MaxQuantity     *int    `json:"max_quantity"`
	IsDefault       *bool   `json:"is_default"`
	SortOrder       *int    `json:"sort_order"`
	Active          *bool   `json:"active"`
}

type AdminOptionPatch struct {
	Name            *string `json:"name"`
	Description     *string `json:"description"`
	DurationMinutes *int    `json:"duration_minutes"`
	MaxQuantity     *int    `json:"max_quantity"`
	IsDefault       *bool   `json:"is_default"`
	SortOrder       *int    `json:"sort_order"`
	Active          *bool   `json:"active"`
}

type AdminAddonGroup struct {
	ID         uuid.UUID `json:"id"`
	ServiceID  uuid.UUID `json:"service_id"`
	Name       string    `json:"name"`
	MinSelect  int       `json:"min_select"`
	MaxSelect  int       `json:"max_select"`
	IsRequired bool      `json:"is_required"`
	SortOrder  int       `json:"sort_order"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AdminAddonGroupInput struct {
	Name       string `json:"name"`
	MinSelect  *int   `json:"min_select"`
	MaxSelect  int    `json:"max_select"`
	IsRequired *bool  `json:"is_required"`
	SortOrder  *int   `json:"sort_order"`
	Active     *bool  `json:"active"`
}

type AdminAddonGroupPatch struct {
	Name       *string `json:"name"`
	MinSelect  *int    `json:"min_select"`
	MaxSelect  *int    `json:"max_select"`
	IsRequired *bool   `json:"is_required"`
	SortOrder  *int    `json:"sort_order"`
	Active     *bool   `json:"active"`
}

type AdminAddon struct {
	ID                   uuid.UUID `json:"id"`
	GroupID              uuid.UUID `json:"group_id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	ExtraDurationMinutes int       `json:"extra_duration_minutes"`
	SortOrder            int       `json:"sort_order"`
	Active               bool      `json:"active"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type AdminAddonInput struct {
	Name                 string  `json:"name"`
	Description          *string `json:"description"`
	ExtraDurationMinutes *int    `json:"extra_duration_minutes"`
	SortOrder            *int    `json:"sort_order"`
	Active               *bool   `json:"active"`
}

type AdminAddonPatch struct {
	Name                 *string `json:"name"`
	Description          *string `json:"description"`
	ExtraDurationMinutes *int    `json:"extra_duration_minutes"`
	SortOrder            *int    `json:"sort_order"`
	Active               *bool   `json:"active"`
}

// AdminAddonGroupTree is a group with its add-ons (service tree).
type AdminAddonGroupTree struct {
	AdminAddonGroup
	Addons []AdminAddon `json:"addons"`
}

// AdminServiceTree is GET /internal/admin/services/{id}.
type AdminServiceTree struct {
	Service     AdminService          `json:"service"`
	Options     []AdminOption         `json:"options"`
	AddonGroups []AdminAddonGroupTree `json:"addon_groups"`
}

// ---- prices, rate cards ----

type AdminPrice struct {
	ID            uuid.UUID  `json:"id"`
	CityCode      string     `json:"city_code"`
	ItemKind      string     `json:"item_kind"`
	ItemID        uuid.UUID  `json:"item_id"`
	PricePaise    int64      `json:"price_paise"`
	MRPPaise      *int64     `json:"mrp_paise"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
	CreatedBy     *uuid.UUID `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
}

type AdminPriceInput struct {
	CityCode      string     `json:"city_code"`
	ItemKind      string     `json:"item_kind"`
	ItemID        *uuid.UUID `json:"item_id"`
	PricePaise    int64      `json:"price_paise"`
	MRPPaise      *int64     `json:"mrp_paise"`
	EffectiveFrom *time.Time `json:"effective_from"`
}

type AdminRateCard struct {
	ID          uuid.UUID `json:"id"`
	CityCode    string    `json:"city_code"`
	CategoryID  uuid.UUID `json:"category_id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Unit        string    `json:"unit"`
	PricePaise  int64     `json:"price_paise"`
	MaxQuantity int       `json:"max_quantity"`
	IsPart      bool      `json:"is_part"`
	SortOrder   int       `json:"sort_order"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AdminRateCardInput struct {
	CityCode    string     `json:"city_code"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	Unit        string     `json:"unit"`
	PricePaise  int64      `json:"price_paise"`
	MaxQuantity *int       `json:"max_quantity"`
	IsPart      *bool      `json:"is_part"`
	SortOrder   *int       `json:"sort_order"`
	Active      *bool      `json:"active"`
}

type AdminRateCardPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Unit        *string `json:"unit"`
	PricePaise  *int64  `json:"price_paise"`
	MaxQuantity *int    `json:"max_quantity"`
	IsPart      *bool   `json:"is_part"`
	SortOrder   *int    `json:"sort_order"`
	Active      *bool   `json:"active"`
}

// ---- config ----

type AdminSlotConfig struct {
	ID              uuid.UUID  `json:"id"`
	CityCode        string     `json:"city_code"`
	CategoryID      *uuid.UUID `json:"category_id"`
	OpenTime        string     `json:"open_time"`
	CloseTime       string     `json:"close_time"`
	SlotStepMinutes int        `json:"slot_step_minutes"`
	MinLeadMinutes  int        `json:"min_lead_minutes"`
	HorizonDays     int        `json:"horizon_days"`
	HoldMinutes     int        `json:"hold_minutes"`
	Active          bool       `json:"active"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type AdminSlotConfigInput struct {
	CityCode        string     `json:"city_code"`
	CategoryID      *uuid.UUID `json:"category_id"`
	OpenTime        string     `json:"open_time"`
	CloseTime       string     `json:"close_time"`
	SlotStepMinutes *int       `json:"slot_step_minutes"`
	MinLeadMinutes  *int       `json:"min_lead_minutes"`
	HorizonDays     *int       `json:"horizon_days"`
	HoldMinutes     *int       `json:"hold_minutes"`
	Active          *bool      `json:"active"`
}

type AdminSlotConfigPatch struct {
	OpenTime        *string `json:"open_time"`
	CloseTime       *string `json:"close_time"`
	SlotStepMinutes *int    `json:"slot_step_minutes"`
	MinLeadMinutes  *int    `json:"min_lead_minutes"`
	HorizonDays     *int    `json:"horizon_days"`
	HoldMinutes     *int    `json:"hold_minutes"`
	Active          *bool   `json:"active"`
}

type AdminCancellationRule struct {
	ID              uuid.UUID  `json:"id"`
	CityCode        string     `json:"city_code"`
	CategoryID      *uuid.UUID `json:"category_id"`
	Stage           string     `json:"stage"`
	MinutesBeforeLT *int       `json:"minutes_before_lt"`
	FeePaise        int64      `json:"fee_paise"`
	Allowed         bool       `json:"allowed"`
	SortOrder       int        `json:"sort_order"`
	Active          bool       `json:"active"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type AdminCancellationRuleInput struct {
	CityCode        string     `json:"city_code"`
	CategoryID      *uuid.UUID `json:"category_id"`
	Stage           string     `json:"stage"`
	MinutesBeforeLT *int       `json:"minutes_before_lt"`
	FeePaise        int64      `json:"fee_paise"`
	Allowed         *bool      `json:"allowed"`
	SortOrder       *int       `json:"sort_order"`
	Active          *bool      `json:"active"`
}

type AdminCancellationRulePatch struct {
	MinutesBeforeLT Nullable[int] `json:"minutes_before_lt"`
	FeePaise        *int64        `json:"fee_paise"`
	Allowed         *bool         `json:"allowed"`
	SortOrder       *int          `json:"sort_order"`
	Active          *bool         `json:"active"`
}

type AdminCommissionRule struct {
	ID            uuid.UUID  `json:"id"`
	CityCode      string     `json:"city_code"`
	CategoryID    *uuid.UUID `json:"category_id"`
	CommissionBPS int        `json:"commission_bps"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
	Active        bool       `json:"active"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type AdminCommissionRuleInput struct {
	CityCode      string     `json:"city_code"`
	CategoryID    *uuid.UUID `json:"category_id"`
	CommissionBPS *int       `json:"commission_bps"`
	EffectiveFrom *time.Time `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
	Active        *bool      `json:"active"`
}

type AdminCommissionRulePatch struct {
	CommissionBPS *int                `json:"commission_bps"`
	EffectiveTo   Nullable[time.Time] `json:"effective_to"`
	Active        *bool               `json:"active"`
}

// AuditLog is one doorstep.admin_audit_log row.
type AuditLog struct {
	ID          int64           `json:"id"`
	ActorUserID uuid.UUID       `json:"actor_user_id"`
	Permission  string          `json:"permission"`
	Action      string          `json:"action"`
	Entity      string          `json:"entity"`
	EntityID    string          `json:"entity_id"`
	Details     json.RawMessage `json:"details"`
	CreatedAt   time.Time       `json:"created_at"`
}
