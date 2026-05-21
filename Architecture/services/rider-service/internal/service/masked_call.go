// masked_call.go is the rider-side proxied-call entry point.
//
// Production wires this against Exotel / Knowlarity / Twilio Proxy
// (env MASKED_CALL_PROVIDER + provider-specific creds). v1 ships a
// stub that returns a pseudo-DID and inserts the audit row so the
// admin board can see "who called whom" without exposing raw phone
// numbers to either side.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// MaskedCallResult is what the partner / customer mobile app dials.
// proxy_did is the masked +91 number provided by the upstream; the
// stub returns a deterministic-ish "+91-555-XXXXXX" so dev flows can
// proceed without provider creds.
type MaskedCallResult struct {
	CallID   uuid.UUID `json:"call_id"`
	ProxyDID string    `json:"proxy_did"`
}

// InitiateMaskedCall mints a call audit row + returns the proxy DID.
// `rideID` is optional — masked calls between matched customer +
// partner reference the ride; out-of-band ops calls don't.
func (s *Service) InitiateMaskedCall(ctx context.Context, rideID *uuid.UUID, callerID, calleeID uuid.UUID) (*MaskedCallResult, error) {
	if callerID == uuid.Nil || calleeID == uuid.Nil {
		return nil, fmt.Errorf("invalid: caller and callee required")
	}
	// Stub provider — deterministic six-hex suffix for traceability;
	// production replaces this with a real upstream call.
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	proxyDID := "+91-555-" + hex.EncodeToString(suffix)

	row, err := s.store.CreateMaskedCall(ctx, rideID, callerID, calleeID, proxyDID)
	if err != nil {
		return nil, err
	}
	return &MaskedCallResult{CallID: row.ID, ProxyDID: proxyDID}, nil
}

// EndMaskedCall is the provider webhook entry. Stamps duration +
// terminal status on the audit row. Idempotent on retry.
func (s *Service) EndMaskedCall(ctx context.Context, callID uuid.UUID, durationS int, status string) error {
	return s.store.EndMaskedCall(ctx, callID, durationS, status)
}

// ListSafetyContactAlerts returns every dispatched alert for one
// incident. Admin/moderator only — enforced at handler level.
func (s *Service) ListSafetyContactAlerts(ctx context.Context, incidentID uuid.UUID) ([]store.SafetyContactAlert, error) {
	return s.store.ListContactAlertsForIncident(ctx, incidentID)
}
