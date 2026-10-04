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

// Dispatch event types (A4).
const (
	BookingAssigned        = "doorstep.booking.assigned"
	BookingReassigned      = "doorstep.booking.reassigned"
	BookingUnassignedAlert = "doorstep.booking.unassigned_alert"
	BookingNoShow          = "doorstep.booking.no_show"
	// BookingProLate: slot + 15 min and the professional has not arrived;
	// the customer may cancel free of charge.
	BookingProLate  = "doorstep.booking.pro_late"
	ProOfferCreated = "doorstep.pro.offer_created"
	ProOfferClosed  = "doorstep.pro.offer_closed"
)

// Reassignment causes (asyncapi BookingReassigned.cause).
const (
	CauseProCancel     = "pro_cancel"
	CauseProNoShow     = "pro_no_show"
	CauseNotOnDuty     = "not_on_duty"
	CauseUnsafeExit    = "unsafe_exit"
	CauseOpsRedispatch = "ops_redispatch"
	CauseProSuspended  = "pro_suspended"
	CauseRescheduled   = "rescheduled"
)

// Unassigned alert reasons.
const (
	AlertTMinus2h           = "t_minus_2h"
	AlertNoProfessionalLeft = "no_professional_left"
)

// BookingAssignedData is doorstep.booking.assigned's data.
type BookingAssignedData struct {
	BookingCore
	ProFirstName string    `json:"pro_first_name"`
	AssignmentID uuid.UUID `json:"assignment_id"`
}

// BookingReassignedData is doorstep.booking.reassigned's data: the booking is
// back to confirmed and dispatch re-runs excluding previous_pro_user_id, who
// is named so they are told the job is no longer theirs.
type BookingReassignedData struct {
	BookingCore
	PreviousProUserID uuid.UUID `json:"previous_pro_user_id"`
	Cause             string    `json:"cause"`
}

// BookingUnassignedAlertData is doorstep.booking.unassigned_alert's data
// (ops only).
type BookingUnassignedAlertData struct {
	BookingCore
	MinutesToSlot int    `json:"minutes_to_slot"`
	Reason        string `json:"reason"`
}

// BookingNoShowData is doorstep.booking.no_show's data.
type BookingNoShowData struct {
	BookingCore
	Party    string `json:"party"`
	FeePaise int64  `json:"fee_paise"`
}

// BookingProLateData is doorstep.booking.pro_late's data.
type BookingProLateData struct {
	BookingCore
	MinutesLate int  `json:"minutes_late"`
	FreeCancel  bool `json:"free_cancel"`
}

// ProOfferCreatedData is doorstep.pro.offer_created's data. Locality only:
// never the address before acceptance.
type ProOfferCreatedData struct {
	OfferID        uuid.UUID `json:"offer_id"`
	BookingID      uuid.UUID `json:"booking_id"`
	ProUserID      uuid.UUID `json:"pro_user_id"`
	CustomerUserID uuid.UUID `json:"customer_user_id"`
	ExpiresAt      time.Time `json:"expires_at"`
	Locality       string    `json:"locality"`
}

// ProOfferClosedData is doorstep.pro.offer_closed's data.
type ProOfferClosedData struct {
	OfferID        uuid.UUID `json:"offer_id"`
	BookingID      uuid.UUID `json:"booking_id"`
	ProUserID      uuid.UUID `json:"pro_user_id"`
	CustomerUserID uuid.UUID `json:"customer_user_id"`
	Outcome        string    `json:"outcome"`
}

// ProOffer builds a doorstep.pro.offer_* event: partition key is the
// professional's user id; the envelope names the booking and both users.
func ProOffer(eventType string, bookingID, customer, proUserID uuid.UUID, at time.Time, data any) (key string, payload []byte, err error) {
	b, c, p := bookingID, customer, proUserID
	payload, err = json.Marshal(Envelope{EventID: uuid.New(), EventType: eventType, Version: 1, OccurredAt: at.UTC(),
		BookingID: &b, CustomerUserID: &c, ProUserID: &p, Data: data})
	return proUserID.String(), payload, err
}

// B1 event types (4 Oct 2026): professionals' prices and the customer's
// choice when a professional is gone.
const (
	// ProPriceSubmitted: a professional submitted a price (pending review).
	ProPriceSubmitted = "doorstep.pro.price_submitted"
	// ProPriceReviewed: an admin approved or rejected a price.
	ProPriceReviewed = "doorstep.pro.price_reviewed"
	// BookingProUnavailable: the chosen professional declined, let the offer
	// lapse, gave the job back, was not on duty, did not turn up, or ops took
	// the job off them. The customer picks another professional and a time,
	// or cancels for a full refund, by choice_deadline (else a full refund).
	BookingProUnavailable = "doorstep.booking.pro_unavailable"
	// BookingProChanged: the customer picked another professional for a
	// pro_unavailable booking; it is confirmed again and offered to them.
	BookingProChanged = "doorstep.booking.pro_changed"
)

// Unavailable causes (bookings.unavailable_cause).
const (
	UnavailableDeclined       = "declined"
	UnavailableOfferExpired   = "offer_expired"
	UnavailableProCancel      = "pro_cancel"
	UnavailableNotOnDuty      = "not_on_duty"
	UnavailableProNoShow      = "pro_no_show"
	UnavailableOpsRedispatch  = "ops_redispatch"
	UnavailableNoProfessional = "no_professional"
)

// ProPriceSubmittedData is doorstep.pro.price_submitted's data (ops review
// queue; previous_price_paise is the live approved price, if any).
type ProPriceSubmittedData struct {
	PriceID            uuid.UUID `json:"price_id"`
	ProID              uuid.UUID `json:"pro_id"`
	ProUserID          uuid.UUID `json:"pro_user_id"`
	ServiceID          uuid.UUID `json:"service_id"`
	ItemKind           string    `json:"item_kind"`
	ItemID             uuid.UUID `json:"item_id"`
	Unit               string    `json:"unit"`
	PricePaise         int64     `json:"price_paise"`
	PreviousPricePaise *int64    `json:"previous_price_paise"`
}

// ProPriceReviewedData is doorstep.pro.price_reviewed's data.
type ProPriceReviewedData struct {
	PriceID    uuid.UUID `json:"price_id"`
	ProID      uuid.UUID `json:"pro_id"`
	ProUserID  uuid.UUID `json:"pro_user_id"`
	ServiceID  uuid.UUID `json:"service_id"`
	ItemKind   string    `json:"item_kind"`
	ItemID     uuid.UUID `json:"item_id"`
	PricePaise int64     `json:"price_paise"`
	Decision   string    `json:"decision"`
	Reason     *string   `json:"reason"`
}

// BookingProUnavailableData is doorstep.booking.pro_unavailable's data. The
// envelope pro_user_id is null (nobody holds the job); previous_pro_user_id
// names the professional who let it go (null when there was none).
type BookingProUnavailableData struct {
	BookingCore
	PreviousProUserID *uuid.UUID `json:"previous_pro_user_id"`
	Cause             string     `json:"cause"`
	ChoiceDeadline    time.Time  `json:"choice_deadline"`
}

// BookingProChangedData is doorstep.booking.pro_changed's data: the booking
// is confirmed again with the professional the customer picked
// (new_pro_user_id is offered the job next; the envelope pro_user_id stays
// null until they accept).
type BookingProChangedData struct {
	BookingCore
	ChangeID          uuid.UUID  `json:"change_id"`
	PreviousProUserID *uuid.UUID `json:"previous_pro_user_id"`
	NewProUserID      uuid.UUID  `json:"new_pro_user_id"`
	DifferencePaise   int64      `json:"difference_paise"`
	Asap              bool       `json:"asap"`
}

// Visit event types (A5).
const (
	BookingEnRoute            = "doorstep.booking.en_route"
	BookingArrived            = "doorstep.booking.arrived"
	BookingStarted            = "doorstep.booking.started"
	BookingExtrasProposed     = "doorstep.booking.extras_proposed"
	BookingExtrasDecided      = "doorstep.booking.extras_decided"
	BookingExtrasPaymentDue   = "doorstep.booking.extras_payment_due"
	BookingExtrasPaid         = "doorstep.booking.extras_paid"
	BookingOutstandingCreated = "doorstep.booking.outstanding_created"
	BookingCompleted          = "doorstep.booking.completed"
	BookingRated              = "doorstep.booking.rated"
	BookingReworkRequested    = "doorstep.booking.rework_requested"
	BookingMessageSent        = "doorstep.booking.message_sent"
	IncidentRaised            = "doorstep.incident.raised"
	ProSettlementComputed     = "doorstep.pro.settlement_computed"
)

// A5 pro_unavailable causes: the professional left feeling unsafe (no
// penalty), or a salon customer's incident suspended them.
const (
	UnavailableProUnsafeExit = "pro_unsafe_exit"
	UnavailableProSuspended  = "pro_suspended"
)

// BookingEnRouteData is doorstep.booking.en_route's data.
type BookingEnRouteData struct {
	BookingCore
	EtaMinutes *int `json:"eta_minutes"`
}

// BookingExtrasProposedData is doorstep.booking.extras_proposed's data.
type BookingExtrasProposedData struct {
	BookingCore
	ExtraID    uuid.UUID `json:"extra_id"`
	Name       string    `json:"name"`
	TotalPaise int64     `json:"total_paise"`
}

// BookingExtrasDecidedData is doorstep.booking.extras_decided's data.
type BookingExtrasDecidedData struct {
	BookingCore
	ExtraID  uuid.UUID `json:"extra_id"`
	Decision string    `json:"decision"`
}

// BookingExtrasBillData is the data of extras_payment_due (with due_at),
// extras_paid and outstanding_created.
type BookingExtrasBillData struct {
	BookingCore
	BillID      uuid.UUID  `json:"bill_id"`
	AmountPaise int64      `json:"amount_paise"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// BookingCompletedData is doorstep.booking.completed's data.
type BookingCompletedData struct {
	BookingCore
	TotalPaise       int64 `json:"total_paise"`
	ExtrasTotalPaise int64 `json:"extras_total_paise"`
}

// BookingRatedData is doorstep.booking.rated's data.
type BookingRatedData struct {
	BookingCore
	RaterKind string `json:"rater_kind"`
	Stars     int    `json:"stars"`
}

// BookingReworkRequestedData is doorstep.booking.rework_requested's data.
type BookingReworkRequestedData struct {
	BookingCore
	ReworkID       uuid.UUID  `json:"rework_id"`
	ChildBookingID *uuid.UUID `json:"child_booking_id"`
}

// BookingMessageSentData is doorstep.booking.message_sent's data: never the
// body.
type BookingMessageSentData struct {
	BookingCore
	MessageID  uuid.UUID `json:"message_id"`
	SenderKind string    `json:"sender_kind"`
}

// IncidentRaisedData is doorstep.incident.raised's data (ops page it; the
// note and the position stay in the database).
type IncidentRaisedData struct {
	IncidentID       uuid.UUID  `json:"incident_id"`
	BookingID        *uuid.UUID `json:"booking_id"`
	CustomerUserID   *uuid.UUID `json:"customer_user_id"`
	ProUserID        *uuid.UUID `json:"pro_user_id"`
	Kind             string     `json:"kind"`
	Severity         string     `json:"severity"`
	RaisedByKind     string     `json:"raised_by_kind"`
	ProAutoSuspended bool       `json:"pro_auto_suspended"`
}

// Incident builds doorstep.incident.raised: partition key is the booking
// (else the incident), the envelope names both users.
func Incident(d IncidentRaisedData, at time.Time) (key string, payload []byte, err error) {
	payload, err = json.Marshal(Envelope{EventID: uuid.New(), EventType: IncidentRaised, Version: 1, OccurredAt: at.UTC(),
		BookingID: d.BookingID, CustomerUserID: d.CustomerUserID, ProUserID: d.ProUserID, Data: d})
	key = d.IncidentID.String()
	if d.BookingID != nil {
		key = d.BookingID.String()
	}
	return key, payload, err
}

// ProSettlementComputedData is doorstep.pro.settlement_computed's data.
type ProSettlementComputedData struct {
	SettlementID uuid.UUID `json:"settlement_id"`
	ProID        uuid.UUID `json:"pro_id"`
	ProUserID    uuid.UUID `json:"pro_user_id"`
	NetPaise     int64     `json:"net_paise"`
	PeriodStart  string    `json:"period_start"`
	PeriodEnd    string    `json:"period_end"`
}
