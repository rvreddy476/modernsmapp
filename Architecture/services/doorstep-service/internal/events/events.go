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
