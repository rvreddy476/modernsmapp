// §P1-2 — "Why am I seeing this profile?" transparency control.
//
// Returns a structured, human-safe list of reasons that the candidate
// surfaced in the viewer's deck. The reasons mirror the matcher's hard
// filters (age band overlap, gender preference) and the soft-ranking
// signals (distance, shared communities, shared interests) WITHOUT
// exposing the numeric score or any internal abuse / risk signals.
//
// is_promoted reflects whether the candidate currently holds an active
// boost. Boost state lives in Redis as a TTL-gated rate-limit key
// (dating:boost:premium:<user_id>); the key's existence means the user
// boosted within the past 24h, which is exactly the "promoted right
// now" semantics the transparency UI needs to surface.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// CandidateExplanation is the payload returned by GET
// /v1/dating/pulse/:targetUserId/explain. Shape locked — mobile +
// web both consume this.
type CandidateExplanation struct {
	Reasons     []ExplainReason `json:"reasons"`
	DistanceKm  int             `json:"distance_km"`
	IsPromoted  bool            `json:"is_promoted"`
}

// ExplainReason is one bullet rendered under the "Why am I seeing
// this profile?" sheet. Kind is one of:
//   - "age_match"        — candidate age sits within viewer's preference window.
//   - "distance"         — candidate is within the viewer's max radius.
//   - "gender_pref"      — candidate gender matches the viewer's interested_in_gender filter.
//   - "shared_community" — both viewer + candidate belong to one or more communities.
//   - "shared_interest"  — overlap in echo-cache topics/community slugs.
//   - "promoted"         — candidate currently holds an active boost.
type ExplainReason struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// ExplainCandidate builds the §P1-2 transparency explanation for
// (viewer -> target). All inputs come from the same stores the
// matcher consults, so the explanation stays consistent with the
// deck-generation outcome.
//
// Errors are returned ONLY when both profiles are entirely missing
// — partial data (missing prefs, missing target birthdate, etc.)
// degrades to fewer reasons rather than failing the call. The UI
// will render whatever subset is available.
func (s *Service) ExplainCandidate(ctx context.Context, viewerID, targetID uuid.UUID) (*CandidateExplanation, error) {
	if viewerID == uuid.Nil || targetID == uuid.Nil {
		return nil, fmt.Errorf("invalid: viewer_id and target_user_id required")
	}
	if viewerID == targetID {
		return nil, fmt.Errorf("invalid: cannot explain self")
	}

	viewer, vErr := s.store.GetProfile(ctx, viewerID)
	target, tErr := s.store.GetProfile(ctx, targetID)
	if vErr != nil && tErr != nil {
		return nil, fmt.Errorf("not_found: profile not found")
	}
	if target == nil {
		return nil, fmt.Errorf("not_found: target profile not found")
	}

	prefs, err := s.store.GetPreferences(ctx, viewerID)
	if err != nil {
		// Best-effort: an empty prefs row will still drive most signals.
		slog.Warn("explain: load viewer preferences failed", "viewer_id", viewerID, "error", err)
		prefs = &store.Preferences{UserID: viewerID}
	}

	out := &CandidateExplanation{Reasons: []ExplainReason{}}

	// ---- distance ----
	dist := computeDistanceKm(viewer, target)
	out.DistanceKm = capDistanceKm(dist, prefs.DistanceKm)
	if dist >= 0 && prefs.DistanceKm > 0 && dist <= float64(prefs.DistanceKm) {
		out.Reasons = append(out.Reasons, ExplainReason{
			Kind:   "distance",
			Detail: formatDistanceReason(out.DistanceKm, prefs.DistanceKm),
		})
	}

	// ---- age band overlap ----
	if ageReason, ok := buildAgeReason(target, prefs); ok {
		out.Reasons = append(out.Reasons, ageReason)
	}

	// ---- gender preference ----
	if genderReason, ok := buildGenderReason(target, prefs); ok {
		out.Reasons = append(out.Reasons, genderReason)
	}

	// ---- shared communities + interests (via GraphProvider) ----
	provider := s.graphProvider
	if provider == nil {
		provider = matcher.NewStaticGraphProvider()
	}
	// Communities Jaccard > 0 → mention "shared community". We don't
	// list the actual community names here — the GraphProvider
	// interface only returns the overlap score, not the slug set, and
	// re-fetching the slug list crosses a service boundary the matcher
	// is intentionally pure about. Detail says how strong the overlap
	// is in plain words rather than a number.
	if commOverlap := provider.CommunitiesOverlap(ctx, viewerID, targetID); commOverlap > 0 {
		out.Reasons = append(out.Reasons, ExplainReason{
			Kind:   "shared_community",
			Detail: formatOverlap("community", commOverlap),
		})
	}

	// ---- shared interests (echo-cache topics ∩) ----
	if interestReason, ok := s.buildSharedInterestReason(ctx, viewerID, targetID); ok {
		out.Reasons = append(out.Reasons, interestReason)
	}

	// ---- promoted? ----
	out.IsPromoted = s.isBoostActive(ctx, targetID)
	if out.IsPromoted {
		out.Reasons = append(out.Reasons, ExplainReason{
			Kind:   "promoted",
			Detail: "This profile is currently promoted — they boosted their profile.",
		})
	}

	return out, nil
}

// computeDistanceKm returns the geodesic distance between viewer and
// target in km, or -1 if either side lacks coordinates.
func computeDistanceKm(viewer, target *store.Profile) float64 {
	if viewer == nil || target == nil {
		return -1
	}
	if viewer.Latitude == nil || viewer.Longitude == nil ||
		target.Latitude == nil || target.Longitude == nil {
		return -1
	}
	return store.DistanceKm(*viewer.Latitude, *viewer.Longitude, *target.Latitude, *target.Longitude)
}

// capDistanceKm rounds the raw distance to the nearest km, then caps
// at the viewer's max radius. -1 (unknown) returns 0 so the response
// shape stays integer.
func capDistanceKm(raw float64, maxKm int) int {
	if raw < 0 {
		return 0
	}
	rounded := int(math.Round(raw))
	if rounded < 0 {
		rounded = 0
	}
	if maxKm > 0 && rounded > maxKm {
		rounded = maxKm
	}
	return rounded
}

func formatDistanceReason(distKm, maxKm int) string {
	switch {
	case distKm <= 1:
		return "Lives less than 1 km away — well inside your distance preference."
	case distKm < 5:
		return fmt.Sprintf("About %d km away, well inside your %d km preference.", distKm, maxKm)
	default:
		return fmt.Sprintf("About %d km away, inside your %d km preference.", distKm, maxKm)
	}
}

// buildAgeReason emits an "age_match" reason iff the target's age is
// known and falls inside the viewer's [min,max] preference window.
func buildAgeReason(target *store.Profile, prefs *store.Preferences) (ExplainReason, bool) {
	if target == nil || target.BirthDate == nil {
		return ExplainReason{}, false
	}
	age := ageFromBirthDate(*target.BirthDate)
	if age <= 0 {
		return ExplainReason{}, false
	}
	min, max := 0, 0
	if prefs != nil {
		if prefs.MinAge != nil {
			min = *prefs.MinAge
		}
		if prefs.MaxAge != nil {
			max = *prefs.MaxAge
		}
	}
	if min == 0 && max == 0 {
		// No age preference set — viewer hasn't filtered, so the
		// candidate is in band by definition. Still surface the
		// reason so the user understands they came up because of
		// the implicit "no restriction" filter.
		return ExplainReason{
			Kind:   "age_match",
			Detail: fmt.Sprintf("They're %d. Your age filter is set to show everyone 18+.", age),
		}, true
	}
	if min > 0 && age < min {
		return ExplainReason{}, false
	}
	if max > 0 && age > max {
		return ExplainReason{}, false
	}
	return ExplainReason{
		Kind:   "age_match",
		Detail: fmt.Sprintf("They're %d, inside your %d–%d age preference.", age, min, max),
	}, true
}

// ageFromBirthDate computes whole-year age. Mirrors store.CandidateProfile.Age
// but takes a value type so we don't need a candidate projection here.
func ageFromBirthDate(birth time.Time) int {
	if birth.IsZero() {
		return 0
	}
	now := time.Now()
	age := now.Year() - birth.Year()
	if now.YearDay() < birth.YearDay() {
		age--
	}
	if age < 0 {
		return 0
	}
	return age
}

// buildGenderReason emits a "gender_pref" reason iff the viewer set
// an interested_in_gender filter and the target matches it.
func buildGenderReason(target *store.Profile, prefs *store.Preferences) (ExplainReason, bool) {
	if target == nil || target.Gender == nil || prefs == nil || prefs.InterestedInGender == nil {
		return ExplainReason{}, false
	}
	if *target.Gender != *prefs.InterestedInGender {
		return ExplainReason{}, false
	}
	return ExplainReason{
		Kind:   "gender_pref",
		Detail: fmt.Sprintf("Their gender matches your %q preference.", *prefs.InterestedInGender),
	}, true
}

// buildSharedInterestReason intersects the viewer + target echo-cache
// topic sets and emits a "shared_interest" reason when at least one
// topic overlaps. Detail names up to three shared topics — never
// surfaces internal counts or scores.
func (s *Service) buildSharedInterestReason(ctx context.Context, viewerID, targetID uuid.UUID) (ExplainReason, bool) {
	viewerEcho, _ := s.store.GetEchoCache(ctx, viewerID)
	targetEcho, _ := s.store.GetEchoCache(ctx, targetID)
	if viewerEcho == nil || targetEcho == nil {
		return ExplainReason{}, false
	}
	shared := intersectStrings(viewerEcho.EchoTopics(), targetEcho.EchoTopics())
	if len(shared) == 0 {
		return ExplainReason{}, false
	}
	return ExplainReason{
		Kind:   "shared_interest",
		Detail: formatSharedInterests(shared),
	}, true
}

func intersectStrings(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(b))
	added := make(map[string]struct{}, len(b))
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			continue
		}
		if _, dup := added[s]; dup {
			continue
		}
		out = append(out, s)
		added[s] = struct{}{}
	}
	return out
}

func formatSharedInterests(topics []string) string {
	switch len(topics) {
	case 0:
		return "You both engage with shared topics on AtPost."
	case 1:
		return fmt.Sprintf("You both engage with %s on AtPost.", topics[0])
	case 2:
		return fmt.Sprintf("You both engage with %s and %s on AtPost.", topics[0], topics[1])
	default:
		return fmt.Sprintf("You both engage with %s, %s, and %s on AtPost.", topics[0], topics[1], topics[2])
	}
}

func formatOverlap(kind string, score float64) string {
	switch {
	case score >= 0.5:
		return fmt.Sprintf("You share several %s memberships on AtPost.", kind)
	case score >= 0.2:
		return fmt.Sprintf("You share a few %s memberships on AtPost.", kind)
	default:
		return fmt.Sprintf("You share at least one %s membership on AtPost.", kind)
	}
}

// isBoostActive reports whether `candidate` currently holds an
// active premium-daily boost. The rate-limit key has a 24h TTL so
// its existence == "boosted in the past 24h" == promoted in the
// transparency-UI sense.
//
// Boost tokens redeemed via the one-shot flow (boost_49 plan) are
// not currently TTL-tracked — they invalidate the cache and exit.
// Phase B can add a separate `dating:boost:active:<id>` marker.
func (s *Service) isBoostActive(ctx context.Context, candidate uuid.UUID) bool {
	if s.rdb == nil {
		return false
	}
	n, err := s.rdb.Exists(ctx, boostRateLimitKey(candidate)).Result()
	if err != nil {
		slog.Warn("explain: boost lookup failed", "candidate_id", candidate, "error", err)
		return false
	}
	return n > 0
}
