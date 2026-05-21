package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MaskedCall mirrors rider_masked_calls.
type MaskedCall struct {
	ID         uuid.UUID  `json:"id"`
	RideID     *uuid.UUID `json:"ride_id,omitempty"`
	CallerID   uuid.UUID  `json:"caller_id"`
	CalleeID   uuid.UUID  `json:"callee_id"`
	ProxyDID   string     `json:"proxy_did"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	DurationS  *int       `json:"duration_s,omitempty"`
	Status     string     `json:"status"`
}

// CreateMaskedCall inserts a row when a customer/partner places a
// proxied call. proxy_did is the masked number returned by the
// upstream provider (Exotel/Twilio/etc.).
func (s *Store) CreateMaskedCall(ctx context.Context, rideID *uuid.UUID, callerID, calleeID uuid.UUID, proxyDID string) (*MaskedCall, error) {
	var c MaskedCall
	if err := s.db.QueryRow(ctx, `
		INSERT INTO rider_masked_calls (ride_id, caller_id, callee_id, proxy_did, status)
		VALUES ($1, $2, $3, $4, 'initiated')
		RETURNING id, ride_id, caller_id, callee_id, proxy_did, started_at, ended_at, duration_s, status
	`, rideID, callerID, calleeID, proxyDID).Scan(
		&c.ID, &c.RideID, &c.CallerID, &c.CalleeID, &c.ProxyDID,
		&c.StartedAt, &c.EndedAt, &c.DurationS, &c.Status,
	); err != nil {
		return nil, fmt.Errorf("create masked call: %w", err)
	}
	return &c, nil
}

// EndMaskedCall closes the audit row when the provider posts a
// completion webhook.
func (s *Store) EndMaskedCall(ctx context.Context, callID uuid.UUID, durationS int, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE rider_masked_calls
		SET ended_at = NOW(), duration_s = $2, status = $3
		WHERE id = $1
	`, callID, durationS, status)
	return err
}

// SafetyContactAlert mirrors rider_safety_contact_alerts.
type SafetyContactAlert struct {
	ID           uuid.UUID `json:"id"`
	IncidentID   uuid.UUID `json:"incident_id"`
	ContactPhone string    `json:"contact_phone"`
	ContactName  *string   `json:"contact_name,omitempty"`
	Channel      string    `json:"channel"`
	Result       string    `json:"result"`
	Error        *string   `json:"error,omitempty"`
	SentAt       time.Time `json:"sent_at"`
}

// RecordSafetyContactAlert audits a dispatch to a trusted contact.
// Called by the SOS handler after each SMS/push/call attempt.
func (s *Store) RecordSafetyContactAlert(ctx context.Context, incidentID uuid.UUID, contactPhone, contactName, channel, result, errText string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO rider_safety_contact_alerts
			(incident_id, contact_phone, contact_name, channel, result, error)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
	`, incidentID, contactPhone, contactName, channel, result, errText)
	return err
}

// ListContactAlertsForIncident returns every alert dispatched for an
// incident. Admin audit only.
func (s *Store) ListContactAlertsForIncident(ctx context.Context, incidentID uuid.UUID) ([]SafetyContactAlert, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, incident_id, contact_phone, contact_name, channel, result, error, sent_at
		FROM rider_safety_contact_alerts
		WHERE incident_id = $1
		ORDER BY sent_at DESC
	`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SafetyContactAlert
	for rows.Next() {
		var a SafetyContactAlert
		if err := rows.Scan(&a.ID, &a.IncidentID, &a.ContactPhone, &a.ContactName,
			&a.Channel, &a.Result, &a.Error, &a.SentAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
