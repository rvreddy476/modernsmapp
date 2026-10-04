// Package model holds the wire shapes of contracts/doorstep/openapi.yaml.
// Optional values are pointers WITHOUT omitempty: the contract emits explicit
// nulls so strict decoders on web and Android see every key.
package model

import (
	"time"

	"github.com/google/uuid"
)

// CityRef is the city a customer payload is about.
type CityRef struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// CategorySummary is one catalogue tile.
type CategorySummary struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Family       string    `json:"family"`
	GenderRule   string    `json:"gender_rule"`
	ImageURL     *string   `json:"image_url"`
	SortOrder    int       `json:"sort_order"`
	ServiceCount int       `json:"service_count"`
	// StartingPricePaise is the lowest approved professional price of the
	// category's services in the city now; null when no professional
	// offers any yet.
	StartingPricePaise *int64 `json:"starting_price_paise"`
}

// Catalogue is GET /v1/doorstep/catalogue.
type Catalogue struct {
	City       CityRef           `json:"city"`
	Categories []CategorySummary `json:"categories"`
}

// ServiceSummary is one service in a category page.
type ServiceSummary struct {
	ID              uuid.UUID `json:"id"`
	CategoryID      uuid.UUID `json:"category_id"`
	Slug            string    `json:"slug"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DurationMinutes int       `json:"duration_minutes"`
	ImageURL        *string   `json:"image_url"`
	// StartingPricePaise: the lowest approved professional price of the
	// service's options in the city now (null: no professional yet).
	StartingPricePaise *int64 `json:"starting_price_paise"`
	// SuggestedPricePaise: the lowest city suggested price of its options
	// (informational; never charged).
	SuggestedPricePaise *int64 `json:"suggested_price_paise"`
}

// CategoryPage is GET /v1/doorstep/categories/{slug}.
type CategoryPage struct {
	City     CityRef          `json:"city"`
	Category CategorySummary  `json:"category"`
	Services []ServiceSummary `json:"services"`
}

// CategoryBrief is the category inside a service detail.
type CategoryBrief struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Family       string    `json:"family"`
	GenderRule   string    `json:"gender_rule"`
	ExtrasPolicy string    `json:"extras_policy"`
}

// ServiceOption is a priced option of a service.
type ServiceOption struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DurationMinutes int       `json:"duration_minutes"`
	MaxQuantity     int       `json:"max_quantity"`
	// Unit: per_job, per_hour or per_month; quantity counts units.
	Unit      string `json:"unit"`
	IsDefault bool   `json:"is_default"`
	// SuggestedPricePaise and MRPPaise: the city's suggested price
	// (informational; prices come from professionals).
	SuggestedPricePaise *int64 `json:"suggested_price_paise"`
	MRPPaise            *int64 `json:"mrp_paise"`
	// FromPricePaise: the lowest approved professional price now.
	FromPricePaise *int64 `json:"from_price_paise"`
}

// Addon is a priced add-on.
type Addon struct {
	ID                   uuid.UUID `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	ExtraDurationMinutes int       `json:"extra_duration_minutes"`
	SuggestedPricePaise  *int64    `json:"suggested_price_paise"`
	FromPricePaise       *int64    `json:"from_price_paise"`
}

// AddonGroup is an add-on group with its selection rule.
type AddonGroup struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	MinSelect  int       `json:"min_select"`
	MaxSelect  int       `json:"max_select"`
	IsRequired bool      `json:"is_required"`
	Addons     []Addon   `json:"addons"`
}

// ServiceDetail is the service inside GET /v1/doorstep/services/{id}.
type ServiceDetail struct {
	ID              uuid.UUID       `json:"id"`
	Category        CategoryBrief   `json:"category"`
	Slug            string          `json:"slug"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	DurationMinutes int             `json:"duration_minutes"`
	Inclusions      []string        `json:"inclusions"`
	Exclusions      []string        `json:"exclusions"`
	ImageURL        *string         `json:"image_url"`
	CrewSize        int             `json:"crew_size"`
	ReworkDays      int             `json:"rework_days"`
	MinBeforePhotos int             `json:"min_before_photos"`
	MinAfterPhotos  int             `json:"min_after_photos"`
	Options         []ServiceOption `json:"options"`
	AddonGroups     []AddonGroup    `json:"addon_groups"`
}

// ServicePage is GET /v1/doorstep/services/{id}.
type ServicePage struct {
	City    CityRef       `json:"city"`
	Service ServiceDetail `json:"service"`
}

// ZoneRef names a zone.
type ZoneRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Serviceability is POST /v1/doorstep/serviceability.
type Serviceability struct {
	Serviceable bool     `json:"serviceable"`
	City        *CityRef `json:"city"`
	Zone        *ZoneRef `json:"zone"`
	Reason      *string  `json:"reason"`
}

// QuoteLine is one priced line of a quote (and later of a booking).
type QuoteLine struct {
	Kind           string    `json:"kind"`
	RefID          uuid.UUID `json:"ref_id"`
	PriceID        uuid.UUID `json:"price_id"`
	Name           string    `json:"name"`
	Unit           string    `json:"unit"`
	Quantity       int       `json:"quantity"`
	UnitPricePaise int64     `json:"unit_price_paise"`
	LineTotalPaise int64     `json:"line_total_paise"`
	TaxablePaise   int64     `json:"taxable_paise"`
	TaxPaise       int64     `json:"tax_paise"`
	TaxRateBPS     int       `json:"tax_rate_bps"`
	GSTCategory    string    `json:"gst_category"`
	SAC            string    `json:"sac"`
}

// Quote statuses.
const (
	QuoteOpen     = "open"
	QuoteExpired  = "expired"
	QuoteConsumed = "consumed"
)

// Quote is POST/GET /v1/doorstep/quotes.
type Quote struct {
	ID               uuid.UUID   `json:"id"`
	Status           string      `json:"status"`
	ServiceID        uuid.UUID   `json:"service_id"`
	OptionID         uuid.UUID   `json:"option_id"`
	Quantity         int         `json:"quantity"`
	ProID            uuid.UUID   `json:"pro_id"`
	CityCode         string      `json:"city_code"`
	ZoneID           uuid.UUID   `json:"zone_id"`
	Lines            []QuoteLine `json:"lines"`
	TotalPaise       int64       `json:"total_paise"`
	TaxablePaise     int64       `json:"taxable_paise"`
	TaxPaise         int64       `json:"tax_paise"`
	PricesIncludeTax bool        `json:"prices_include_tax"`
	TaxProvisional   bool        `json:"tax_provisional"`
	TaxNote          string      `json:"tax_note"`
	DurationMinutes  int         `json:"duration_minutes"`
	ExpiresAt        time.Time   `json:"expires_at"`
	CreatedAt        time.Time   `json:"created_at"`
}

// QuoteRequest is the POST /v1/doorstep/quotes body.
type QuoteRequest struct {
	ServiceID *uuid.UUID          `json:"service_id"`
	ProID     *uuid.UUID          `json:"pro_id"`
	OptionID  *uuid.UUID          `json:"option_id"`
	Quantity  *int                `json:"quantity"`
	Addons    []QuoteAddonRequest `json:"addons"`
	Lat       *float64            `json:"lat"`
	Lng       *float64            `json:"lng"`
}

// QuoteAddonRequest selects one add-on.
type QuoteAddonRequest struct {
	AddonID *uuid.UUID `json:"addon_id"`
}

// ServiceabilityRequest is the POST /v1/doorstep/serviceability body.
type ServiceabilityRequest struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
}
