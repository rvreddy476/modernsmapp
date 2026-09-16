package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// GetPreferences returns the user's preferences row, creating defaults if
// none exists.
func (s *Service) GetPreferences(ctx context.Context, userID uuid.UUID) (*store.Preferences, error) {
	return s.store.GetPreferences(ctx, userID)
}

// UpsertPreferences validates and persists the partial update.
func (s *Service) UpsertPreferences(ctx context.Context, userID uuid.UUID, p store.UpsertPreferencesParams) (*store.Preferences, error) {
	if p.MinAge != nil && *p.MinAge < MinPreferenceAge {
		return nil, ErrMinAgeTooLow
	}
	if p.MaxAge != nil && *p.MaxAge > MaxPreferenceAge {
		return nil, ErrMaxAgeTooHigh
	}
	if p.MinAge != nil && p.MaxAge != nil && *p.MinAge > *p.MaxAge {
		return nil, ErrAgeRangeInverted
	}
	if p.DistanceKm != nil && (*p.DistanceKm < MinDistanceKm || *p.DistanceKm > MaxDistanceKm) {
		return nil, ErrInvalidDistanceKm
	}
	if p.InterestedInGender != nil && !validInterestedInGender(strings.TrimSpace(*p.InterestedInGender)) {
		return nil, ErrInvalidInterestedInGender
	}
	if p.IntentFilter != nil {
		for _, intent := range p.IntentFilter {
			if !validIntent(intent) {
				return nil, ErrInvalidIntentFilter
			}
		}
	}
	out, err := s.store.UpsertPreferences(ctx, userID, p)
	if err != nil {
		return nil, err
	}
	// interested_in is one of the onboarding basics (lane D2): setting it
	// may complete draft → pending_photo. No profile yet is fine.
	if p.InterestedInGender != nil {
		if _, err := s.advanceOnboarding(ctx, userID); err != nil && !errors.Is(err, store.ErrProfileNotFound) {
			return nil, err
		}
	}
	s.InvalidatePulseCache(ctx, userID)
	return out, nil
}

// InterestedInGenders is the enum accepted for a discovery gender
// preference. It is the same vocabulary a profile's own gender uses, plus
// "everyone": the preference must EQUAL the other person's gender, and
// "everyone" is the only way to say "no gender filter".
var InterestedInGenders = []string{"woman", "man", "nonbinary", "everyone"}

// InterestedInEveryone is the preference that applies no gender filter.
const InterestedInEveryone = "everyone"

// ErrInvalidInterestedInGender: interested_in_gender is not one of
// InterestedInGenders. It has its own stable code
// (400 INVALID_INTERESTED_IN_GENDER) rather than the generic INVALID_REQUEST,
// because the app shows the picker again and needs to know which field the
// server refused.
var ErrInvalidInterestedInGender = errors.New("interested_in_gender must be one of " + strings.Join(InterestedInGenders, ", "))

// The age and distance bounds a discovery preference must stay inside.
// MinPreferenceAge is the legal floor (the product is 18+), not a taste.
const (
	MinPreferenceAge = 18
	MaxPreferenceAge = 120
	MinDistanceKm    = 1
	MaxDistanceKm    = 500
)

// The preference validation refusals, each with its own stable code rather
// than the generic INVALID_REQUEST, so the app can put the message on the
// field the server actually refused. The three age errors share the code
// INVALID_AGE_RANGE (400) and differ only in message; distance is
// INVALID_DISTANCE_KM and the intent filter INVALID_INTENT_FILTER.
var (
	ErrMinAgeTooLow        = fmt.Errorf("min_age must be >= %d", MinPreferenceAge)
	ErrMaxAgeTooHigh       = fmt.Errorf("max_age must be <= %d", MaxPreferenceAge)
	ErrAgeRangeInverted    = errors.New("min_age must be <= max_age")
	ErrInvalidDistanceKm   = fmt.Errorf("distance_km must be between %d and %d", MinDistanceKm, MaxDistanceKm)
	ErrInvalidIntentFilter = errors.New("intent_filter values must each be one of " + strings.Join(Intents, ", "))
)

// validInterestedInGender reports whether v is one of InterestedInGenders.
func validInterestedInGender(v string) bool {
	for _, g := range InterestedInGenders {
		if v == g {
			return true
		}
	}
	return false
}
