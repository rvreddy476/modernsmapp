package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// ErrUnderage is returned when a profile / spark / chat actor is under
// 18 by birth_date, or has no birth_date on file. P0-5 in
// PRODUCTION_GAP_ANALYSIS.md — adult-only platform.
var ErrUnderage = errors.New("dating requires a verified birth date and age 18+")

// MinDatingAgeYears is the absolute floor for dating discovery /
// sparks / chat. Server-enforced; never relax via preference.
const MinDatingAgeYears = 18

// ageYears returns the age in whole years derived from birthDate as of
// `at`. Mirrors PostgreSQL `EXTRACT(YEAR FROM AGE(...))` exactly.
func ageYears(birthDate time.Time, at time.Time) int {
	y := at.Year() - birthDate.Year()
	if at.Month() < birthDate.Month() ||
		(at.Month() == birthDate.Month() && at.Day() < birthDate.Day()) {
		y--
	}
	return y
}

// requireAdult returns ErrUnderage when the user's profile is missing
// a birth_date or computes to under 18. Used as a server-side gate
// before every action that requires adult status (profile activation,
// sparks, chat send, premium checkout, etc.).
func (s *Service) requireAdult(ctx context.Context, userID uuid.UUID) error {
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return err
	}
	if p == nil || p.BirthDate == nil {
		return ErrUnderage
	}
	if ageYears(*p.BirthDate, time.Now()) < MinDatingAgeYears {
		return ErrUnderage
	}
	return nil
}

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
	prior, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			existed = false
		}
	}
	// P0-5: profile activation requires birth_date + 18+. Compute the
	// effective birth_date from the merged (prior, incoming) state and
	// reject if it would activate an under-18 or missing-DOB profile.
	// The store enforces NOT NULL on birth_date for active rows via a
	// CHECK in migration 002, but defence-in-depth at the service
	// layer also gives a clean error code.
	effectiveDOB := p.BirthDate
	if effectiveDOB == nil && prior != nil {
		effectiveDOB = prior.BirthDate
	}
	if effectiveDOB == nil {
		return nil, ErrUnderage
	}
	if ageYears(*effectiveDOB, time.Now()) < MinDatingAgeYears {
		return nil, ErrUnderage
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
