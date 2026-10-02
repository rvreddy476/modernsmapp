// Dealbreakers (mechanic M12, DATING_DEALBREAKERS_ENABLED), and the "would
// they see you too" rules shared with mutual picks (M7).
//
// A user marks some of their preferences as dealbreakers. A dealbreaker
// works both ways: the user's deck and picks already keep to their own
// preferences, and now nobody who fails one of the user's dealbreakers is
// shown the user either. Age, distance and intent are free; the pass filters
// (verified only, height, languages, the lifestyle basics) can be
// dealbreakers only for a pass holder, and count only while they hold one.
//
// A viewer who has not filled a field a dealbreaker asks about does not pass
// it (the pass filters treat a missing value the same way); an unknown age or
// location is given the benefit of the doubt, as the deck does.
package service

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Dealbreaker codes. The free ones first, then the pass ones.
const (
	DealbreakerAge       = "age"
	DealbreakerDistance  = "distance"
	DealbreakerIntent    = "intent"
	DealbreakerVerified  = "verified"
	DealbreakerHeight    = "height"
	DealbreakerLanguages = "languages"
	DealbreakerDrinking  = "drinking"
	DealbreakerSmoking   = "smoking"
	DealbreakerExercise  = "exercise"
	DealbreakerDiet      = "diet"
)

var (
	freeDealbreakers = []string{DealbreakerAge, DealbreakerDistance, DealbreakerIntent}
	passDealbreakers = []string{DealbreakerVerified, DealbreakerHeight, DealbreakerLanguages,
		DealbreakerDrinking, DealbreakerSmoking, DealbreakerExercise, DealbreakerDiet}

	// ErrDealbreakersRequirePass maps to 403 DEALBREAKERS_REQUIRE_PASS.
	ErrDealbreakersRequirePass = errors.New("forbidden: these dealbreakers come with a pass")
	// ErrInvalidDealbreaker maps to 400 INVALID_DEALBREAKER.
	ErrInvalidDealbreaker = refuseField("INVALID_DEALBREAKER", "dealbreakers", "dealbreakers must be distinct codes from the allowed list",
		map[string]any{"allowed": append(append([]string{}, freeDealbreakers...), passDealbreakers...)})
)

// validateDealbreakers checks the codes and reports whether any needs a pass.
func validateDealbreakers(codes []string) (needsPass bool, err error) {
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			return false, ErrInvalidDealbreaker
		}
		seen[c] = true
		switch {
		case slices.Contains(freeDealbreakers, c):
		case slices.Contains(passDealbreakers, c):
			needsPass = true
		default:
			return false, ErrInvalidDealbreaker
		}
	}
	return needsPass, nil
}

// addsPassDealbreaker reports whether codes holds a pass dealbreaker that
// current does not.
func addsPassDealbreaker(codes, current []string) bool {
	for _, c := range codes {
		if slices.Contains(passDealbreakers, c) && !slices.Contains(current, c) {
			return true
		}
	}
	return false
}

// viewerFacts is what the candidate-side rules look at on the viewer.
type viewerFacts struct {
	Age       int
	Intent    string
	Verified  bool
	Lat, Lon  *float64
	HeightCm  *int
	Languages []string
	Drinking  *string
	Smoking   *string
	Exercise  *string
	Diet      *string
}

func factsOf(p *store.Profile) viewerFacts {
	if p == nil {
		return viewerFacts{}
	}
	v := viewerFacts{
		Intent: p.Intent, Verified: verifiedTier(p.TrustTier), Lat: p.Latitude, Lon: p.Longitude,
		HeightCm: p.HeightCm, Languages: p.LanguagePrefs,
		Drinking: p.Drinking, Smoking: p.Smoking, Exercise: p.Exercise, Diet: p.Diet,
	}
	if p.BirthDate != nil {
		v.Age = store.AgeOn(*p.BirthDate, time.Now())
	}
	return v
}

// passesTheir reports whether the viewer passes the candidate's preference
// named by code. cLat/cLon is the candidate's point.
func passesTheir(code string, t store.TheirPreferences, v viewerFacts, cLat, cLon *float64) bool {
	in := func(set []string, val *string) bool {
		return len(set) == 0 || (val != nil && slices.Contains(set, *val))
	}
	switch code {
	case DealbreakerAge:
		return v.Age <= 0 || ((t.MinAge <= 0 || v.Age >= t.MinAge) && (t.MaxAge <= 0 || v.Age <= t.MaxAge))
	case DealbreakerIntent:
		return len(t.IntentFilter) == 0 || v.Intent == "" || slices.Contains(t.IntentFilter, v.Intent)
	case DealbreakerDistance:
		km := t.DistanceKm
		if km <= 0 {
			km = 25 // the default radius, as in the deck
		}
		if v.Lat == nil || v.Lon == nil || cLat == nil || cLon == nil {
			return true
		}
		return store.SnappedDistanceKm(*v.Lat, *v.Lon, *cLat, *cLon) < float64(store.EffectiveDiscoveryRadiusKm(km))
	case DealbreakerVerified:
		wants := t.PrivacyVerifiedOnly || (t.HoldsPass && t.Pass.VerifiedOnly)
		return !wants || v.Verified
	}
	// The rest are pass filters: they count only while the candidate holds
	// a pass.
	if !t.HoldsPass {
		return true
	}
	switch code {
	case DealbreakerHeight:
		if t.Pass.MinHeightCm == nil && t.Pass.MaxHeightCm == nil {
			return true
		}
		if v.HeightCm == nil {
			return false
		}
		return (t.Pass.MinHeightCm == nil || *v.HeightCm >= *t.Pass.MinHeightCm) &&
			(t.Pass.MaxHeightCm == nil || *v.HeightCm <= *t.Pass.MaxHeightCm)
	case DealbreakerLanguages:
		if len(t.Pass.Languages) == 0 {
			return true
		}
		for _, l := range v.Languages {
			if slices.Contains(t.Pass.Languages, l) {
				return true
			}
		}
		return false
	case DealbreakerDrinking:
		return in(t.Pass.Drinking, v.Drinking)
	case DealbreakerSmoking:
		return in(t.Pass.Smoking, v.Smoking)
	case DealbreakerExercise:
		return in(t.Pass.Exercise, v.Exercise)
	case DealbreakerDiet:
		return in(t.Pass.Diet, v.Diet)
	}
	return true
}

// admitsViewer is mutual picks' check: the candidate's age range, intents,
// distance and verified-only wish must all let the viewer through, whether
// or not they are dealbreakers.
func admitsViewer(t store.TheirPreferences, v viewerFacts, cLat, cLon *float64) bool {
	for _, code := range []string{DealbreakerAge, DealbreakerIntent, DealbreakerVerified, DealbreakerDistance} {
		if !passesTheir(code, t, v, cLat, cLon) {
			return false
		}
	}
	return true
}

// passesTheirDealbreakers is the dealbreakers check: every preference the
// candidate marked as a dealbreaker must let the viewer through. A pass
// dealbreaker left over from an expired pass counts for nothing.
func passesTheirDealbreakers(t store.TheirPreferences, v viewerFacts, cLat, cLon *float64) bool {
	for _, code := range t.Dealbreakers {
		if !passesTheir(code, t, v, cLat, cLon) {
			return false
		}
	}
	return true
}

// dropByTheirDealbreakers removes the candidates whose dealbreakers the
// viewer fails. With the flag off it returns candidates unchanged. A failed
// lookup keeps nobody out rather than emptying the deck.
func (s *Service) dropByTheirDealbreakers(ctx context.Context, viewer *store.Profile, candidates []store.CandidateProfile) []store.CandidateProfile {
	if !s.mechanics.Dealbreakers || len(candidates) == 0 {
		return candidates
	}
	ids := make([]uuid.UUID, len(candidates))
	for i := range candidates {
		ids[i] = candidates[i].UserID
	}
	theirs, err := s.store.TheirPreferencesOf(ctx, ids)
	if err != nil {
		return candidates
	}
	v := factsOf(viewer)
	out := candidates[:0:0]
	for _, c := range candidates {
		if passesTheirDealbreakers(theirs[c.UserID], v, c.Latitude, c.Longitude) {
			out = append(out, c)
		}
	}
	return out
}
