package model

import (
	"time"

	"github.com/google/uuid"
)

// ---- addresses (A3) ----

// Address is a customer's saved address. line1/line2/landmark are opened
// from the sealed blob for the owner only.
type Address struct {
	ID        uuid.UUID `json:"id"`
	Label     string    `json:"label"`
	Line1     string    `json:"line1"`
	Line2     *string   `json:"line2"`
	Landmark  *string   `json:"landmark"`
	Locality  string    `json:"locality"`
	CityCode  string    `json:"city_code"`
	Pincode   string    `json:"pincode"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	ZoneID    uuid.UUID `json:"zone_id"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
}

// AddressInput is the POST/PATCH /addresses body. On PATCH an absent field
// keeps its value.
type AddressInput struct {
	Label     *string  `json:"label"`
	Line1     *string  `json:"line1"`
	Line2     *string  `json:"line2"`
	Landmark  *string  `json:"landmark"`
	Locality  *string  `json:"locality"`
	Pincode   *string  `json:"pincode"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	IsDefault *bool    `json:"is_default"`
}

// ---- slots ----

// Slot is one start on the grid.
type Slot struct {
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Available bool      `json:"available"`
}

// SlotDay is one date.
type SlotDay struct {
	Date  string `json:"date"`
	Slots []Slot `json:"slots"`
}

// SlotDays is GET /slots.
type SlotDays struct {
	Timezone string    `json:"timezone"`
	Days     []SlotDay `json:"days"`
}

// ---- bookings ----

// BookingCreateRequest is the POST /bookings body.
type BookingCreateRequest struct {
	QuoteID   *uuid.UUID `json:"quote_id"`
	AddressID *uuid.UUID `json:"address_id"`
	// Exactly one of SlotStart (scheduled) or Asap=true (same day, now).
	SlotStart        *time.Time `json:"slot_start"`
	Asap             *bool      `json:"asap"`
	RequireFemalePro *bool      `json:"require_female_pro"`
	Notes            *string    `json:"notes"`
}

// BookingProfessional is the professional a customer sees from acceptance
// on: first name, photo, rating; never a phone.
type BookingProfessional struct {
	FirstName     string   `json:"first_name"`
	PhotoMediaID  *string  `json:"photo_media_id"`
	RatingAvg     *float64 `json:"rating_avg"`
	JobsCompleted int      `json:"jobs_completed"`
}

// Photo is a visit photo (A5 writes them).
type Photo struct {
	ID        uuid.UUID `json:"id"`
	BookingID uuid.UUID `json:"booking_id"`
	Phase     string    `json:"phase"`
	MediaID   string    `json:"media_id"`
	CreatedAt time.Time `json:"created_at"`
}

// StatusStep is one entry of the customer's booking timeline.
type StatusStep struct {
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	CreatedAt  time.Time `json:"created_at"`
}

// Booking is the customer's booking. end_otp, photos and status_history
// close the A3 contract gaps: end_otp stays null until the visit lane sets
// it, photos stays empty until it uploads them.
type Booking struct {
	ID                   uuid.UUID            `json:"id"`
	Status               string               `json:"status"`
	ServiceID            uuid.UUID            `json:"service_id"`
	ServiceName          string               `json:"service_name"`
	CategorySlug         string               `json:"category_slug"`
	CityCode             string               `json:"city_code"`
	ZoneID               uuid.UUID            `json:"zone_id"`
	SlotStart            time.Time            `json:"slot_start"`
	SlotEnd              time.Time            `json:"slot_end"`
	DurationMinutes      int                  `json:"duration_minutes"`
	RequireFemalePro     bool                 `json:"require_female_pro"`
	Items                []QuoteLine          `json:"items"`
	TotalPaise           int64                `json:"total_paise"`
	TaxablePaise         int64                `json:"taxable_paise"`
	TaxPaise             int64                `json:"tax_paise"`
	PaidPaise            int64                `json:"paid_paise"`
	RefundedPaise        int64                `json:"refunded_paise"`
	CancellationFeePaise int64                `json:"cancellation_fee_paise"`
	ExtrasTotalPaise     int64                `json:"extras_total_paise"`
	OutstandingPaise     int64                `json:"outstanding_paise"`
	HoldExpiresAt        *time.Time           `json:"hold_expires_at"`
	Address              Address              `json:"address"`
	Professional         *BookingProfessional `json:"professional"`
	ParentBookingID      *uuid.UUID           `json:"parent_booking_id"`
	StartOTP             *string              `json:"start_otp"`
	EndOTP               *string              `json:"end_otp"`
	Photos               []Photo              `json:"photos"`
	StatusHistory        []StatusStep         `json:"status_history"`
	CanCancel            bool                 `json:"can_cancel"`
	CanReschedule        bool                 `json:"can_reschedule"`
	// B1. Asap: a same-day "as soon as possible" booking. ChoiceDeadline and
	// UnavailableCause are set while the booking is pro_unavailable (pick
	// another professional by then, or it is cancelled with a full refund).
	// PendingChange is a change of professional waiting for the difference
	// to be paid.
	Asap             bool       `json:"asap"`
	ChoiceDeadline   *time.Time `json:"choice_deadline"`
	UnavailableCause *string    `json:"unavailable_cause"`
	PendingChange    *ProChange `json:"pending_change"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// BookingSummary is one row of a bookings list.
type BookingSummary struct {
	ID           uuid.UUID `json:"id"`
	Status       string    `json:"status"`
	ServiceName  string    `json:"service_name"`
	CategorySlug string    `json:"category_slug"`
	SlotStart    time.Time `json:"slot_start"`
	SlotEnd      time.Time `json:"slot_end"`
	TotalPaise   int64     `json:"total_paise"`
	CreatedAt    time.Time `json:"created_at"`
}

// BookingPage is a page of bookings.
type BookingPage struct {
	Items      []BookingSummary `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

// PaymentIntent is a payment row as a client sees it. Checkout is
// payments-service's client session (provider, order_id, key_id,
// merchant_display_name), relayed unchanged; {} when payments attached none.
type PaymentIntent struct {
	PaymentID     uuid.UUID         `json:"payment_id"`
	ReferenceType string            `json:"reference_type"`
	ReferenceID   uuid.UUID         `json:"reference_id"`
	AmountPaise   int64             `json:"amount_paise"`
	Status        string            `json:"status"`
	Checkout      map[string]string `json:"checkout"`
}

// BookingCreated is POST /bookings.
type BookingCreated struct {
	Booking       Booking       `json:"booking"`
	PaymentIntent PaymentIntent `json:"payment_intent"`
}

// Refund is one refund of a booking.
type Refund struct {
	ID          uuid.UUID `json:"id"`
	PaymentID   uuid.UUID `json:"payment_id"`
	Cause       string    `json:"cause"`
	AmountPaise int64     `json:"amount_paise"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// BookingPayments is GET /bookings/{id}/payment: the only paid source.
type BookingPayments struct {
	Payments []PaymentIntent `json:"payments"`
	Refunds  []Refund        `json:"refunds"`
}

// CancelRequest is the POST /bookings/{id}/cancel body.
type CancelRequest struct {
	Reason *string `json:"reason"`
}

// CancelPreview is GET /bookings/{id}/cancel-preview.
type CancelPreview struct {
	Allowed     bool   `json:"allowed"`
	FeePaise    int64  `json:"fee_paise"`
	RefundPaise int64  `json:"refund_paise"`
	Rule        string `json:"rule"`
}

// RescheduleRequest is the POST /bookings/{id}/reschedule body.
type RescheduleRequest struct {
	SlotStart *time.Time `json:"slot_start"`
}

// ---- admin ----

// AdminCancelInput is the admin cancel body: a reason, and a fee only when
// ops decide the customer pays one (default: full refund).
type AdminCancelInput struct {
	Reason   *string `json:"reason"`
	FeePaise *int64  `json:"fee_paise"`
}

// AdminRefundInput is the admin refund body.
type AdminRefundInput struct {
	AmountPaise *int64  `json:"amount_paise"`
	Reason      *string `json:"reason"`
	Payment     *string `json:"payment"`
}

// HistoryEntry is one status change in the admin view.
type HistoryEntry struct {
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	ActorKind  string    `json:"actor_kind"`
	Reason     *string   `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// AssignmentView is one offer/assignment of a booking.
type AssignmentView struct {
	ID             uuid.UUID  `json:"id"`
	ProID          uuid.UUID  `json:"pro_id"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	OfferedAt      time.Time  `json:"offered_at"`
	OfferExpiresAt time.Time  `json:"offer_expires_at"`
	RespondedAt    *time.Time `json:"responded_at"`
}

// Extra is a proposed extra (A5 writes them; the admin view lists them).
type Extra struct {
	ID              uuid.UUID  `json:"id"`
	BookingID       uuid.UUID  `json:"booking_id"`
	Kind            string     `json:"kind"`
	RateCardID      *uuid.UUID `json:"rate_card_id"`
	AddonID         *uuid.UUID `json:"addon_id"`
	Name            string     `json:"name"`
	Quantity        int        `json:"quantity"`
	UnitPricePaise  int64      `json:"unit_price_paise"`
	TotalPaise      int64      `json:"total_paise"`
	Status          string     `json:"status"`
	EvidenceMediaID *string    `json:"evidence_media_id"`
	CreatedAt       time.Time  `json:"created_at"`
}

// AdminBookingDetail is the admin booking view. The OTPs are never in it.
type AdminBookingDetail struct {
	Booking         Booking          `json:"booking"`
	CustomerUserID  uuid.UUID        `json:"customer_user_id"`
	ReservedProID   *uuid.UUID       `json:"reserved_pro_id"`
	NeedsAttention  bool             `json:"needs_attention"`
	AttentionReason *string          `json:"attention_reason"`
	History         []HistoryEntry   `json:"history"`
	Assignments     []AssignmentView `json:"assignments"`
	Payments        []PaymentIntent  `json:"payments"`
	Refunds         []Refund         `json:"refunds"`
	Extras          []Extra          `json:"extras"`
	Photos          []Photo          `json:"photos"`
	// B1: the professionals the customer may not pick again for this booking
	// (they let it go, or ops excluded them) and every change of
	// professional, oldest first.
	ExcludedProIDs []uuid.UUID `json:"excluded_pro_ids"`
	ProChanges     []ProChange `json:"pro_changes"`
}

// AdminStats is the dashboard counts.
type AdminStats struct {
	BookingsToday            int   `json:"bookings_today"`
	BookingsInProgress       int   `json:"bookings_in_progress"`
	UnassignedWithin2h       int   `json:"unassigned_within_2h"`
	BookingsNeedingAttention int   `json:"bookings_needing_attention"`
	ProsApproved             int   `json:"pros_approved"`
	ProsPendingVerification  int   `json:"pros_pending_verification"`
	DocumentsPending         int   `json:"documents_pending"`
	IncidentsOpen            int   `json:"incidents_open"`
	GMVTodayPaise            int64 `json:"gmv_today_paise"`
	RefundsTodayPaise        int64 `json:"refunds_today_paise"`
	OutstandingPaise         int64 `json:"outstanding_paise"`
}
