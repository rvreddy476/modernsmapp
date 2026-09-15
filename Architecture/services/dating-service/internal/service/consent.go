// Consent for sensitive data (Dating plan lane D9).
//
// Four consents are explicit and logged (dating_consent_log, append-only,
// included in the data export):
//
//   - sensitive_religion, sensitive_community: before either field is set;
//   - biometric_selfie: before a selfie liveness / face check starts, and
//     before a moderator decides a pending one;
//   - echoes: main-app activity shown in dating (also toggled by the privacy
//     PATCH, lane D7).
//
// Setting a field or starting a check without the consent is refused with
// *ConsentRequiredError (422 CONSENT_REQUIRED). Withdrawing clears the field,
// spends open selfie challenges, or turns Echoes off, in the same transaction
// as the log entry (store.SetConsent).
//
// Not covered: the dating profile has no sexual-orientation field. Gender and
// the discovery gender preference are plain columns used by SQL filters.
package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Consent types.
const (
	ConsentReligion        = store.ConsentTypeReligion
	ConsentCommunity       = store.ConsentTypeCommunity
	ConsentBiometricSelfie = store.ConsentTypeBiometricSelfie
)

// ConsentTypes lists every consent a user can grant or withdraw.
var ConsentTypes = []string{ConsentReligion, ConsentCommunity, ConsentBiometricSelfie, EchoesConsentType}

// ErrUnknownConsentType is a consent type outside ConsentTypes (400).
var ErrUnknownConsentType = errors.New("invalid: unknown consent type")

// ConsentRequiredError refuses a write or check the user has not consented to.
type ConsentRequiredError struct {
	ConsentType   string
	PolicyVersion string
}

func (e *ConsentRequiredError) Error() string { return "consent required: " + e.ConsentType }

// ConsentState is the current state of one consent.
type ConsentState struct {
	ConsentType   string     `json:"consent_type"`
	Granted       bool       `json:"granted"`
	PolicyVersion *string    `json:"policy_version,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// ConsentsView is GET /v1/dating/consents.
type ConsentsView struct {
	CurrentPolicyVersion string         `json:"current_policy_version"`
	Consents             []ConsentState `json:"consents"`
}

func validConsentType(t string) bool {
	for _, c := range ConsentTypes {
		if c == t {
			return true
		}
	}
	return false
}

// ListConsents returns the caller's current state for every consent type.
func (s *Service) ListConsents(ctx context.Context, userID uuid.UUID) (*ConsentsView, error) {
	latest, err := s.store.LatestConsents(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &ConsentsView{CurrentPolicyVersion: s.consentPolicy(), Consents: make([]ConsentState, 0, len(ConsentTypes))}
	for _, t := range ConsentTypes {
		st := ConsentState{ConsentType: t}
		if e := latest[t]; e != nil {
			st.Granted = e.Granted
			pv, at := e.PolicyVersion, e.CreatedAt.UTC()
			st.PolicyVersion, st.UpdatedAt = &pv, &at
		}
		out.Consents = append(out.Consents, st)
	}
	return out, nil
}

// SetConsent grants or withdraws one consent. Repeating the current state
// under the current policy version adds no log entry.
func (s *Service) SetConsent(ctx context.Context, userID uuid.UUID, consentType string, granted bool) (*ConsentsView, error) {
	if userID == uuid.Nil {
		return nil, errors.New("invalid: user_id required")
	}
	if !validConsentType(consentType) {
		return nil, ErrUnknownConsentType
	}
	latest, err := s.store.LatestConsents(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cur := latest[consentType]; cur != nil && cur.Granted == granted && cur.PolicyVersion == s.consentPolicy() {
		return s.ListConsents(ctx, userID)
	}
	if _, err := s.store.SetConsent(ctx, userID, consentType, granted, s.consentPolicy()); err != nil {
		return nil, err
	}
	switch consentType {
	case ConsentReligion, ConsentCommunity:
		if !granted {
			s.InvalidatePulseCache(ctx, userID)
			s.InvalidateDecksForCandidate(ctx, userID)
			if s.producer != nil {
				_ = s.producer.PublishProfileUpdated(ctx, userID, []string{strings.TrimPrefix(consentType, "sensitive_")})
			}
		}
	case EchoesConsentType:
		s.InvalidatePulseCache(ctx, userID)
	}
	return s.ListConsents(ctx, userID)
}

// requireConsent returns nil when the user's latest entry for consentType is
// a grant, else *ConsentRequiredError.
func (s *Service) requireConsent(ctx context.Context, userID uuid.UUID, consentType string) error {
	latest, err := s.store.LatestConsents(ctx, userID)
	if err != nil {
		return err
	}
	if e := latest[consentType]; e != nil && e.Granted {
		return nil
	}
	return &ConsentRequiredError{ConsentType: consentType, PolicyVersion: s.consentPolicy()}
}

// requireSensitiveProfileConsent gates a profile write that sets religion or
// community to a non-blank value. Clearing a field needs no consent.
func (s *Service) requireSensitiveProfileConsent(ctx context.Context, userID uuid.UUID, p store.UpsertProfileParams) error {
	if p.Religion != nil && strings.TrimSpace(*p.Religion) != "" {
		if err := s.requireConsent(ctx, userID, ConsentReligion); err != nil {
			return err
		}
	}
	if p.Community != nil && strings.TrimSpace(*p.Community) != "" {
		if err := s.requireConsent(ctx, userID, ConsentCommunity); err != nil {
			return err
		}
	}
	return nil
}
