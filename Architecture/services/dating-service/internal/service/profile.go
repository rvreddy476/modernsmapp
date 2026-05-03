package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func validIntent(i string) bool {
	switch i {
	case "casual", "serious", "marriage":
		return true
	}
	return false
}

func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error) {
	return s.store.GetProfile(ctx, userID)
}

func (s *Service) UpsertProfile(ctx context.Context, userID uuid.UUID, p store.UpsertProfileParams) (*store.Profile, error) {
	if p.Intent != nil && !validIntent(*p.Intent) {
		return nil, fmt.Errorf("invalid: intent must be one of casual|serious|marriage")
	}
	existed := true
	if _, err := s.store.GetProfile(ctx, userID); err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			existed = false
		}
	}
	out, err := s.store.UpsertProfile(ctx, userID, p)
	if err != nil {
		return nil, err
	}
	if p.Latitude != nil || p.Longitude != nil {
		_ = s.store.SetProfileGeohash(ctx, userID)
	}
	if s.producer != nil {
		if !existed {
			_ = s.producer.PublishProfileCreated(ctx, userID, out.Intent)
		} else {
			_ = s.producer.PublishProfileUpdated(ctx, userID, fieldsTouched(p))
		}
	}
	s.InvalidatePulseCache(ctx, userID)
	return out, nil
}

func (s *Service) SetIntent(ctx context.Context, userID uuid.UUID, intent string) (*store.Profile, error) {
	if !validIntent(intent) {
		return nil, fmt.Errorf("invalid: intent must be one of casual|serious|marriage")
	}
	out, err := s.store.SetIntent(ctx, userID, intent)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishProfileUpdated(ctx, userID, []string{"intent"})
	}
	s.InvalidatePulseCache(ctx, userID)
	return out, nil
}

func (s *Service) SetPaused(ctx context.Context, userID uuid.UUID, paused bool) (*store.Profile, error) {
	out, err := s.store.SetPaused(ctx, userID, paused)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishProfilePaused(ctx, userID, paused)
	}
	s.InvalidatePulseCache(ctx, userID)
	return out, nil
}

func (s *Service) DeleteProfile(ctx context.Context, userID uuid.UUID, reason string) error {
	if err := s.store.SoftDeleteProfile(ctx, userID); err != nil {
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishProfileDeleted(ctx, userID, reason)
	}
	s.InvalidatePulseCache(ctx, userID)
	return nil
}

func fieldsTouched(p store.UpsertProfileParams) []string {
	var out []string
	if p.Intent != nil {
		out = append(out, "intent")
	}
	if p.Bio != nil {
		out = append(out, "bio")
	}
	if p.Gender != nil {
		out = append(out, "gender")
	}
	if p.BirthDate != nil {
		out = append(out, "birth_date")
	}
	if p.City != nil {
		out = append(out, "city")
	}
	if p.State != nil {
		out = append(out, "state")
	}
	if p.Country != nil {
		out = append(out, "country")
	}
	if p.Latitude != nil {
		out = append(out, "latitude")
	}
	if p.Longitude != nil {
		out = append(out, "longitude")
	}
	if p.LocationGeohash != nil {
		out = append(out, "location_geohash")
	}
	if p.HeightCm != nil {
		out = append(out, "height_cm")
	}
	if p.Religion != nil {
		out = append(out, "religion")
	}
	if p.Community != nil {
		out = append(out, "community")
	}
	if p.Occupation != nil {
		out = append(out, "occupation")
	}
	if p.Education != nil {
		out = append(out, "education")
	}
	if p.Drinking != nil {
		out = append(out, "drinking")
	}
	if p.Smoking != nil {
		out = append(out, "smoking")
	}
	if p.Exercise != nil {
		out = append(out, "exercise")
	}
	if p.Diet != nil {
		out = append(out, "diet")
	}
	if p.WantsChildren != nil {
		out = append(out, "wants_children")
	}
	if p.FamilyPlans != nil {
		out = append(out, "family_plans")
	}
	if p.BlurMode != nil {
		out = append(out, "blur_mode")
	}
	if p.VisibleToPublic != nil {
		out = append(out, "visible_to_public")
	}
	if p.LanguagePrefs != nil {
		out = append(out, "language_prefs")
	}
	return out
}
