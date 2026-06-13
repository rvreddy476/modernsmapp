package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// ErrUnderage is returned when a profile / spark / chat actor is under
// 18 by birth_date, or has no birth_date on file. P0-5 in
// PRODUCTION_GAP_ANALYSIS.md — adult-only platform.
var ErrUnderage = errors.New("dating requires a verified birth date and age 18+")

// ErrProfileRestricted is returned when a moderator-restricted profile
// attempts to take an outbound action (new spark, new chat). The
// "forbidden:" prefix maps to a 403 in the HTTP layer. §P1-1.
var ErrProfileRestricted = errors.New("forbidden: your profile is restricted and cannot start new sparks or chats")

// ErrProfileSuspended is returned when a suspended or deleted profile
// attempts ANY interactive action. Maps to a 403. §P1-1.
var ErrProfileSuspended = errors.New("forbidden: your profile is suspended and cannot interact")

// ErrProfilePendingReview is returned when a profile flagged for manual
// moderator inspection (risk score >= 86 / admin action="review")
// attempts an outbound action. §P1-1. Maps to 403.
var ErrProfilePendingReview = errors.New("forbidden: your profile is under review and cannot start new sparks or chats")

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

// requireInteractiveProfile gates the calling user's profile_status for
// outbound actions (new spark, new chat / match formation). §P1-1.
//
// Mapping of profile_status → return value:
//
//	active                  -> nil (allowed)
//	paused                  -> nil (paused users can still spark — pause only
//	                            hides them from discovery; legacy boolean rules)
//	draft / pending_photo /
//	  pending_selfie         -> ErrUnderage-style 400; onboarding incomplete,
//	                            handled by the existing onboarding flow
//	pending_review           -> ErrProfilePendingReview
//	restricted               -> ErrProfileRestricted
//	suspended / deleted      -> ErrProfileSuspended
//
// Missing profile (ErrProfileNotFound) is treated as "no row to gate"
// and returns nil — the caller's adult check + the store.CreateSpark
// FK constraint will catch it. We don't synthesise a row here.
func (s *Service) requireInteractiveProfile(ctx context.Context, userID uuid.UUID) error {
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return nil
		}
		return err
	}
	if p == nil {
		return nil
	}
	switch p.ProfileStatus {
	case store.ProfileStatusActive, store.ProfileStatusPaused:
		return nil
	case store.ProfileStatusPendingReview:
		return ErrProfilePendingReview
	case store.ProfileStatusRestricted:
		return ErrProfileRestricted
	case store.ProfileStatusSuspended, store.ProfileStatusDeleted:
		return ErrProfileSuspended
	default:
		// draft / pending_photo / pending_selfie — onboarding still in
		// progress. Returning ErrUnderage here would be wrong (the user
		// may well be an adult), but they cannot spark before their
		// profile is active. Surface as 400 via the "invalid:" prefix.
		return fmt.Errorf("invalid: complete onboarding (profile_status=%s) before sparking", p.ProfileStatus)
	}
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

	// §P1-1 lifecycle: graduate draft -> pending_photo when the minimum
	// onboarding fields are populated. Once past draft we never demote
	// back here — the photo / selfie consumers and trust-safety drive
	// the rest of the state machine.
	if out.ProfileStatus == store.ProfileStatusDraft && hasMinimumOnboardingFields(out) {
		if updated, err := s.store.SetProfileStatus(ctx, userID, store.ProfileStatusPendingPhoto); err == nil {
			out = updated
		} else {
			slog.Warn("profile state: graduate to pending_photo failed", "user_id", userID, "error", err)
		}
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

// hasMinimumOnboardingFields returns true once a profile carries the
// fields required to move out of 'draft'. Per §P1-1 the threshold is
// first_name (best-effort — not user-writable through this service yet)
// + birth_date + intent + gender + city. first_name is treated as
// optional because the value is currently sourced from user-service via
// LookupFirstName rather than the dating UpsertProfile contract.
func hasMinimumOnboardingFields(p *store.Profile) bool {
	if p == nil {
		return false
	}
	if p.BirthDate == nil {
		return false
	}
	if p.Intent == "" {
		return false
	}
	if p.Gender == nil || *p.Gender == "" {
		return false
	}
	if p.City == nil || *p.City == "" {
		return false
	}
	return true
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
	// Phase 1 §3: paused profile must drop out of every viewer's cached
	// deck. Restoring (paused=false) doesn't need fan-out — the viewer
	// gets us on their next refresh anyway.
	if paused {
		s.InvalidateDecksForCandidate(ctx, userID)
	}
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
	s.InvalidateDecksForCandidate(ctx, userID)
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
