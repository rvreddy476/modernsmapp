// Filters (mechanic M6, DATING_FILTERS_V2_ENABLED).
//
// Free for everyone: age range, a distance bucket and intent. With a pass:
// verified only, height, languages and the lifestyle basics. The pass
// filters are stored for anyone who sets them while holding a pass, and are
// applied to the deck only while the caller still holds one: a pass running
// out turns them off without forgetting them. A caller without a pass can
// clear them but not set them (403 FILTERS_REQUIRE_PASS).
//
// While the flag is on, the old free "verified only" privacy toggle is a
// pass filter too.
package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

var (
	// ErrFiltersRequirePass maps to 403 FILTERS_REQUIRE_PASS.
	ErrFiltersRequirePass = errors.New("forbidden: these filters come with a pass")
	// ErrInvalidDistanceBucket maps to 400 INVALID_DISTANCE_BUCKET.
	ErrInvalidDistanceBucket = errors.New("invalid: distance_bucket must be one of the profile options")
	// ErrInvalidHeightRange: min above max. Maps to 400 INVALID_HEIGHT.
	ErrInvalidHeightRange = refuseField("INVALID_HEIGHT", "min_height_cm", "min_height_cm must not be above max_height_cm",
		map[string]any{"min": MinHeightCm, "max": MaxHeightCm})
)

// distanceForBucket is the radius a distance bucket filter means.
func distanceForBucket(bucket string) (int, bool) {
	switch bucket {
	case "lt_5_km":
		return 5, true
	case "km_5_10":
		return 10, true
	case "km_10_25":
		return 25, true
	case "gt_25_km":
		return MaxDistanceKm, true
	}
	return 0, false
}

// bucketForDistance is the bucket a stored radius reads as.
func bucketForDistance(km int) string {
	switch {
	case km <= 0:
		return "km_10_25"
	case km <= 5:
		return "lt_5_km"
	case km <= 10:
		return "km_5_10"
	case km <= 25:
		return "km_10_25"
	}
	return "gt_25_km"
}

// PassFiltersView is the "pass_filters" member of GET /preferences.
type PassFiltersView struct {
	// Active: the caller holds a pass, so these filters apply to the deck.
	Active       bool     `json:"active"`
	VerifiedOnly bool     `json:"verified_only"`
	MinHeightCm  *int     `json:"min_height_cm,omitempty"`
	MaxHeightCm  *int     `json:"max_height_cm,omitempty"`
	Languages    []string `json:"languages"`
	Drinking     []string `json:"drinking"`
	Smoking      []string `json:"smoking"`
	Exercise     []string `json:"exercise"`
	Diet         []string `json:"diet"`
}

// PreferencesView is GET/PUT /preferences: the stored row, plus, while the
// flag is on, the distance bucket and the pass filters.
type PreferencesView struct {
	*store.Preferences
	DistanceBucket string           `json:"distance_bucket,omitempty"`
	PassFilters    *PassFiltersView `json:"pass_filters,omitempty"`
	// Dealbreakers (M12): present, possibly empty, while that flag is on.
	Dealbreakers *[]string `json:"dealbreakers,omitempty"`
}

// PassFiltersInput is the "pass_filters" member of PUT /preferences.
type PassFiltersInput struct {
	VerifiedOnly bool     `json:"verified_only"`
	MinHeightCm  *int     `json:"min_height_cm"`
	MaxHeightCm  *int     `json:"max_height_cm"`
	Languages    []string `json:"languages"`
	Drinking     []string `json:"drinking"`
	Smoking      []string `json:"smoking"`
	Exercise     []string `json:"exercise"`
	Diet         []string `json:"diet"`
}

// PreferencesInput is the PUT body: the existing fields plus M6's.
type PreferencesInput struct {
	store.UpsertPreferencesParams
	DistanceBucket *string           `json:"distance_bucket,omitempty"`
	PassFilters    *PassFiltersInput `json:"pass_filters,omitempty"`
	// Dealbreakers (M12) replaces the list when present.
	Dealbreakers *[]string `json:"dealbreakers,omitempty"`
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// holdsPass reports whether the user holds an unexpired pass; a failed
// lookup is "no" (a filter never applies by accident of an outage, and
// setting one is refused).
func (s *Service) holdsPass(ctx context.Context, userID uuid.UUID) bool {
	ok, err := s.store.IsPremium(ctx, userID)
	if err != nil {
		slog.Warn("filters: premium lookup failed; treating as no pass", "user_id", userID, "error", err)
		return false
	}
	return ok
}

// GetPreferencesView returns the preferences as GET /preferences shows them.
func (s *Service) GetPreferencesView(ctx context.Context, userID uuid.UUID) (*PreferencesView, error) {
	prefs, err := s.store.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &PreferencesView{Preferences: prefs}
	if s.mechanics.Dealbreakers {
		codes, err := s.store.GetDealbreakers(ctx, userID)
		if err != nil {
			return nil, err
		}
		out.Dealbreakers = &codes
	}
	if !s.mechanics.FiltersV2 {
		return out, nil
	}
	out.DistanceBucket = bucketForDistance(prefs.DistanceKm)
	f, err := s.store.GetPassFilters(ctx, userID)
	if err != nil {
		return nil, err
	}
	out.PassFilters = &PassFiltersView{
		Active: s.holdsPass(ctx, userID), VerifiedOnly: f.VerifiedOnly, MinHeightCm: f.MinHeightCm, MaxHeightCm: f.MaxHeightCm,
		Languages: nonNil(f.Languages), Drinking: nonNil(f.Drinking), Smoking: nonNil(f.Smoking), Exercise: nonNil(f.Exercise), Diet: nonNil(f.Diet),
	}
	return out, nil
}

// PutPreferences validates and stores a PUT /preferences.
func (s *Service) PutPreferences(ctx context.Context, userID uuid.UUID, in PreferencesInput) (*PreferencesView, error) {
	if (in.DistanceBucket != nil || in.PassFilters != nil) && !s.mechanics.FiltersV2 {
		return nil, ErrMechanicDisabled
	}
	if in.Dealbreakers != nil {
		if !s.mechanics.Dealbreakers {
			return nil, ErrMechanicDisabled
		}
		needsPass, err := validateDealbreakers(*in.Dealbreakers)
		if err != nil {
			return nil, err
		}
		if needsPass && !s.holdsPass(ctx, userID) {
			return nil, ErrDealbreakersRequirePass
		}
	}
	params := in.UpsertPreferencesParams
	// The old language_filter field writes the same column as the pass
	// filter, so while the flag is on it needs a pass too.
	if s.mechanics.FiltersV2 && len(params.LanguageFilter) > 0 && !s.holdsPass(ctx, userID) {
		return nil, ErrFiltersRequirePass
	}
	if in.DistanceBucket != nil {
		km, ok := distanceForBucket(*in.DistanceBucket)
		if !ok {
			return nil, ErrInvalidDistanceBucket
		}
		params.DistanceKm = &km
	}
	var pass *store.PassFilters
	if in.PassFilters != nil {
		f := store.PassFilters{
			VerifiedOnly: in.PassFilters.VerifiedOnly, MinHeightCm: in.PassFilters.MinHeightCm, MaxHeightCm: in.PassFilters.MaxHeightCm,
			Languages: in.PassFilters.Languages, Drinking: in.PassFilters.Drinking, Smoking: in.PassFilters.Smoking,
			Exercise: in.PassFilters.Exercise, Diet: in.PassFilters.Diet,
		}
		if err := validatePassFilters(f); err != nil {
			return nil, err
		}
		if !f.Empty() && !s.holdsPass(ctx, userID) {
			return nil, ErrFiltersRequirePass
		}
		pass = &f
	}
	if _, err := s.UpsertPreferences(ctx, userID, params); err != nil {
		return nil, err
	}
	if pass != nil {
		if err := s.store.SetPassFilters(ctx, userID, *pass); err != nil {
			return nil, err
		}
		s.InvalidatePulseCache(ctx, userID)
	}
	if in.Dealbreakers != nil {
		if err := s.store.SetDealbreakers(ctx, userID, *in.Dealbreakers); err != nil {
			return nil, err
		}
	}
	return s.GetPreferencesView(ctx, userID)
}

func validatePassFilters(f store.PassFilters) error {
	if err := checkHeight("min_height_cm", f.MinHeightCm); err != nil {
		return err
	}
	if err := checkHeight("max_height_cm", f.MaxHeightCm); err != nil {
		return err
	}
	if f.MinHeightCm != nil && f.MaxHeightCm != nil && *f.MinHeightCm > *f.MaxHeightCm {
		return ErrInvalidHeightRange
	}
	for _, c := range []struct {
		field, code string
		opts        []Option
		v           []string
	}{
		{"languages", "LANGUAGE", LanguageOptions, f.Languages},
		{"drinking", "LIFESTYLE", DrinkingOptions, f.Drinking},
		{"smoking", "LIFESTYLE", SmokingOptions, f.Smoking},
		{"exercise", "LIFESTYLE", ExerciseOptions, f.Exercise},
		{"diet", "LIFESTYLE", DietOptions, f.Diet},
	} {
		if err := checkCodes(c.field, c.code, c.opts, c.v, 0); err != nil {
			return err
		}
	}
	return nil
}

// applyDeckFilters adds the verified-only rule and, for a pass holder, the
// pass filters to a deck query (mechanic M6). With the flag off it keeps the
// pilot's free verified-only toggle.
func (s *Service) applyDeckFilters(ctx context.Context, viewerID uuid.UUID, q *store.CandidateQuery, privacyVerifiedOnly bool) {
	if !s.mechanics.FiltersV2 {
		q.VerifiedOnly = privacyVerifiedOnly
		return
	}
	if !s.holdsPass(ctx, viewerID) {
		q.VerifiedOnly = false
		return
	}
	f, err := s.store.GetPassFilters(ctx, viewerID)
	if err != nil {
		slog.Warn("filters: pass filters unreadable; deck without them", "viewer_id", viewerID, "error", err)
		q.VerifiedOnly = privacyVerifiedOnly
		return
	}
	q.VerifiedOnly = privacyVerifiedOnly || f.VerifiedOnly
	if f.MinHeightCm != nil {
		q.MinHeightCm = *f.MinHeightCm
	}
	if f.MaxHeightCm != nil {
		q.MaxHeightCm = *f.MaxHeightCm
	}
	q.Languages, q.Drinking, q.Smoking, q.Exercise, q.Diet = f.Languages, f.Drinking, f.Smoking, f.Exercise, f.Diet
}

// checkVerifiedOnlyToggle refuses switching the privacy "verified only"
// toggle on without a pass while the flag is on.
func (s *Service) checkVerifiedOnlyToggle(ctx context.Context, userID uuid.UUID, u store.PrivacyUpdate) error {
	if s.mechanics.FiltersV2 && u.VerifiedOnlyFilter != nil && *u.VerifiedOnlyFilter && !s.holdsPass(ctx, userID) {
		return ErrFiltersRequirePass
	}
	return nil
}
