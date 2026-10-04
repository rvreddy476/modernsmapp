// Package events builds doorstep.events envelopes (contracts/doorstep/
// asyncapi.yaml): {event_id, event_type, version, occurred_at, booking_id,
// customer_user_id, pro_user_id, data}, nulls explicit. The store enqueues
// them through the schema-local outbox in the same transaction as the state
// change. No address, phone, OTP or document number ever goes in data.
package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Professional event types (A2).
const (
	ProApplied          = "doorstep.pro.applied"
	ProStatusChanged    = "doorstep.pro.status_changed"
	ProDocumentReviewed = "doorstep.pro.document_reviewed"
)

// Envelope is the Kafka value.
type Envelope struct {
	EventID        uuid.UUID  `json:"event_id"`
	EventType      string     `json:"event_type"`
	Version        int        `json:"version"`
	OccurredAt     time.Time  `json:"occurred_at"`
	BookingID      *uuid.UUID `json:"booking_id"`
	CustomerUserID *uuid.UUID `json:"customer_user_id"`
	ProUserID      *uuid.UUID `json:"pro_user_id"`
	Data           any        `json:"data"`
}

// Pro builds a professional event: partition key is the pro's user id.
func Pro(eventType string, proUserID uuid.UUID, at time.Time, data any) (key string, payload []byte, err error) {
	uid := proUserID
	payload, err = json.Marshal(Envelope{EventID: uuid.New(), EventType: eventType, Version: 1, OccurredAt: at.UTC(),
		ProUserID: &uid, Data: data})
	return proUserID.String(), payload, err
}

// ProAppliedData is doorstep.pro.applied's data.
type ProAppliedData struct {
	ProID     uuid.UUID `json:"pro_id"`
	ProUserID uuid.UUID `json:"pro_user_id"`
	CityCode  string    `json:"city_code"`
}

// ProStatusChangedData is doorstep.pro.status_changed's data.
type ProStatusChangedData struct {
	ProID      uuid.UUID `json:"pro_id"`
	ProUserID  uuid.UUID `json:"pro_user_id"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Reason     *string   `json:"reason"`
}

// ProDocumentReviewedData is doorstep.pro.document_reviewed's data. A
// police certificate's decision is also the background check's: approved
// means the check is clear until issued_on + 12 months.
type ProDocumentReviewedData struct {
	ProID      uuid.UUID `json:"pro_id"`
	ProUserID  uuid.UUID `json:"pro_user_id"`
	DocumentID uuid.UUID `json:"document_id"`
	Kind       string    `json:"kind"`
	Decision   string    `json:"decision"`
}

// Booking event types (A3).
const (
	BookingCreated      = "doorstep.booking.created"
	BookingConfirmed    = "doorstep.booking.confirmed"
	BookingExpired      = "doorstep.booking.expired"
	BookingCancelled    = "doorstep.booking.cancelled"
	BookingRescheduled  = "doorstep.booking.rescheduled"
	BookingRefunded     = "doorstep.booking.refunded"
	BookingRefundFailed = "doorstep.booking.refund_failed"
	BookingAttention    = "doorstep.booking.payment_attention"
)

// BookingCore is every booking event's common data (asyncapi BookingCore).
// pro_user_id is the ACCEPTED professional only: a professional who was
// reserved but never offered the job is never told about it.
type BookingCore struct {
	BookingID       uuid.UUID  `json:"booking_id"`
	CustomerUserID  uuid.UUID  `json:"customer_user_id"`
	ProUserID       *uuid.UUID `json:"pro_user_id"`
	Status          string     `json:"status"`
	CityCode        string     `json:"city_code"`
	CategorySlug    string     `json:"category_slug"`
	ServiceID       uuid.UUID  `json:"service_id"`
	SlotStart       time.Time  `json:"slot_start"`
	SlotEnd         time.Time  `json:"slot_end"`
	ParentBookingID *uuid.UUID `json:"parent_booking_id"`
}

// Booking builds a booking event: partition key is the booking id.
func Booking(eventType string, core BookingCore, at time.Time, data any) (key string, payload []byte, err error) {
	id, cust := core.BookingID, core.CustomerUserID
	payload, err = json.Marshal(Envelope{EventID: uuid.New(), EventType: eventType, Version: 1, OccurredAt: at.UTC(),
		BookingID: &id, CustomerUserID: &cust, ProUserID: core.ProUserID, Data: data})
	return id.String(), payload, err
}

// BookingCreatedData is doorstep.booking.created's data.
type BookingCreatedData struct {
	BookingCore
	TotalPaise    int64     `json:"total_paise"`
	HoldExpiresAt time.Time `json:"hold_expires_at"`
}

// BookingConfirmedData is doorstep.booking.confirmed's data.
type BookingConfirmedData struct {
	BookingCore
	PaidPaise int64     `json:"paid_paise"`
	PaymentID uuid.UUID `json:"payment_id"`
}

// BookingCancelledData is doorstep.booking.cancelled's data.
type BookingCancelledData struct {
	BookingCore
	CancelledBy string `json:"cancelled_by"`
	Reason      string `json:"reason"`
	FeePaise    int64  `json:"fee_paise"`
	RefundPaise int64  `json:"refund_paise"`
}

// BookingRescheduledData is doorstep.booking.rescheduled's data.
type BookingRescheduledData struct {
	BookingCore
	PreviousSlotStart time.Time `json:"previous_slot_start"`
}

// BookingRefundData is doorstep.booking.refunded's and
// doorstep.booking.refund_failed's data.
type BookingRefundData struct {
	BookingCore
	RefundID    uuid.UUID `json:"refund_id"`
	AmountPaise int64     `json:"amount_paise"`
	Cause       string    `json:"cause"`
	Reason      *string   `json:"reason"`
}

// BookingAttentionData is doorstep.booking.payment_attention's data: money
// that did not match the booking (ops live board; never a customer push).
type BookingAttentionData struct {
	BookingCore
	EventType string `json:"payment_event_type"`
	Detail    string `json:"detail"`
}
