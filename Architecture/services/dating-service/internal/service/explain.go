// §P1-2 — "Why am I seeing this profile?" transparency control.
//
// Returns a structured, human-safe list of reasons that the candidate
// surfaced in the viewer's deck. The reasons mirror the matcher's hard
// filters (age band overlap, gender preference) and the soft-ranking
// signals (distance, shared communities, shared interests) WITHOUT
// exposing the numeric score or any internal abuse / risk signals.
//
// Lane D7: explain answers only for a candidate in the viewer's current deck,
// counts every request against a daily allowance, and conveys distance as a
// bucket code and label only. Anyone else is CANDIDATE_UNAVAILABLE, so explain
// can no longer measure an arbitrary user.
//
// is_promoted reflects whether the candidate currently holds an active
// boost. Boost state lives in Redis as a TTL-gated rate-limit key
// (dating:boost:premium:<user_id>); the key's existence means the user
// boosted within the past 24h, which is exactly the "promoted right
// now" semantics the transparency UI needs to surface.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// CandidateExplanation is the payload returned by GET
// /v1/dating/pulse/:targetUserId/explain. Mobile + web both consume this.
//
// Lane D7: distance_km (a whole km) is gone. DistanceBucket is the bucket
// code (lt_5_km | km_5_10 | km_10_25 | gt_25_km) and DistanceLabel its display
// text; both are omitted when either side has no location.
type CandidateExplanation struct {
	Reasons        []ExplainReason `json:"reasons"`
	DistanceBucket string          `json:"distance_bucket,omitempty"`
	DistanceLabel  string          `json:"distance_label,omitempty"`
	IsPromoted     bool            `json:"is_promoted"`
}

// ExplainReason is one bullet rendered under the "Why am I seeing
// this profile?" sheet. Kind is one of:
//   - "age_match"        — candidate age sits within viewer's preference window.
//   - "distance"         — candidate is within the viewer's max radius (bucket label only).
//   - "gender_pref"      — candidate gender matches the viewer's interested_in_gender filter.
//   - "shared_community" — both viewer + candidate belong to one or more communities.
//   - "shared_interest"  — overlap in echo-cache topics/community slugs.
//   - "promoted"         — candidate currently holds an active boost.
type ExplainReason struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// ExplainCandidate builds the §P1-2 transparency explanation for
// (viewer -> target).
//
// Order: the request is counted against the viewer's daily allowance
// (*store.ExplainRateLimitError past it), then the target must be in the
// viewer's current deck — the cached one, or the deck computed for today —
// and still available (not blocked either way, not suspended or deleted).
// Otherwise ErrCandidateUnavailable, the one refusal used everywhere.
// Partial data (missing prefs, birth date, …) degrades to fewer reasons.
func (s *Service) ExplainCandidate(ctx context.Context, viewerID, targetID uuid.UUID) (*CandidateExplanation, error) {
	if viewerID == uuid.Nil || targetID == uuid.Nil {
		return nil, fmt.Errorf("invalid: viewer_id and target_user_id required")
	}
	if viewerID == targetID {
		return nil, fmt.Errorf("invalid: cannot explain self")
	}
	if err := s.store.ConsumeExplainQuota(ctx, viewerID, s.store.ExplainDailyLimit()); err != nil {
		return nil, err
	}
	inDeck, err := s.candidateInCurrentDeck(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	if !inDeck {
		return nil, ErrCandidateUnavailable
	}

	target, err := s.store.GetProfile(ctx, targetID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return nil, ErrCandidateUnavailable
		}
		return nil, err
	}
	// Lane D3: a suspended or deleted target, or a pair blocked either
	// way, is indistinguishable from a missing profile.
	if target.ProfileStatus == store.ProfileStatusSuspended || target.ProfileStatus == store.ProfileStatusDeleted {
		return nil, ErrCandidateUnavailable
	}
	if err := s.requireNotBlocked(ctx, viewerID, targetID); err != nil {
		return nil, err
	}
	viewer, err := s.store.GetProfile(ctx, viewerID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return nil, ErrCandidateUnavailable
		}
		return nil, err
	}

	prefs, err := s.store.GetPreferences(ctx, viewerID)
	if err != nil {
		// Best-effort: an empty prefs row will still drive most signals.
		slog.Warn("explain: load viewer preferences failed", "viewer_id", viewerID, "error", err)
		prefs = &store.Preferences{UserID: viewerID}
	}

	out := &CandidateExplanation{Reasons: []ExplainReason{}}

	// ---- distance (bucket only; the deck already applied the radius) ----
	if band, ok := store.DistanceBandBetween(viewer.Latitude, viewer.Longitude, target.Latitude, target.Longitude); ok {
		out.DistanceBucket, out.DistanceLabel = band.Code, band.Label
		out.Reasons = append(out.Reasons, ExplainReason{
			Kind:   "distance",
			Detail: formatDistanceReason(band),
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

// candidateInCurrentDeck reports whether targetID is a card in the viewer's
// current deck: the cached deck, or (on a miss) the deck computed for today,
// which GetPulseToday caches. A gated viewer's deck is empty.
func (s *Service) candidateInCurrentDeck(ctx context.Context, viewerID, targetID uuid.UUID) (bool, error) {
	deck, err := s.GetPulseToday(ctx, viewerID)
	if err != nil {
		return false, err
	}
	for _, card := range deck.Data {
		if card.CandidateID == targetID {
			return true, nil
		}
	}
	return false, nil
}

// formatDistanceReason names the bucket, never a km figure.
func formatDistanceReason(band store.DistanceBand) string {
	if band.Code == store.DistanceBucketUnder5 {
		return "Less than 5 km away, inside your distance preference."
	}
	return band.Label + " away, inside your distance preference."
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

// ageFromBirthDate computes whole-year age today via store.AgeOn, the one
// calendar-correct age function shared with store.CandidateProfile.Age.
func ageFromBirthDate(birth time.Time) int {
	return store.AgeOn(birth, time.Now())
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
