package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ---- presence (A4) ----

// LocationInput is POST /pro/duty/on (optional) and POST /pro/location.
type LocationInput struct {
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	AccuracyM *float64 `json:"accuracy_m"`
}

// DutyState is the professional's duty: since is when they went on duty.
type DutyState struct {
	OnDuty bool       `json:"on_duty"`
	Since  *time.Time `json:"since"`
}

// ProZone is a zone a professional may pick for their service area.
type ProZone struct {
	ID       uuid.UUID       `json:"id"`
	CityCode string          `json:"city_code"`
	Name     string          `json:"name"`
	Slug     string          `json:"slug"`
	Boundary json.RawMessage `json:"boundary"`
}

// ---- offers and jobs (A4) ----

// Offer is a job offered to a professional: the locality only, never the
// address. status is open while it can be accepted.
type Offer struct {
	ID                   uuid.UUID `json:"id"`
	BookingID            uuid.UUID `json:"booking_id"`
	Status               string    `json:"status"`
	ServiceName          string    `json:"service_name"`
	CategorySlug         string    `json:"category_slug"`
	Locality             string    `json:"locality"`
	DistanceM            int       `json:"distance_m"`
	SlotStart            time.Time `json:"slot_start"`
	SlotEnd              time.Time `json:"slot_end"`
	EarningEstimatePaise int64     `json:"earning_estimate_paise"`
	ExpiresAt            time.Time `json:"expires_at"`
}

// DeclineInput is POST /pro/offers/{id}/decline.
type DeclineInput struct {
	Reason *string `json:"reason"`
}

// PhotoCounts counts visit photos per phase.
type PhotoCounts struct {
	Before  int `json:"before"`
	After   int `json:"after"`
	KitSeal int `json:"kit_seal"`
}

// ProJob is one of a professional's jobs. The address (and chat) only from
// acceptance to completion + 2 h; the locality always.
type ProJob struct {
	BookingID            uuid.UUID   `json:"booking_id"`
	Status               string      `json:"status"`
	ServiceName          string      `json:"service_name"`
	CategorySlug         string      `json:"category_slug"`
	SlotStart            time.Time   `json:"slot_start"`
	SlotEnd              time.Time   `json:"slot_end"`
	Items                []QuoteLine `json:"items"`
	Locality             string      `json:"locality"`
	Address              *Address    `json:"address"`
	CustomerFirstName    *string     `json:"customer_first_name"`
	ChatOpen             bool        `json:"chat_open"`
	PhotosRequired       PhotoCounts `json:"photos_required"`
	PhotosUploaded       PhotoCounts `json:"photos_uploaded"`
	ArrivedAt            *time.Time  `json:"arrived_at"`
	Finished             bool        `json:"finished"`
	EarningEstimatePaise int64       `json:"earning_estimate_paise"`
}

// ProJobPage is GET /pro/jobs.
type ProJobPage struct {
	Items      []ProJob `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

// ProCancelInput is POST /pro/jobs/{id}/cancel.
type ProCancelInput struct {
	Reason *string `json:"reason"`
}

// ---- realtime (A4) ----

// RealtimeTokenRequest is the customer's POST /realtime/token body.
type RealtimeTokenRequest struct {
	BookingID *uuid.UUID `json:"booking_id"`
}

// RealtimeToken is a shared/realtime topic token.
type RealtimeToken struct {
	Token     string    `json:"token"`
	Topics    []string  `json:"topics"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ---- admin (A4) ----

// AdminRedispatchInput is POST /internal/admin/bookings/{id}/redispatch:
// dispatch re-runs excluding the current professional and anyone listed;
// ops never pick the professional.
type AdminRedispatchInput struct {
	Reason        *string     `json:"reason"`
	ExcludeProIDs []uuid.UUID `json:"exclude_pro_ids"`
}
