package service

import (
	"fmt"
	"math"
)

// ---------------------------------------------------------------------------
// Quality-weighted payout
// ---------------------------------------------------------------------------
//
// The founder's question was "how much to be paid, based on quality of
// content". Until now the answer was views x RPM and nothing else:
// quality gated *whether* a creator was in the fund, never how much they
// received.
//
// analytics-service already computes a content quality score (CQS) in
// [0,1] per content item per day. The shape chosen here is deliberately
// conservative:
//
//	base_gross = ComputeGrossPaise(views, rpm)   <- unchanged, still exported
//	multiplier = clamp(curve(effective_cqs), floor, ceiling)
//	gross      = base_gross * multiplier
//
// Three guard rails, each for a concrete failure mode:
//
//  1. A FLOOR, not a raw multiply. CQS near zero is normal for content
//     that simply has not been shown to many people. Multiplying the
//     payout by a raw CQS would pay almost nothing for views that
//     genuinely happened, which is a worse injustice than slightly
//     overpaying mediocre content. At the launch band a zero-scoring
//     creator still receives 85% of the plain amount.
//
//  2. A CONFIDENCE SHRINK. CQS is extremely noisy at low impression
//     counts: three shares on eighty impressions is a huge rate and
//     means nothing. Before the curve is applied, the measured score is
//     pulled toward the neutral pivot in proportion to how little
//     evidence there is - impressions / (impressions + K). With no
//     impressions at all the shrink returns the pivot exactly, so a
//     zero-impression creator gets a 1.0x multiplier rather than a
//     division by zero or a floor-level punishment.
//
//  3. A CEILING. Engagement is the most gameable half of the score, so
//     the upside is capped. At the launch band the very best content
//     earns 25% more, not 300% more.
//
// The curve itself is piecewise linear through a neutral pivot:
//
//	cqs = 0      -> floor      (0.85x at launch)
//	cqs = pivot  -> 1.0x       (0.35 at launch: paid exactly as before)
//	cqs = 1      -> ceiling    (1.25x at launch)
//
// Linear rather than a power curve because a creator has to be able to
// read the explanation and predict the effect of improving. The pivot
// sits at 0.35 because CQS is watch-time dominant (0.45 weight) and its
// engagement rates are divided by a 50-per-1000 soft cap, which puts
// ordinary healthy content in the 0.2-0.5 range: the median creator
// should be neutral, not penalised.

const (
	// NeutralMultiplierBps is 1.0x - the historical behaviour.
	NeutralMultiplierBps = int64(10_000)

	defaultQualityFloorBps              = int64(8_500)  // 0.85x
	defaultQualityCeilingBps            = int64(12_500) // 1.25x
	defaultQualityPivotCQS              = 0.35
	defaultQualityConfidenceImpressions = int64(1_000)
)

// QualityBand is the configurable multiplier band. It is stored per
// (content_type, region) and versioned by effective_from/effective_to,
// exactly the way the RPM rate sheet behind SetRpmRate is.
type QualityBand struct {
	FloorBps              int64   `json:"floor_bps"`
	CeilingBps            int64   `json:"ceiling_bps"`
	PivotCQS              float64 `json:"pivot_cqs"`
	ConfidenceImpressions int64   `json:"confidence_impressions"`
	Enabled               bool    `json:"enabled"`
}

// DefaultQualityBand is the launch baseline, and also the band used when
// no row is configured for a (content_type, region).
func DefaultQualityBand() QualityBand {
	return QualityBand{
		FloorBps:              defaultQualityFloorBps,
		CeilingBps:            defaultQualityCeilingBps,
		PivotCQS:              defaultQualityPivotCQS,
		ConfidenceImpressions: defaultQualityConfidenceImpressions,
		Enabled:               true,
	}
}

// NeutralQualityBand is the escape hatch: a band pinned to 1.0x, which
// reproduces the pre-quality payout exactly. Setting floor == ceiling ==
// 10000, or enabled = false, keeps the old behaviour reachable.
func NeutralQualityBand() QualityBand {
	return QualityBand{
		FloorBps:              NeutralMultiplierBps,
		CeilingBps:            NeutralMultiplierBps,
		PivotCQS:              defaultQualityPivotCQS,
		ConfidenceImpressions: defaultQualityConfidenceImpressions,
		Enabled:               false,
	}
}

// normalized returns a band with any nonsensical values repaired, so a
// bad admin row can never produce a negative or inverted multiplier.
func (b QualityBand) normalized() QualityBand {
	if b.FloorBps < 0 {
		b.FloorBps = 0
	}
	if b.CeilingBps < b.FloorBps {
		b.CeilingBps = b.FloorBps
	}
	if b.PivotCQS <= 0 || b.PivotCQS >= 1 || math.IsNaN(b.PivotCQS) {
		b.PivotCQS = defaultQualityPivotCQS
	}
	if b.ConfidenceImpressions < 0 {
		b.ConfidenceImpressions = 0
	}
	return b
}

// ShrinkCQS pulls a measured score toward the neutral pivot in
// proportion to how little evidence supports it. With zero impressions
// it returns the pivot, which maps to a 1.0x multiplier - a creator with
// no impressions recorded is never divided by zero and never punished
// for the absence of data.
func ShrinkCQS(cqs float64, impressions int64, band QualityBand) float64 {
	b := band.normalized()
	if math.IsNaN(cqs) {
		return b.PivotCQS
	}
	if cqs < 0 {
		cqs = 0
	}
	if cqs > 1 {
		cqs = 1
	}
	if impressions <= 0 {
		return b.PivotCQS
	}
	if b.ConfidenceImpressions <= 0 {
		return cqs
	}
	weight := float64(impressions) / float64(impressions+b.ConfidenceImpressions)
	return b.PivotCQS + (cqs-b.PivotCQS)*weight
}

// ComputeQualityMultiplierBps maps a measured CQS and the impression
// count behind it onto a payout multiplier in basis points, clamped to
// the band. A disabled band always returns 1.0x.
func ComputeQualityMultiplierBps(cqs float64, impressions int64, band QualityBand) int64 {
	b := band.normalized()
	if !b.Enabled {
		return NeutralMultiplierBps
	}

	effective := ShrinkCQS(cqs, impressions, b)

	var bps float64
	if effective <= b.PivotCQS {
		// floor .. 1.0x over [0, pivot]
		span := float64(NeutralMultiplierBps - b.FloorBps)
		bps = float64(b.FloorBps) + span*(effective/b.PivotCQS)
	} else {
		// 1.0x .. ceiling over [pivot, 1]
		span := float64(b.CeilingBps - NeutralMultiplierBps)
		bps = float64(NeutralMultiplierBps) + span*((effective-b.PivotCQS)/(1-b.PivotCQS))
	}

	out := int64(math.Round(bps))
	if out < b.FloorBps {
		out = b.FloorBps
	}
	if out > b.CeilingBps {
		out = b.CeilingBps
	}
	return out
}

// ApplyQualityMultiplier scales a gross amount by a basis-point
// multiplier. Integer arithmetic throughout so paise are never rounded
// away by a float round-trip; multiply before divide so a 0.85x on a
// small amount does not collapse to zero prematurely.
func ApplyQualityMultiplier(basePaise, multiplierBps int64) int64 {
	if basePaise <= 0 || multiplierBps <= 0 {
		return 0
	}
	return basePaise * multiplierBps / NeutralMultiplierBps
}

// ComputeQualityAdjustedGrossPaise is the full payout formula:
// ComputeGrossPaise (unchanged) then the band multiplier. Passing a
// disabled or neutral band reproduces ComputeGrossPaise exactly, so the
// old behaviour stays reachable and testable.
func ComputeQualityAdjustedGrossPaise(views, rpmPaise int64, cqs float64, impressions int64, band QualityBand) (grossPaise, baseGrossPaise, multiplierBps int64) {
	baseGrossPaise = ComputeGrossPaise(views, rpmPaise)
	multiplierBps = ComputeQualityMultiplierBps(cqs, impressions, band)
	grossPaise = ApplyQualityMultiplier(baseGrossPaise, multiplierBps)
	return grossPaise, baseGrossPaise, multiplierBps
}

// QualityPayoutExplanation is the creator-facing "why was I paid this".
// It is attached to every settled earning row and returned by the
// earnings endpoint, so the number is never unexplained.
type QualityPayoutExplanation struct {
	ViewCount        int64   `json:"view_count"`
	RpmPaise         int64   `json:"rpm_paise"`
	BaseGrossPaise   int64   `json:"base_gross_paise"`
	MeasuredCQS      float64 `json:"measured_cqs"`
	Impressions      int64   `json:"impressions"`
	EffectiveCQS     float64 `json:"effective_cqs"`
	MultiplierBps    int64   `json:"quality_multiplier_bps"`
	FloorBps         int64   `json:"quality_floor_bps"`
	CeilingBps       int64   `json:"quality_ceiling_bps"`
	PivotCQS         float64 `json:"quality_pivot_cqs"`
	GrossPaise       int64   `json:"gross_paise"`
	PlatformFeeBps   int64   `json:"platform_fee_bps"`
	PlatformFeePaise int64   `json:"platform_fee_paise"`
	NetPaise         int64   `json:"net_paise"`
	Summary          string  `json:"summary"`
}

// ExplainQualityPayout renders the sentence a creator reads under their
// earnings line. Written in plain money, not basis points.
func ExplainQualityPayout(e QualityPayoutExplanation) string {
	direction := "left unchanged by"
	switch {
	case e.MultiplierBps > NeutralMultiplierBps:
		direction = "raised by"
	case e.MultiplierBps < NeutralMultiplierBps:
		direction = "reduced by"
	}
	confidence := ""
	// Only mention the confidence blend when it actually moved the
	// number a creator would notice. At high impression counts the shrink
	// is a fraction of a percent, and saying "not enough impressions to
	// be confident" about a well-measured 50 000-impression day reads as
	// a complaint about data that is in fact plentiful.
	if e.Impressions <= 0 {
		confidence = " No impressions were recorded for this day, so the neutral 1.00x rate was used rather than a score."
	} else if math.Abs(e.EffectiveCQS-e.MeasuredCQS) > 0.05 {
		confidence = fmt.Sprintf(
			" This score came from only %d impressions, so it was blended toward neutral before being applied (%.2f used).",
			e.Impressions, e.EffectiveCQS)
	}
	return fmt.Sprintf(
		"%d views at %s per 1000 = %s. Quality score %.2f %s a %.2fx multiplier (band %.2fx-%.2fx, neutral at %.2f) = %s gross, %s to you after the %.0f%% platform share.%s",
		e.ViewCount, rupees(e.RpmPaise), rupees(e.BaseGrossPaise),
		e.MeasuredCQS, direction, float64(e.MultiplierBps)/float64(NeutralMultiplierBps),
		float64(e.FloorBps)/float64(NeutralMultiplierBps),
		float64(e.CeilingBps)/float64(NeutralMultiplierBps),
		e.PivotCQS,
		rupees(e.GrossPaise), rupees(e.NetPaise),
		float64(e.PlatformFeeBps)/100.0,
		confidence,
	)
}

func rupees(paise int64) string {
	return fmt.Sprintf("Rs %.2f", float64(paise)/100.0)
}
