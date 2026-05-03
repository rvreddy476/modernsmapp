package service

import (
	"context"
	"fmt"

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
	s.InvalidatePulseCache(ctx, userID)
	return out, nil
}
