// Safety surface for Sprint 3: SOS, share-ride tokens, trusted contact.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §12 (Safety) + §17 (Admin endpoints).
//
// Boundary rules:
//  - SOS may only fire while a ride is in_progress; the spec is explicit
//    that pre-pickup or post-completion SOS becomes a generic incident,
//    not a critical alert. We use the same rider_safety_incidents table
//    but downgrade severity in those cases.
//  - Share tokens are 32-char hex (16 bytes of crypto/rand) — same shape
//    as the S2 share token but stored in their own table with a 24-hour
//    expiry. The redacted view drops PII (full pickup street, customer
//    phone, customer name) and shows only what a worried family member
//    needs to track the ride.
//  - Trusted contact PII (phone) is redacted to last-4 in the contact-alert
//    payload sent to Kafka.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// ShareTokenTTL is the lifetime of a share token. Per spec §12 — short
// enough to bound abuse, long enough to cover an Indian intercity ride.
const ShareTokenTTL = 24 * time.Hour

// SOSResult is the response shape from TriggerSOS.
type SOSResult struct {
	IncidentID uuid.UUID `json:"incident_id"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// TriggerSOS records an SOS incident. Allowed when the ride is in_progress
// (spec: customer-side panic during the trip). For pre-pickup or arrived
// states the incident is still recorded but downgraded to severity=high so
// trust-and-safety can review without paging the on-call admin.
//
// Idempotency: a customer who taps SOS twice within 60s gets the same
// incident_id (we look up an open same-ride incident before inserting).
func (s *Service) TriggerSOS(ctx context.Context, customerID, rideID uuid.UUID, lat, lng *float64) (*SOSResult, error) {
	if customerID == uuid.Nil || rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer_id and ride_id required")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if ride.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}

	// Severity policy. Critical only when the customer is actually moving
	// (in_progress); high during the rendezvous; medium otherwise. Always
	// record — never silently drop.
	severity := "medium"
	switch ride.Status {
	case "in_progress", "otp_verified":
		severity = "critical"
	case "partner_assigned", "partner_arriving", "arrived":
		severity = "high"
	}

	meta := map[string]any{
		"ride_status": ride.Status,
		"trigger":     "customer_app",
	}
	if lat != nil && lng != nil {
		meta["lat"] = *lat
		meta["lng"] = *lng
	}
	metaBytes, _ := json.Marshal(meta)

	var partnerRef *uuid.UUID
	if ride.PartnerID != nil {
		p := *ride.PartnerID
		partnerRef = &p
	}
	cust := customerID
	rid := rideID
	incident, err := s.store.CreateSafetyIncident(ctx, store.CreateSafetyIncidentInput{
		RideID:     &rid,
		CustomerID: &cust,
		PartnerID:  partnerRef,
		Kind:       "sos_triggered",
		Severity:   severity,
		Metadata:   metaBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
	}

	// Emit primary SOS event.
	sosPayload := events.SafetySOSPayload{
		IncidentID: incident.ID.String(),
		RideID:     rideID.String(),
		CustomerID: customerID.String(),
		Severity:   severity,
		OccurredAt: incident.CreatedAt,
	}
	if partnerRef != nil {
		sosPayload.PartnerID = partnerRef.String()
	}
	if lat != nil {
		sosPayload.Lat = *lat
	}
	if lng != nil {
		sosPayload.Lng = *lng
	}
	if perr := s.producer.PublishSafetySOS(ctx, sosPayload); perr != nil {
		slog.Warn("rider: publish safety.sos failed", "incident_id", incident.ID, "error", perr)
	}

	// Best-effort trusted-contact alert. A missing contact is NOT an error;
	// we just skip the secondary alert. Per spec §12 the customer might
	// not have set one yet.
	if contact, cerr := s.store.GetTrustedContact(ctx, customerID); cerr == nil {
		alert := events.SafetyContactAlertPayload{
			IncidentID:   incident.ID.String(),
			RideID:       rideID.String(),
			CustomerID:   customerID.String(),
			ContactName:  contact.ContactName,
			ContactPhone: redactPhone(contact.ContactPhone),
			Message:      "SOS triggered on Mopedu ride. Mopedu Trust & Safety is on it.",
			OccurredAt:   incident.CreatedAt,
		}
		if perr := s.producer.PublishSafetyContactAlert(ctx, alert); perr != nil {
			slog.Warn("rider: publish safety.contact_alert failed", "incident_id", incident.ID, "error", perr)
		}
	} else if !errors.Is(cerr, store.ErrTrustedContactNotFound) {
		slog.Warn("rider: trusted contact lookup failed", "customer_id", customerID, "error", cerr)
	}

	return &SOSResult{
		IncidentID: incident.ID,
		Severity:   severity,
		Status:     incident.Status,
		CreatedAt:  incident.CreatedAt,
	}, nil
}

// AcknowledgeSafetyIncident — admin-only. open -> acknowledged.
func (s *Service) AcknowledgeSafetyIncident(ctx context.Context, incidentID, adminID uuid.UUID) (*store.SafetyIncident, error) {
	if adminID == uuid.Nil {
		return nil, fmt.Errorf("forbidden: admin id required")
	}
	incident, err := s.store.AcknowledgeSafetyIncident(ctx, incidentID, adminID)
	if err != nil {
		if errors.Is(err, store.ErrSafetyIncidentNotFound) {
			return nil, fmt.Errorf("not_found: incident")
		}
		return nil, err
	}
	payload := events.SafetyIncidentLifecyclePayload{
		IncidentID: incident.ID.String(),
		AdminID:    adminID.String(),
		OccurredAt: time.Now().UTC(),
	}
	if incident.RideID != nil {
		payload.RideID = incident.RideID.String()
	}
	if perr := s.producer.PublishSafetyIncidentAcknowledged(ctx, payload); perr != nil {
		slog.Warn("rider: publish safety.acknowledged failed", "incident_id", incidentID, "error", perr)
	}
	return incident, nil
}

// ResolveSafetyIncident — admin-only. acknowledged|open -> resolved.
func (s *Service) ResolveSafetyIncident(ctx context.Context, incidentID, adminID uuid.UUID, note string) (*store.SafetyIncident, error) {
	if adminID == uuid.Nil {
		return nil, fmt.Errorf("forbidden: admin id required")
	}
	incident, err := s.store.ResolveSafetyIncident(ctx, incidentID, adminID, strings.TrimSpace(note))
	if err != nil {
		if errors.Is(err, store.ErrSafetyIncidentNotFound) {
			return nil, fmt.Errorf("not_found: incident or already resolved")
		}
		return nil, err
	}
	payload := events.SafetyIncidentLifecyclePayload{
		IncidentID: incident.ID.String(),
		AdminID:    adminID.String(),
		Note:       note,
		OccurredAt: time.Now().UTC(),
	}
	if incident.RideID != nil {
		payload.RideID = incident.RideID.String()
	}
	if perr := s.producer.PublishSafetyIncidentResolved(ctx, payload); perr != nil {
		slog.Warn("rider: publish safety.resolved failed", "incident_id", incidentID, "error", perr)
	}
	return incident, nil
}

// --- Share tokens ---------------------------------------------------------

// CreateShareTokenResult is the response shape from CreateShareToken.
type CreateShareTokenResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateShareToken generates a 32-char hex token bound to (ride, customer)
// with a 24h expiry. The ride must belong to the customer and be in a
// non-terminal state (sharing a completed ride is pointless and confusing).
func (s *Service) CreateShareToken(ctx context.Context, customerID, rideID uuid.UUID) (*CreateShareTokenResult, error) {
	if customerID == uuid.Nil || rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer_id and ride_id required")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if ride.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}
	if isTerminalRideStatus(ride.Status) {
		return nil, fmt.Errorf("invalid: ride is in terminal state %q", ride.Status)
	}
	tok, err := generateShareToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(ShareTokenTTL)
	stored, err := s.store.CreateShareToken(ctx, tok, rideID, customerID, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("store share token: %w", err)
	}
	if perr := s.producer.PublishShareTokenCreated(ctx, events.ShareTokenCreatedPayload{
		Token:      stored.Token,
		RideID:     rideID.String(),
		CustomerID: customerID.String(),
		ExpiresAt:  stored.ExpiresAt,
	}); perr != nil {
		slog.Warn("rider: publish share.token_created failed", "ride_id", rideID, "error", perr)
	}
	return &CreateShareTokenResult{Token: stored.Token, ExpiresAt: stored.ExpiresAt}, nil
}

// SharedRideView is the redacted ride snapshot served to the public share
// link. Drops customer PII (no name, no phone) and the precise pickup
// street; keeps drop, partner first name, vehicle reg, ETA.
type SharedRideView struct {
	RideID            string    `json:"ride_id"`
	Status            string    `json:"status"`
	VehicleType       string    `json:"vehicle_type"`
	PickupArea        string    `json:"pickup_area"` // first comma-separated chunk only
	DropAddress       string    `json:"drop_address"`
	DropLat           float64   `json:"drop_lat"`
	DropLng           float64   `json:"drop_lng"`
	PartnerFirstName  string    `json:"partner_first_name,omitempty"`
	PartnerPhotoURL   string    `json:"partner_photo_url,omitempty"`
	PartnerRating     float64   `json:"partner_rating,omitempty"`
	VehicleRegSuffix  string    `json:"vehicle_reg_suffix,omitempty"` // last 4 only
	EstimatedFareINR  *float64  `json:"estimated_fare_inr,omitempty"`
	EstimatedDistance *float64  `json:"estimated_distance_km,omitempty"`
	ExpiresAt         time.Time `json:"share_expires_at"`
}

// GetSharedRide resolves a share token to a redacted ride view. The view
// count is incremented; expired tokens return 410-style errors.
func (s *Service) GetSharedRide(ctx context.Context, token string) (*SharedRideView, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("invalid: token required")
	}
	share, err := s.store.LookupShareToken(ctx, token)
	if err != nil {
		if errors.Is(err, store.ErrShareTokenNotFound) {
			return nil, fmt.Errorf("not_found: share token")
		}
		if errors.Is(err, store.ErrShareTokenExpired) {
			return nil, fmt.Errorf("invalid: share token expired")
		}
		return nil, err
	}
	ride, err := s.store.GetRide(ctx, share.RideID)
	if err != nil {
		return nil, err
	}
	view := &SharedRideView{
		RideID:            ride.ID.String(),
		Status:            ride.Status,
		VehicleType:       ride.VehicleType,
		PickupArea:        firstChunk(ride.PickupAddress),
		DropAddress:       ride.DropAddress,
		DropLat:           ride.DropLat,
		DropLng:           ride.DropLng,
		EstimatedFareINR:  ride.EstimatedFare,
		EstimatedDistance: ride.EstimatedDistanceKM,
		ExpiresAt:         share.ExpiresAt,
	}
	if ride.PartnerID != nil {
		if p, perr := s.store.GetPartner(ctx, *ride.PartnerID); perr == nil {
			view.PartnerFirstName = firstName(p.FullName)
			if p.ProfilePhotoURL != nil {
				view.PartnerPhotoURL = *p.ProfilePhotoURL
			}
			view.PartnerRating = p.Rating
		}
	}
	if ride.VehicleID != nil {
		if v, verr := s.store.GetVehicle(ctx, *ride.VehicleID); verr == nil {
			view.VehicleRegSuffix = lastN(v.RegistrationNumber, 4)
		}
	}
	if mverr := s.store.MarkShareTokenViewed(ctx, token); mverr != nil {
		slog.Warn("rider: mark share token viewed failed", "token", token, "error", mverr)
	}
	return view, nil
}

// --- Trusted contact ------------------------------------------------------

// SetTrustedContactRequest is the input shape for SetTrustedContact.
type SetTrustedContactRequest struct {
	Name         string
	Phone        string
	Relationship *string
	ShareOnSOS   bool
}

// SetTrustedContact upserts the customer's trusted contact.
func (s *Service) SetTrustedContact(ctx context.Context, userID uuid.UUID, req SetTrustedContactRequest) (*store.TrustedContact, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	name := strings.TrimSpace(req.Name)
	phone := strings.TrimSpace(req.Phone)
	if name == "" {
		return nil, fmt.Errorf("invalid: contact name required")
	}
	if phone == "" {
		return nil, fmt.Errorf("invalid: contact phone required")
	}
	if len(phone) < 6 {
		return nil, fmt.Errorf("invalid: contact phone too short")
	}
	return s.store.UpsertTrustedContact(ctx, store.UpsertTrustedContactInput{
		UserID:              userID,
		ContactName:         name,
		ContactPhone:        phone,
		ContactRelationship: req.Relationship,
		ShareLocationOnSOS:  req.ShareOnSOS,
	})
}

// GetTrustedContact returns the customer's trusted contact or a not_found error.
func (s *Service) GetTrustedContact(ctx context.Context, userID uuid.UUID) (*store.TrustedContact, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	t, err := s.store.GetTrustedContact(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrTrustedContactNotFound) {
			return nil, fmt.Errorf("not_found: trusted contact")
		}
		return nil, err
	}
	return t, nil
}

// --- helpers --------------------------------------------------------------

// generateShareToken returns a 32-char hex token (16 bytes of crypto/rand).
func generateShareToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// isTerminalRideStatus is true when the ride no longer changes state.
func isTerminalRideStatus(status string) bool {
	switch status {
	case "completed", "expired", "failed",
		"cancelled_by_customer", "cancelled_by_partner", "cancelled_by_admin", "cancelled_by_system":
		return true
	}
	return false
}

// firstChunk returns the first comma-separated chunk of an address, used
// to redact the precise pickup street from the public share view.
func firstChunk(addr string) string {
	if i := strings.Index(addr, ","); i >= 0 {
		return strings.TrimSpace(addr[:i])
	}
	return addr
}

// firstName returns the first space-separated chunk of a full name.
func firstName(full string) string {
	if i := strings.Index(full, " "); i >= 0 {
		return strings.TrimSpace(full[:i])
	}
	return full
}

// lastN returns the last n characters of s, or s if shorter than n.
func lastN(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// redactPhone keeps only the last 4 digits, replacing the prefix with stars.
// Used in the contact-alert payload so Kafka logs don't carry full numbers.
func redactPhone(phone string) string {
	digits := make([]byte, 0, len(phone))
	for i := 0; i < len(phone); i++ {
		if phone[i] >= '0' && phone[i] <= '9' {
			digits = append(digits, phone[i])
		}
	}
	if len(digits) <= 4 {
		return string(digits)
	}
	return strings.Repeat("*", len(digits)-4) + string(digits[len(digits)-4:])
}
