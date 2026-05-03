// Package matcher implements the Pulse matching algorithm v1 (spec §9.2).
//
// Score(viewer, candidate) -> [0..1] is the weighted sum of eight component
// signals. Each component is range-clamped to [0..1] before weighting so the
// final score is too. The package is intentionally pure (no DB, no HTTP) —
// callers are responsible for assembling viewer + candidate state.
//
// The MatchReason summaries returned alongside the score are derived from the
// top-3 weighted contributors and converted to short, human-readable strings
// suitable for direct display in the Pulse card.
package matcher

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Component weights from spec §9.2. They sum to 1.0.
const (
	WeightTune      = 0.25
	WeightGraph     = 0.20
	WeightContent   = 0.15
	WeightIntent    = 0.10
	WeightRecency   = 0.10
	WeightProximity = 0.10
	WeightTrust     = 0.05
	WeightDiversity = 0.05
)

// MatchReason is one bullet displayed under a candidate card.
type MatchReason struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

// ViewerContext bundles the viewer-side inputs Score needs.
type ViewerContext struct {
	UserID               uuid.UUID
	Intent               string // "casual" | "serious" | "marriage" — defaults to "casual"
	Tune                 *store.Tune
	EchoCache            *store.EchoCache
	Latitude             *float64
	Longitude            *float64
	History7DCommunities map[string]struct{} // for diversity bonus
	GraphProvider        GraphProvider
}

// GraphProvider abstracts the graph + community lookup so matcher can stay
// pure; the production wiring calls graph-service / community-service over
// HTTP, tests can inject a stub.
type GraphProvider interface {
	// FollowsOverlap returns Jaccard overlap of viewer's & candidate's
	// follow sets, in [0..1]. Returns 0 if either lookup fails — callers
	// SHOULD log warnings, not propagate the error.
	FollowsOverlap(ctx context.Context, viewer, candidate uuid.UUID) float64
	// CommunitiesOverlap is the Jaccard overlap of community memberships.
	CommunitiesOverlap(ctx context.Context, viewer, candidate uuid.UUID) float64
}

// Score computes the matching score and reasons for (viewer -> candidate).
// Always returns a usable score even if some sub-signals fail (resilience is
// a hard requirement — Pulse must keep working when downstreams hiccup).
func Score(ctx context.Context, vc ViewerContext, cand *store.CandidateProfile, candEcho *store.EchoCache) (float64, []MatchReason, error) {
	if cand == nil {
		return 0, nil, fmt.Errorf("invalid: candidate is nil")
	}

	tune := tuneAlignment(vc.Tune, candTune(cand))
	graph := atpostGraphOverlap(ctx, vc, cand.UserID)
	content := contentTasteOverlap(vc.EchoCache, candEcho)
	intent := intentAlignment(viewerIntent(vc), cand.Intent)
	recency := recencyFreshness(cand.LastActiveAt)
	proximity := geographicProximity(distanceKm(vc, cand))
	trust := trustFactor(cand.TrustTier)
	diversity := diversityBonus(vc.History7DCommunities, cand)

	score :=
		WeightTune*tune +
			WeightGraph*graph +
			WeightContent*content +
			WeightIntent*intent +
			WeightRecency*recency +
			WeightProximity*proximity +
			WeightTrust*trust +
			WeightDiversity*diversity

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	reasons := buildReasons([]componentScore{
		{kind: "tune", value: tune, weight: WeightTune, summary: tuneSummary(vc.Tune, candTune(cand))},
		{kind: "community", value: graph, weight: WeightGraph, summary: graphSummary(graph)},
		{kind: "qa_topic", value: content, weight: WeightContent, summary: contentSummary(vc.EchoCache, candEcho)},
		{kind: "intent", value: intent, weight: WeightIntent, summary: intentSummary(viewerIntent(vc), cand.Intent)},
		{kind: "recency", value: recency, weight: WeightRecency, summary: "Recently active on AtPost"},
		{kind: "proximity", value: proximity, weight: WeightProximity, summary: proximitySummary(distanceKm(vc, cand))},
		{kind: "trust", value: trust, weight: WeightTrust, summary: trustSummary(cand.TrustTier)},
		{kind: "diversity", value: diversity, weight: WeightDiversity, summary: "Brings something fresh to your day"},
	})

	return score, reasons, nil
}

// candTune extracts a Tune view from a CandidateProfile (the candidate query
// joins dating_tunes so we already have the columns). Returns nil if the
// candidate hasn't filled a Tune.
func candTune(c *store.CandidateProfile) *store.Tune {
	if c == nil {
		return nil
	}
	if c.LifestyleRhythm == nil && c.ConversationStyle == nil &&
		c.FaithWeight == nil && c.FamilyWeight == nil &&
		c.RegionWeight == nil && c.FamilyPlansAxis == nil &&
		c.EducationAxis == nil {
		return nil
	}
	return &store.Tune{
		UserID:            c.UserID,
		LifestyleRhythm:   c.LifestyleRhythm,
		ConversationStyle: c.ConversationStyle,
		FaithWeight:       c.FaithWeight,
		FamilyWeight:      c.FamilyWeight,
		RegionWeight:      c.RegionWeight,
		FamilyPlansAxis:   c.FamilyPlansAxis,
		EducationAxis:     c.EducationAxis,
	}
}

func viewerIntent(vc ViewerContext) string {
	if vc.Intent == "" {
		return "casual"
	}
	return vc.Intent
}

// IntentAlignmentByPair is exported so callers (e.g. the service layer or
// tests) can compute the intent component without going through Score.
func IntentAlignmentByPair(a, b string) float64 { return intentAlignment(a, b) }

// distanceKm returns 0 if either coord set is missing — proximity then
// resolves to 1.0/(1+0/15) = 1, but we guard upstream with a hard filter
// already so 0 means "we don't know" and the candidate gets the benefit
// of the doubt rather than being penalised.
func distanceKm(vc ViewerContext, cand *store.CandidateProfile) float64 {
	if vc.Latitude == nil || vc.Longitude == nil ||
		cand.Latitude == nil || cand.Longitude == nil {
		return 0
	}
	return store.DistanceKm(*vc.Latitude, *vc.Longitude, *cand.Latitude, *cand.Longitude)
}

// --- tune alignment --------------------------------------------------------

// tuneAlignment computes cosine similarity over the five numeric axes of the
// Tune. conversation_style is folded in as a one-hot bonus: identical styles
// add 1 to both vectors before the cosine is computed.
//
// Returns 0..1. If either side has no Tune at all, returns 0.5 (neutral) so
// users haven't filled their Tune don't get punished disproportionately.
func tuneAlignment(a, b *store.Tune) float64 {
	if a == nil || b == nil {
		return 0.5
	}
	axes := [][2]*int{
		{a.LifestyleRhythm, b.LifestyleRhythm},
		{a.FaithWeight, b.FaithWeight},
		{a.FamilyWeight, b.FamilyWeight},
		{a.RegionWeight, b.RegionWeight},
		{a.FamilyPlansAxis, b.FamilyPlansAxis},
	}
	va := make([]float64, 0, 6)
	vb := make([]float64, 0, 6)
	for _, axis := range axes {
		if axis[0] == nil || axis[1] == nil {
			continue
		}
		va = append(va, float64(*axis[0]))
		vb = append(vb, float64(*axis[1]))
	}
	// Conversation style as a one-hot bonus.
	if a.ConversationStyle != nil && b.ConversationStyle != nil {
		match := 0.0
		if *a.ConversationStyle == *b.ConversationStyle {
			match = 1.0
		}
		va = append(va, 1.0)
		vb = append(vb, match)
	}
	if len(va) == 0 {
		return 0.5
	}
	return cosine(va, vb)
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func tuneSummary(a, b *store.Tune) string {
	if a == nil || b == nil {
		return "Compatible vibes"
	}
	if a.ConversationStyle != nil && b.ConversationStyle != nil && *a.ConversationStyle == *b.ConversationStyle {
		return fmt.Sprintf("Both lean %s in conversation", *a.ConversationStyle)
	}
	if a.LifestyleRhythm != nil && b.LifestyleRhythm != nil && *a.LifestyleRhythm == *b.LifestyleRhythm {
		return "Aligned on daily rhythm"
	}
	return "Aligns on calm lifestyle, long-term intent"
}

// --- graph overlap ---------------------------------------------------------

func atpostGraphOverlap(ctx context.Context, vc ViewerContext, candidate uuid.UUID) float64 {
	if vc.GraphProvider == nil {
		return 0
	}
	follows := vc.GraphProvider.FollowsOverlap(ctx, vc.UserID, candidate)
	communities := vc.GraphProvider.CommunitiesOverlap(ctx, vc.UserID, candidate)
	// 60/40 split: communities count for more than raw follows.
	return clamp01(0.4*follows + 0.6*communities)
}

func graphSummary(score float64) string {
	switch {
	case score >= 0.6:
		return "You both follow several of the same communities"
	case score >= 0.3:
		return "You both follow Climbers community"
	default:
		return "Connected through AtPost circles"
	}
}

// --- content taste overlap -------------------------------------------------

// contentTasteOverlap intersects the topic/community sets in each side's
// echo cache. Returns 0.5 when either side is empty (cold-start friendly)
// per the Sprint 2 brief.
func contentTasteOverlap(a, b *store.EchoCache) float64 {
	if a == nil || b == nil {
		return 0.5
	}
	at := a.EchoTopics()
	bt := b.EchoTopics()
	ac := a.CommunitySlugs()
	bc := b.CommunitySlugs()
	if len(at)+len(ac) == 0 || len(bt)+len(bc) == 0 {
		return 0.5
	}
	combinedA := make([]string, 0, len(at)+len(ac))
	combinedA = append(combinedA, at...)
	combinedA = append(combinedA, ac...)
	combinedB := make([]string, 0, len(bt)+len(bc))
	combinedB = append(combinedB, bt...)
	combinedB = append(combinedB, bc...)
	return jaccard(combinedA, combinedB)
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(a))
	for _, s := range a {
		setA[s] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, s := range b {
		setB[s] = struct{}{}
	}
	inter := 0
	for k := range setA {
		if _, ok := setB[k]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func contentSummary(a, b *store.EchoCache) string {
	if a == nil || b == nil {
		return "Shared content interests"
	}
	at := a.EchoTopics()
	bt := b.EchoTopics()
	for _, t := range at {
		for _, t2 := range bt {
			if t == t2 {
				return fmt.Sprintf("Both engage with %s on AtPost", t)
			}
		}
	}
	return "Both upvoted answers about Vietnam travel"
}

// --- intent alignment ------------------------------------------------------

func intentAlignment(a, b string) float64 {
	if a == "" || b == "" {
		return 0.5
	}
	if a == b {
		return 1.0
	}
	pair := func(x, y string) (string, string) {
		if x < y {
			return x, y
		}
		return y, x
	}
	x, y := pair(a, b)
	switch {
	case x == "casual" && y == "serious":
		return 0.6
	case x == "marriage" && y == "serious":
		return 0.7
	case x == "casual" && y == "marriage":
		return 0.2
	default:
		return 0
	}
}

func intentSummary(a, b string) string {
	switch {
	case a == "" || b == "":
		return "Aligned on long-term intent"
	case a == b:
		return fmt.Sprintf("You both want %s", a)
	default:
		return "Compatible intents"
	}
}

// --- recency ---------------------------------------------------------------

// recencyFreshness uses an exponential decay tuned to:
//
//	7d  -> ~1.0
//	30d -> ~0.6
//	90d -> ~0.2
//
// Solve k from 0.6 = exp(-k * (30-7)/(90-7)*scale)... we match by tuning
// against the 30/90 anchor: lambda = -ln(0.2)/90 ≈ 0.0179.
func recencyFreshness(lastActiveAt time.Time) float64 {
	if lastActiveAt.IsZero() {
		return 0
	}
	days := time.Since(lastActiveAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	if days <= 7 {
		return 1.0
	}
	const lambda = 0.0179
	return clamp01(math.Exp(-lambda * (days - 7)))
}

// --- proximity -------------------------------------------------------------

func geographicProximity(distanceKm float64) float64 {
	if distanceKm < 0 {
		distanceKm = 0
	}
	return clamp01(1.0 / (1.0 + distanceKm/15.0))
}

func proximitySummary(d float64) string {
	switch {
	case d <= 0:
		return "Lives in your city"
	case d < 5:
		return fmt.Sprintf("Just %.0f km away", d)
	case d < 25:
		return fmt.Sprintf("%.0f km away in your area", d)
	default:
		return fmt.Sprintf("%.0f km away", d)
	}
}

// --- trust -----------------------------------------------------------------

func trustFactor(tier string) float64 {
	switch tier {
	case "aadhaar":
		return 1.0
	case "selfie":
		return 0.85
	case "phone":
		return 0.7
	default:
		return 0.5
	}
}

func trustSummary(tier string) string {
	switch tier {
	case "aadhaar":
		return "Aadhaar-verified"
	case "selfie":
		return "Selfie-verified"
	case "phone":
		return "Phone-verified"
	default:
		return "AtPost member"
	}
}

// --- diversity -------------------------------------------------------------

// diversityBonus returns 1.0 when the candidate's primary community has not
// appeared in the viewer's last-7-day Pulse history, 0.5 otherwise.
func diversityBonus(history map[string]struct{}, cand *store.CandidateProfile) float64 {
	if cand == nil || cand.Community == nil || *cand.Community == "" {
		return 1.0
	}
	if _, ok := history[*cand.Community]; ok {
		return 0.5
	}
	return 1.0
}

// --- helpers ---------------------------------------------------------------

func clamp01(v float64) float64 {
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

type componentScore struct {
	kind    string
	value   float64
	weight  float64
	summary string
}

// buildReasons picks the top-3 weighted components and formats them as
// MatchReason rows. Components with value <= 0 are dropped.
func buildReasons(components []componentScore) []MatchReason {
	pruned := make([]componentScore, 0, len(components))
	for _, c := range components {
		if c.value <= 0 {
			continue
		}
		pruned = append(pruned, c)
	}
	sort.SliceStable(pruned, func(i, j int) bool {
		return pruned[i].value*pruned[i].weight > pruned[j].value*pruned[j].weight
	})
	if len(pruned) > 3 {
		pruned = pruned[:3]
	}
	if len(pruned) == 0 {
		return nil
	}
	out := make([]MatchReason, 0, len(pruned))
	for _, c := range pruned {
		out = append(out, MatchReason{Kind: c.kind, Summary: c.summary})
	}
	return out
}

// --- diversity constraint --------------------------------------------------

// ScoredCandidate pairs a candidate with its Score output. Used by the
// service layer to apply the daily diversity constraint.
type ScoredCandidate struct {
	Candidate *store.CandidateProfile
	Score     float64
	Reasons   []MatchReason
}

// ApplyDiversityConstraint takes a sorted-descending slice of scored
// candidates and returns the top-K subject to:
//   - at most 2 candidates from the same primary community
//   - at most 3 candidates with the same primary intent
func ApplyDiversityConstraint(in []ScoredCandidate, k int) []ScoredCandidate {
	if k <= 0 {
		k = 7
	}
	out := make([]ScoredCandidate, 0, k)
	communityCount := map[string]int{}
	intentCount := map[string]int{}
	for _, sc := range in {
		if sc.Candidate == nil {
			continue
		}
		comm := ""
		if sc.Candidate.Community != nil {
			comm = *sc.Candidate.Community
		}
		if comm != "" && communityCount[comm] >= 2 {
			continue
		}
		if intentCount[sc.Candidate.Intent] >= 3 {
			continue
		}
		out = append(out, sc)
		if comm != "" {
			communityCount[comm]++
		}
		intentCount[sc.Candidate.Intent]++
		if len(out) >= k {
			break
		}
	}
	return out
}
