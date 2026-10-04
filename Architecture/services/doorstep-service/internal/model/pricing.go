package model

import (
	"time"

	"github.com/google/uuid"
)

// B1 (4 Oct 2026): professionals price their own services, an admin reviews
// every price, the customer picks a professional, and a booking whose
// professional is gone waits in pro_unavailable for the customer's choice.

// ---- the professional's prices (/pro) ----

// ProPrice is one of a professional's price rows (GST-inclusive paise).
// status: pending | approved | rejected | withdrawn. An approved row is live
// from effective_from until effective_to (null: still live).
type ProPrice struct {
	ID            uuid.UUID  `json:"id"`
	ServiceID     uuid.UUID  `json:"service_id"`
	ItemKind      string     `json:"item_kind"`
	ItemID        uuid.UUID  `json:"item_id"`
	Unit          string     `json:"unit"`
	PricePaise    int64      `json:"price_paise"`
	Status        string     `json:"status"`
	EffectiveFrom *time.Time `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
	SubmittedAt   time.Time  `json:"submitted_at"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	Reason        *string    `json:"reason"`
}

// ProPriceItem is one option or add-on of a service the professional may
// price: the live approved price, a submission waiting for review, and the
// latest rejection newer than both (so the professional sees why).
type ProPriceItem struct {
	ItemKind            string    `json:"item_kind"`
	ItemID              uuid.UUID `json:"item_id"`
	Name                string    `json:"name"`
	Unit                string    `json:"unit"`
	MaxQuantity         int       `json:"max_quantity"`
	SuggestedPricePaise *int64    `json:"suggested_price_paise"`
	Approved            *ProPrice `json:"approved"`
	Pending             *ProPrice `json:"pending"`
	Rejected            *ProPrice `json:"rejected"`
}

// ProServicePricing is one service the professional may price (its skill
// declared and not revoked). bookable: customers can book this professional
// for it now (approved professional, skill verified, an approved option
// price, a family the tax computer prices).
type ProServicePricing struct {
	ServiceID    uuid.UUID      `json:"service_id"`
	ServiceName  string         `json:"service_name"`
	CategorySlug string         `json:"category_slug"`
	CategoryName string         `json:"category_name"`
	Family       string         `json:"family"`
	SkillCode    string         `json:"skill_code"`
	SkillStatus  string         `json:"skill_status"`
	SameDay      bool           `json:"same_day"`
	Bookable     bool           `json:"bookable"`
	Items        []ProPriceItem `json:"items"`
}

// ProPriceInput is POST /pro/me/prices.
type ProPriceInput struct {
	ServiceID  *uuid.UUID `json:"service_id"`
	ItemKind   *string    `json:"item_kind"`
	ItemID     *uuid.UUID `json:"item_id"`
	PricePaise *int64     `json:"price_paise"`
}

// SameDayInput is PUT /pro/me/services/{id}/same-day.
type SameDayInput struct {
	Enabled *bool `json:"enabled"`
}

// SameDaySetting is the professional's same-day opt-in for one service.
type SameDaySetting struct {
	ServiceID uuid.UUID `json:"service_id"`
	SameDay   bool      `json:"same_day"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---- admin price review ----

// AdminProPrice is a price row in the admin review queue, with what the
// reviewer compares it to: the city's suggested price and the professional's
// current approved price for the item.
type AdminProPrice struct {
	ProPrice
	ProID                uuid.UUID  `json:"pro_id"`
	ProDisplayName       string     `json:"pro_display_name"`
	ProStatus            string     `json:"pro_status"`
	CityCode             string     `json:"city_code"`
	ServiceName          string     `json:"service_name"`
	CategorySlug         string     `json:"category_slug"`
	ItemName             string     `json:"item_name"`
	SuggestedPricePaise  *int64     `json:"suggested_price_paise"`
	CurrentApprovedPaise *int64     `json:"current_approved_paise"`
	ReviewedBy           *uuid.UUID `json:"reviewed_by"`
}

// PriceDecisionInput is the admin approve/reject body (reason required to
// reject).
type PriceDecisionInput struct {
	Reason *string `json:"reason"`
}

// ---- the customer's professionals list ----

// PriceLine is one line of a professional's price for the selection.
type PriceLine struct {
	Kind           string    `json:"kind"`
	RefID          uuid.UUID `json:"ref_id"`
	Name           string    `json:"name"`
	Unit           string    `json:"unit"`
	Quantity       int       `json:"quantity"`
	UnitPricePaise int64     `json:"unit_price_paise"`
	LineTotalPaise int64     `json:"line_total_paise"`
}

// CardPrice is a professional's GST-inclusive price for the selection.
type CardPrice struct {
	TotalPaise   int64       `json:"total_paise"`
	TaxablePaise int64       `json:"taxable_paise"`
	TaxPaise     int64       `json:"tax_paise"`
	Lines        []PriceLine `json:"lines"`
}

// NextSlot is a start the professional is free for the job.
type NextSlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Distance bands (never an exact distance or location).
const (
	DistanceUnder2KM = "under_2_km"
	Distance2To5KM   = "2_to_5_km"
	Distance5To10KM  = "5_to_10_km"
	DistanceOver10KM = "over_10_km"
)

// ProfessionalCard is one professional the customer may pick: first name,
// photo, rating, a distance band (never exact), the price for the selection,
// and the next free starts (scheduled) or the ETA (asap).
type ProfessionalCard struct {
	ProID         uuid.UUID  `json:"pro_id"`
	FirstName     string     `json:"first_name"`
	PhotoMediaID  *string    `json:"photo_media_id"`
	RatingAvg     *float64   `json:"rating_avg"`
	RatingCount   int        `json:"rating_count"`
	JobsCompleted int        `json:"jobs_completed"`
	DistanceBand  string     `json:"distance_band"`
	Price         CardPrice  `json:"price"`
	NextSlots     []NextSlot `json:"next_slots"`
	EtaMinutes    *int       `json:"eta_minutes"`
	SameDay       bool       `json:"same_day"`
	// DifferencePaise is the price against the booking's current total
	// (GET /bookings/{id}/professionals only; null on the service list).
	DifferencePaise *int64 `json:"difference_paise"`
}

// ProfessionalList is GET /services/{id}/professionals and GET
// /bookings/{id}/professionals. mode asap with nobody on duty in range says
// so (no_professional, reason) and lists scheduled_alternatives: the
// professionals' next free starts for the same selection and address.
type ProfessionalList struct {
	ServiceID             uuid.UUID          `json:"service_id"`
	OptionID              uuid.UUID          `json:"option_id"`
	Quantity              int                `json:"quantity"`
	AddonIDs              []uuid.UUID        `json:"addon_ids"`
	BookingID             *uuid.UUID         `json:"booking_id"`
	Mode                  string             `json:"mode"`
	Date                  *string            `json:"date"`
	Sort                  string             `json:"sort"`
	Timezone              string             `json:"timezone"`
	Items                 []ProfessionalCard `json:"items"`
	NoProfessional        bool               `json:"no_professional"`
	NoProfessionalReason  *string            `json:"no_professional_reason"`
	ScheduledAlternatives []ProfessionalCard `json:"scheduled_alternatives"`
}

// ---- a change of professional (pro_unavailable) ----

// ProChange is one change of professional on a booking. status:
// pending_payment (a dearer professional is held until the difference is
// paid), applied, abandoned. difference_paise = new - previous (negative:
// refunded at once, refund_paise).
type ProChange struct {
	ID                 uuid.UUID      `json:"id"`
	Status             string         `json:"status"`
	ProID              uuid.UUID      `json:"pro_id"`
	ProFirstName       string         `json:"pro_first_name"`
	Asap               bool           `json:"asap"`
	SlotStart          time.Time      `json:"slot_start"`
	SlotEnd            time.Time      `json:"slot_end"`
	PreviousTotalPaise int64          `json:"previous_total_paise"`
	NewTotalPaise      int64          `json:"new_total_paise"`
	DifferencePaise    int64          `json:"difference_paise"`
	RefundPaise        int64          `json:"refund_paise"`
	HoldExpiresAt      *time.Time     `json:"hold_expires_at"`
	PaymentIntent      *PaymentIntent `json:"payment_intent"`
	CreatedAt          time.Time      `json:"created_at"`
}

// ProChangeRequest is POST /bookings/{id}/change-professional: the
// professional and exactly one of slot_start or asap=true.
type ProChangeRequest struct {
	ProID     *uuid.UUID `json:"pro_id"`
	SlotStart *time.Time `json:"slot_start"`
	Asap      *bool      `json:"asap"`
}

// ProChangeResult answers a change: the booking and the change (with the
// payment intent when the difference is to be paid).
type ProChangeResult struct {
	Booking Booking   `json:"booking"`
	Change  ProChange `json:"change"`
}
