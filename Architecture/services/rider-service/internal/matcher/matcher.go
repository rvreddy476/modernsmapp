// Package matcher implements the Mopedu ride-to-partner matching algorithm.
//
// The matcher is split in two halves:
//
//  1. Hard filters — boolean rules that disqualify a candidate outright:
//     partner status=approved, KYC approved, an approved vehicle of the
//     requested type, an active subscription, online flag set, the city
//     matches, and (for non-unlimited plans) the lead allotment is not yet
//     consumed for the billing period.
//
//  2. Soft scoring — the 8-component formula from spec §10. Higher is
//     better. Plan priority weight dominates (×100) so an Elite partner
//     always beats a Basic partner of identical performance, with rating,
//     acceptance rate, distance, and fraud score modulating the order.
//
// The algorithm has no side effects: callers feed in candidates and receive
// the top-N back. Persisting offers + emitting Kafka events lives in
// service/ride.go so the matcher itself is unit-testable in pure isolation.
package matcher

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// PartnerCandidate is one online partner who could be offered the ride.
//
// All fields are pre-fetched by the service layer so the matcher does no I/O:
// the service joins partners, vehicles, subscriptions, and locations into
// PartnerCandidate values before invoking Score / Filter.
type PartnerCandidate struct {
	PartnerID          string
	UserID             string
	VehicleID          string
	VehicleType        string
	CityID             string
	PartnerStatus      string  // 'approved' | 'pending_verification' | …
	KYCStatus          string  // 'approved' | …
	VehicleStatus      string  // 'approved' | 'pending' | 'rejected'
	SubscriptionStatus string  // 'trial' | 'active' | 'grace_period' | …
	IsOnline           bool
	IsUnlimitedPlan    bool
	LeadsUsedThisMonth int
	LeadAllotment      int     // 0 means unlimited; ignored if IsUnlimitedPlan
	PlanPriorityWeight int     // 50–160 from rider_subscription_plans
	Rating             float64 // 0–5
	AcceptanceRate     float64 // 0–100 percent
	CancellationRate   float64 // 0–100 percent
	FraudScore         float64 // higher = more suspicious
	DistanceKM         float64 // straight-line km from pickup
	IdleSecs           int     // how long the partner has been idle (tie-breaker)
	LastSeen           time.Time
}

// RideRequest captures the matcher-relevant fields of a ride.
type RideRequest struct {
	VehicleType string
	CityID      string
}

// HardFilterReason names the rule that disqualified a candidate. Returned for
// observability + admin-debugging; not surfaced to clients.
type HardFilterReason string

const (
	ReasonPartnerNotApproved   HardFilterReason = "partner_not_approved"
	ReasonKYCNotApproved       HardFilterReason = "kyc_not_approved"
	ReasonVehicleNotApproved   HardFilterReason = "vehicle_not_approved"
	ReasonNoActiveSubscription HardFilterReason = "no_active_subscription"
	ReasonOffline              HardFilterReason = "offline"
	ReasonVehicleTypeMismatch  HardFilterReason = "vehicle_type_mismatch"
	ReasonCityMismatch         HardFilterReason = "city_mismatch"
	ReasonLeadCapExhausted     HardFilterReason = "lead_cap_exhausted"
)

// FilterCandidates applies the hard filters and returns the candidates that
// survived plus a parallel slice of rejection reasons for the rest.
//
// The output preserves input order (no sort) — caller uses Score / Rank.
func FilterCandidates(req RideRequest, candidates []PartnerCandidate) (kept []PartnerCandidate, rejected []HardFilterReason) {
	kept = make([]PartnerCandidate, 0, len(candidates))
	rejected = make([]HardFilterReason, 0, len(candidates))
	for _, c := range candidates {
		if reason, ok := disqualify(req, c); ok {
			rejected = append(rejected, reason)
			continue
		}
		kept = append(kept, c)
	}
	return kept, rejected
}

func disqualify(req RideRequest, c PartnerCandidate) (HardFilterReason, bool) {
	if c.PartnerStatus != "approved" {
		return ReasonPartnerNotApproved, true
	}
	if c.KYCStatus != "approved" {
		return ReasonKYCNotApproved, true
	}
	if c.VehicleStatus != "approved" {
		return ReasonVehicleNotApproved, true
	}
	switch c.SubscriptionStatus {
	case "trial", "active", "grace_period":
		// ok
	default:
		return ReasonNoActiveSubscription, true
	}
	if !c.IsOnline {
		return ReasonOffline, true
	}
	if !strings.EqualFold(c.VehicleType, req.VehicleType) {
		return ReasonVehicleTypeMismatch, true
	}
	if req.CityID != "" && c.CityID != "" && c.CityID != req.CityID {
		return ReasonCityMismatch, true
	}
	if !c.IsUnlimitedPlan && c.LeadAllotment > 0 && c.LeadsUsedThisMonth >= c.LeadAllotment {
		return ReasonLeadCapExhausted, true
	}
	return "", false
}

// Score implements the Mopedu match formula (spec §10).
//
//	score = plan_priority_weight*100
//	      + rating*20
//	      + acceptance_rate*0.5
//	      - cancellation_rate*1.0
//	      - fraud_score*2.0
//	      - distance_to_pickup_km*10
//	      + idle_bonus
//
// A small idle bonus (capped) tilts the tie-breaker toward partners who have
// been waiting longer — ensures fair distribution when two partners have
// otherwise-identical scores. The reasons slice records the exact value of
// each component so admin debug surfaces (S3) can explain a ranking.
func Score(c PartnerCandidate) (float64, []string) {
	planComponent := float64(c.PlanPriorityWeight) * 100.0
	ratingComponent := c.Rating * 20.0
	acceptComponent := c.AcceptanceRate * 0.5
	cancelComponent := -1.0 * c.CancellationRate
	fraudComponent := -2.0 * c.FraudScore
	distComponent := -10.0 * c.DistanceKM
	idleBonus := float64(c.IdleSecs) * 0.05
	if idleBonus > 30 {
		idleBonus = 30 // cap so a long-idle Basic partner can't beat Elite
	}
	total := planComponent + ratingComponent + acceptComponent + cancelComponent + fraudComponent + distComponent + idleBonus
	reasons := []string{
		formatComponent("plan", planComponent),
		formatComponent("rating", ratingComponent),
		formatComponent("acceptance", acceptComponent),
		formatComponent("cancellation", cancelComponent),
		formatComponent("fraud", fraudComponent),
		formatComponent("distance", distComponent),
		formatComponent("idle", idleBonus),
	}
	return total, reasons
}

// Rank returns the candidates sorted highest-score first. Stable sort so the
// input order acts as a deterministic tie-breaker.
func Rank(candidates []PartnerCandidate) []ScoredCandidate {
	out := make([]ScoredCandidate, 0, len(candidates))
	for _, c := range candidates {
		s, reasons := Score(c)
		out = append(out, ScoredCandidate{Candidate: c, Score: s, Reasons: reasons})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// ScoredCandidate is one candidate with its computed score + breakdown.
type ScoredCandidate struct {
	Candidate PartnerCandidate
	Score     float64
	Reasons   []string
}

// OfferResult is the matcher's high-level output: the partners selected to
// receive offers in this batch + the cohort of remaining candidates the
// caller can fall through to in the next batch.
type OfferResult struct {
	Selected  []ScoredCandidate
	Remaining []ScoredCandidate
	BatchSize int
	ExpiresAt time.Time
}

// BatchOffer takes the full filtered + scored candidate set and selects the
// top `batchSize` to be offered the ride concurrently with a `timer` expiry
// (spec §10.5: 15-second concurrent offer window).
//
// `now` is injected for deterministic tests; real callers pass time.Now().
func BatchOffer(candidates []ScoredCandidate, batchSize int, timer time.Duration, now time.Time) OfferResult {
	if batchSize <= 0 {
		batchSize = 5
	}
	if timer <= 0 {
		timer = 15 * time.Second
	}
	expires := now.Add(timer)
	if len(candidates) <= batchSize {
		return OfferResult{
			Selected:  candidates,
			Remaining: nil,
			BatchSize: batchSize,
			ExpiresAt: expires,
		}
	}
	return OfferResult{
		Selected:  candidates[:batchSize],
		Remaining: candidates[batchSize:],
		BatchSize: batchSize,
		ExpiresAt: expires,
	}
}

func formatComponent(label string, value float64) string {
	sign := "+"
	if value < 0 {
		sign = ""
	}
	return label + "=" + sign + strconv.FormatFloat(value, 'f', 2, 64)
}
