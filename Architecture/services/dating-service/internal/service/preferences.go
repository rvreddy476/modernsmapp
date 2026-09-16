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
	if p.MinAge != nil && *p.MinAge < 18 {
		return nil, fmt.Errorf("invalid: min_age must be >= 18")
	}
	if p.MaxAge != nil && *p.MaxAge > 120 {
		return nil, fmt.Errorf("invalid: max_age must be <= 120")
	}
	if p.MinAge != nil && p.MaxAge != nil && *p.MinAge > *p.MaxAge {
		return nil, fmt.Errorf("invalid: min_age must be <= max_age")
	}
	if p.DistanceKm != nil && (*p.DistanceKm <= 0 || *p.DistanceKm > 500) {
		return nil, fmt.Errorf("invalid: distance_km must be between 1 and 500")
	}
	if p.InterestedInGender != nil && !validInterestedInGender(strings.TrimSpace(*p.InterestedInGender)) {
		return nil, ErrInvalidInterestedInGender
	}
	if p.IntentFilter != nil {
		for _, intent := range p.IntentFilter {
			if !validIntent(intent) {
				return nil, fmt.Errorf("invalid: intent_filter contains unknown value %q", intent)
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

// validInterestedInGender reports whether v is one of InterestedInGenders.
func validInterestedInGender(v string) bool {
	for _, g := range InterestedInGenders {
		if v == g {
			return true
		}
	}
	return false
}
