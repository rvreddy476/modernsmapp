package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
// `at`, via the calendar-correct store.AgeOn.
func ageYears(birthDate time.Time, at time.Time) int {
	return store.AgeOn(birthDate, at)
}

// IdentityBasics is what identity holds for a user from registration.
type IdentityBasics struct {
	FirstName string
	BirthDate time.Time
}

// IdentityBasicsClient reads a user's registration birth date and first
// name service-to-service.
//
// Lane D2: no identity route returns DOB to a service caller today, so no
// production implementation is wired. Without one, UpsertProfile runs the
// interim rule: the client birth date is accepted once, then locked
// (dob_source=client), and the client first name is recorded as
// first_name_source=client. Wiring a client makes identity authoritative:
// its birth date and first name replace client values and the client
// birth_date field has no effect.
type IdentityBasicsClient interface {
	GetIdentityBasics(ctx context.Context, userID uuid.UUID) (*IdentityBasics, error)
}

// SetIdentityBasicsClient wires the identity reader.
func (s *Service) SetIdentityBasicsClient(c IdentityBasicsClient) {
	s.identityClient = c
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
//	paused                  -> nil when the remembered step is active (pause
//	                           only hides from discovery); otherwise the
//	                           onboarding error below
//	draft / pending_photo /
//	  pending_selfie         -> "invalid:" 400; onboarding incomplete
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
	case store.ProfileStatusActive:
		return nil
	case store.ProfileStatusPaused:
		if p.PriorStatus != nil && *p.PriorStatus == store.ProfileStatusActive {
			return nil
		}
		step := "unknown"
		if p.PriorStatus != nil {
			step = *p.PriorStatus
		}
		return fmt.Errorf("invalid: complete onboarding (profile_status=paused, step=%s) before sparking", step)
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

// UpsertProfile creates or updates the caller's profile.
//
// Birth date and first name (lane D2):
//   - identity client wired: identity's values are stored (dob_source /
//     first_name_source = identity) and the client birth_date has no effect;
//     an under-18 identity birth date is refused.
//   - interim (no client): the first client birth_date is stored as
//     dob_source=client and locked — later values are ignored. The client
//     first_name is stored as first_name_source=client.
//
// After the write the profile is walked forward through every onboarding
// step its evidence supports (advanceOnboarding).
func (s *Service) UpsertProfile(ctx context.Context, userID uuid.UUID, p store.UpsertProfileParams) (*store.Profile, error) {
	if p.Intent != nil && !validIntent(*p.Intent) {
		return nil, fmt.Errorf("invalid: intent must be one of casual|serious|marriage")
	}
	existed := true
	prior, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		if !errors.Is(err, store.ErrProfileNotFound) {
			return nil, err
		}
		existed = false
		prior = nil
	}

	var identity *IdentityBasics
	if s.identityClient != nil {
		identity, err = s.identityClient.GetIdentityBasics(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("identity basics: %w", err)
		}
		if identity == nil || identity.BirthDate.IsZero() {
			return nil, ErrUnderage
		}
	}

	// P0-5: the profile needs an 18+ birth date. Refused before anything
	// is written.
	dob, dobSource := effectiveBirthDate(prior, p.BirthDate, identity)
	if dob == nil || ageYears(*dob, time.Now()) < MinDatingAgeYears {
		return nil, ErrUnderage
	}

	if _, err := s.store.UpsertProfile(ctx, userID, p); err != nil {
		return nil, err
	}
	if dobSource != "" {
		if _, err := s.store.SetProfileBirthDate(ctx, userID, *dob, dobSource); err != nil {
			return nil, err
		}
	}
	if identity != nil && strings.TrimSpace(identity.FirstName) != "" {
		if _, err := s.store.SetProfileFirstName(ctx, userID, strings.TrimSpace(identity.FirstName), store.BasicsSourceIdentity); err != nil {
			return nil, err
		}
	} else if p.FirstName != nil && strings.TrimSpace(*p.FirstName) != "" {
		if _, err := s.store.SetProfileFirstName(ctx, userID, strings.TrimSpace(*p.FirstName), store.BasicsSourceClient); err != nil {
			return nil, err
		}
	}
	if p.Latitude != nil || p.Longitude != nil {
		if err := s.store.SetProfileGeohash(ctx, userID); err != nil {
			return nil, err
		}
	}

	out, err := s.advanceOnboarding(ctx, userID)
	if err != nil {
		return nil, err
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

// effectiveBirthDate picks the birth date to gate on and where to write it
// from. An empty source means nothing is written (a birth date already on
// file is locked).
func effectiveBirthDate(prior *store.Profile, client *time.Time, identity *IdentityBasics) (*time.Time, string) {
	if identity != nil {
		d := identity.BirthDate
		return &d, store.BasicsSourceIdentity
	}
	if prior != nil && prior.BirthDate != nil {
		return prior.BirthDate, ""
	}
	if client != nil {
		return client, store.BasicsSourceClient
	}
	return nil, ""
}

// advanceOnboarding walks the profile forward through every onboarding step
// the database evidence already supports (basics → approved primary photo →
// passed selfie), via the status machine as the system actor. A paused or
// held profile has only its remembered step advanced. Missing evidence ends
// the walk without error.
func (s *Service) advanceOnboarding(ctx context.Context, userID uuid.UUID) (*store.Profile, error) {
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 3; i++ {
		state := store.ProfileStatusState{Status: p.ProfileStatus, Paused: p.Paused}
		if p.PriorStatus != nil {
			state.Prior = *p.PriorStatus
		}
		ev, ok := store.OnboardingEventFrom(state.Base())
		if !ok || p.ProfileStatus == store.ProfileStatusDeleted {
			return p, nil
		}
		next, err := s.store.TransitionProfileStatus(ctx, userID, ev, store.ProfileActorSystem)
		switch {
		case errors.Is(err, store.ErrOnboardingIncomplete):
			return p, nil
		case errors.Is(err, store.ErrProfileTransitionNotAllowed):
			slog.Warn("profile state: onboarding step not allowed", "user_id", userID, "event", ev, "error", err)
			return p, nil
		case err != nil:
			return nil, err
		}
		p = next
	}
	return p, nil
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

// SetPaused is the user's pause toggle. Pause remembers the current step;
// unpause restores exactly that step and never lifts a moderation hold.
func (s *Service) SetPaused(ctx context.Context, userID uuid.UUID, paused bool) (*store.Profile, error) {
	ev := store.ProfileEventUnpause
	if paused {
		ev = store.ProfileEventPause
	}
	out, err := s.store.TransitionProfileStatus(ctx, userID, ev, store.ProfileActorUser)
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

// SetProfileHidden is the account-lifecycle hide/unhide (auth-service
// user.deactivated / user.deletion_scheduled and their reversals). It
// pauses or unpauses through the status machine as the lifecycle actor, so
// it never lifts a hold or skips an onboarding step, and on hide it drops
// the profile from every cached deck. A user without a dating profile is a
// no-op.
func (s *Service) SetProfileHidden(ctx context.Context, userID uuid.UUID, hidden bool) error {
	ev := store.ProfileEventUnpause
	if hidden {
		ev = store.ProfileEventPause
	}
	if _, err := s.store.TransitionProfileStatus(ctx, userID, ev, store.ProfileActorLifecycle); err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return nil
		}
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishProfilePaused(ctx, userID, hidden)
	}
	s.InvalidatePulseCache(ctx, userID)
	if hidden {
		s.InvalidateDecksForCandidate(ctx, userID)
	}
	return nil
}

func (s *Service) DeleteProfile(ctx context.Context, userID uuid.UUID, reason string) error {
	if _, err := s.store.TransitionProfileStatus(ctx, userID, store.ProfileEventDelete, store.ProfileActorUser); err != nil {
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
	if p.FirstName != nil {
		out = append(out, "first_name")
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
