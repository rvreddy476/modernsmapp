package model

import (
	"time"

	"github.com/google/uuid"
)

// TaxRegistration is admin-only; a GSTIN contains an embedded PAN.
type TaxRegistration struct {
	ProID     uuid.UUID `json:"pro_id"`
	GSTIN     *string   `json:"gstin"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---- the visit (A5) ----

// PhotoInput is POST /pro/jobs/{id}/photos: a media-service image the
// professional uploaded (workspace and kit only, never the customer).
type PhotoInput struct {
	Phase   *string  `json:"phase"`
	MediaID *string  `json:"media_id"`
	Lat     *float64 `json:"lat"`
	Lng     *float64 `json:"lng"`
}

// OTPInput is the start/complete body: the customer's four digits.
type OTPInput struct {
	OTP *string `json:"otp"`
}

// ExtraInput is POST /pro/jobs/{id}/extras: one rate-card item or one of
// the service's catalogue add-ons, never free text.
type ExtraInput struct {
	RateCardID      *uuid.UUID `json:"rate_card_id"`
	AddonID         *uuid.UUID `json:"addon_id"`
	Quantity        *int       `json:"quantity"`
	EvidenceMediaID *string    `json:"evidence_media_id"`
}

// ExtraOption is one extra a professional may propose on a job: a rate-card
// item of the job's category in its city (rate_card policy), or an add-on
// of the job's service priced with the professional's approved add-on price
// (B1; salon offers add-ons only).
type ExtraOption struct {
	Kind           string     `json:"kind"`
	RateCardID     *uuid.UUID `json:"rate_card_id"`
	AddonID        *uuid.UUID `json:"addon_id"`
	Name           string     `json:"name"`
	Description    *string    `json:"description"`
	UnitPricePaise int64      `json:"unit_price_paise"`
	MaxQuantity    int        `json:"max_quantity"`
}

// ExtrasBill is a visit-extras bill.
type ExtrasBill struct {
	ID           uuid.UUID  `json:"id"`
	BookingID    uuid.UUID  `json:"booking_id"`
	AmountPaise  int64      `json:"amount_paise"`
	TaxablePaise int64      `json:"taxable_paise"`
	TaxPaise     int64      `json:"tax_paise"`
	Status       string     `json:"status"`
	DueAt        *time.Time `json:"due_at"`
	PaidAt       *time.Time `json:"paid_at"`
}

// Outstanding is GET /me/outstanding: unpaid extras that block a new
// booking until paid.
type Outstanding struct {
	TotalPaise int64        `json:"total_paise"`
	Bills      []ExtrasBill `json:"bills"`
}

// RatingInput rates the other side once the job is done.
type RatingInput struct {
	Stars   *int     `json:"stars"`
	Tags    []string `json:"tags"`
	Comment *string  `json:"comment"`
}

// Rating is one rating.
type Rating struct {
	ID        uuid.UUID `json:"id"`
	BookingID uuid.UUID `json:"booking_id"`
	RaterKind string    `json:"rater_kind"`
	Stars     int       `json:"stars"`
	Tags      []string  `json:"tags"`
	Comment   *string   `json:"comment"`
	Hidden    bool      `json:"hidden"`
	CreatedAt time.Time `json:"created_at"`
}

// ReworkInput asks for the job to be redone. With slot_start the zero-price
// child booking is made at once, with the same professional unless
// same_professional is false (then the customer picks one through the
// pro_unavailable flow: GET /bookings/{child}/professionals).
type ReworkInput struct {
	Reason           *string    `json:"reason"`
	MediaIDs         []string   `json:"media_ids"`
	SlotStart        *time.Time `json:"slot_start"`
	SameProfessional *bool      `json:"same_professional"`
}

// ReworkRequest is one rework request.
type ReworkRequest struct {
	ID             uuid.UUID  `json:"id"`
	BookingID      uuid.UUID  `json:"booking_id"`
	ChildBookingID *uuid.UUID `json:"child_booking_id"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	CreatedAt      time.Time  `json:"created_at"`
}

// SOSInput is an SOS or an "unsafe, leaving" exit.
type SOSInput struct {
	Lat  *float64 `json:"lat"`
	Lng  *float64 `json:"lng"`
	Note *string  `json:"note"`
}

// Incident is a safety incident.
type Incident struct {
	ID               uuid.UUID  `json:"id"`
	BookingID        *uuid.UUID `json:"booking_id"`
	RaisedByKind     string     `json:"raised_by_kind"`
	Kind             string     `json:"kind"`
	Severity         string     `json:"severity"`
	Status           string     `json:"status"`
	Description      *string    `json:"description"`
	ProAutoSuspended bool       `json:"pro_auto_suspended"`
	CreatedAt        time.Time  `json:"created_at"`
}

// ShareToken is a share-status link (the token is shown once; only its
// hash is stored).
type ShareToken struct {
	Token     string    `json:"token"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SharedBookingView is what a share link shows: never the address.
type SharedBookingView struct {
	Status                string    `json:"status"`
	Locality              string    `json:"locality"`
	ProfessionalFirstName *string   `json:"professional_first_name"`
	SlotStart             time.Time `json:"slot_start"`
	EtaMinutes            *int      `json:"eta_minutes"`
}

// TrustedContact is the customer's trusted contact, masked.
type TrustedContact struct {
	Name        string    `json:"name"`
	PhoneMasked string    `json:"phone_masked"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TrustedContactInput is PUT /trusted-contact.
type TrustedContactInput struct {
	Name  *string `json:"name"`
	Phone *string `json:"phone"`
}

// MessageInput is one chat message.
type MessageInput struct {
	Body *string `json:"body"`
}

// Message is one chat message.
type Message struct {
	ID         uuid.UUID  `json:"id"`
	BookingID  uuid.UUID  `json:"booking_id"`
	SenderKind string     `json:"sender_kind"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

// MessagePage is a page of chat, oldest first; open says whether a message
// may be sent now (acceptance to completion + 2 h).
type MessagePage struct {
	Items      []Message `json:"items"`
	NextCursor *string   `json:"next_cursor"`
	Open       bool      `json:"open"`
}

// TicketInput opens a support ticket.
type TicketInput struct {
	BookingID *uuid.UUID `json:"booking_id"`
	Category  *string    `json:"category"`
	Subject   *string    `json:"subject"`
	Body      *string    `json:"body"`
}

// Ticket is a support ticket.
type Ticket struct {
	ID        uuid.UUID  `json:"id"`
	BookingID *uuid.UUID `json:"booking_id"`
	Category  string     `json:"category"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// EarningLine is one computed earning line (payouts are OFF).
type EarningLine struct {
	ID          uuid.UUID  `json:"id"`
	BookingID   *uuid.UUID `json:"booking_id"`
	Kind        string     `json:"kind"`
	AmountPaise int64      `json:"amount_paise"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Earnings is GET /pro/earnings.
type Earnings struct {
	TotalPaise int64         `json:"total_paise"`
	Lines      []EarningLine `json:"lines"`
}

// Invoice is the split recomputed at completion with the actual
// professional (bookings.invoice_snapshot).
type Invoice struct {
	ComputedAt    time.Time     `json:"computed_at"`
	ProRegistered bool          `json:"pro_registered"`
	Lines         []InvoiceLine `json:"lines"`
	TotalPaise    int64         `json:"total_paise"`
	TaxablePaise  int64         `json:"taxable_paise"`
	TaxPaise      int64         `json:"tax_paise"`
	Provisional   bool          `json:"provisional"`
	Note          string        `json:"note"`
}

// InvoiceLine is one invoiced charge (the booking's lines, then the billed
// extras).
type InvoiceLine struct {
	Ref          string `json:"ref"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	GSTCategory  string `json:"gst_category"`
	SAC          string `json:"sac"`
	RateBPS      int    `json:"rate_bps"`
	GrossPaise   int64  `json:"gross_paise"`
	TaxablePaise int64  `json:"taxable_paise"`
	TaxPaise     int64  `json:"tax_paise"`
}
