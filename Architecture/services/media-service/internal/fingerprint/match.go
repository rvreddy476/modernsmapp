package fingerprint

import (
	"sort"
)

// Voting, alignment, coverage and classification (plan 12.3–12.4). The
// query side is "copy" (the job's own asset) and the candidate is "ref";
// which upload is earlier is decided elsewhere from upload_confirmed_at and
// is a fact, never a verdict.

const (
	// VoteBinMs: candidate offsets are binned to 0.5 s.
	VoteBinMs = 500
	// MinVotes: proceed with ≥ 2 hits in one bin or in adjacent bins.
	MinVotes = 2
	// AlignHamming is τ, the per-frame match threshold. An unvalidated
	// starting value, tuned in shadow.
	AlignHamming = 10
	// BridgeGapMs: gaps up to this are bridged for continuity and add no
	// matched weight.
	BridgeGapMs = 3000
	// MinSegmentMs: a matched run shorter than this is noise.
	MinSegmentMs = 5000
	// MaxSegments bounds the monotonic segments per pair.
	MaxSegments = 8
	// MaxOffsetsTried bounds the voted offsets alignment walks.
	MaxOffsetsTried = 8
	// DiversityHamming / DiversityCap: matched frames pairwise more than
	// this apart count toward diversity, capped.
	DiversityHamming = 10
	DiversityCap     = 10
)

// Posting is one index hit as the store returns it.
type Posting struct {
	AnchorMs int32
	Hash     uint64
}

// Hit is a query frame matched to a posting.
type Hit struct {
	QueryMs, AnchorMs int32
	Hamming           int
	MinBandDistance   int
}

// Hits filters postings against the query frames at τ_hit and records the
// minimum band distance that produced each hit.
func Hits(query []Frame, postings []Posting) []Hit {
	var out []Hit
	for _, p := range postings {
		for _, q := range query {
			if d := Hamming(q.Hash, p.Hash); d <= HitHamming {
				out = append(out, Hit{QueryMs: q.TMs, AnchorMs: p.AnchorMs, Hamming: d,
					MinBandDistance: MinBandDistance(q.Hash, p.Hash)})
			}
		}
	}
	return out
}

// Offset is a voted alignment: refMs ≈ copyMs + OffsetMs.
type Offset struct {
	OffsetMs int32
	Votes    int
}

// Vote bins the hits' offsets to 0.5 s and returns the offsets with ≥
// MinVotes counting adjacent bins, best first. A bin's reported offset is
// the mean of its own hits.
func Vote(hits []Hit) []Offset {
	if len(hits) == 0 {
		return nil
	}
	type acc struct {
		n   int
		sum int64
	}
	bins := map[int32]*acc{}
	for _, h := range hits {
		off := h.AnchorMs - h.QueryMs
		b := floorDiv(off, VoteBinMs)
		a := bins[b]
		if a == nil {
			a = &acc{}
			bins[b] = a
		}
		a.n++
		a.sum += int64(off)
	}
	var out []Offset
	for b, a := range bins {
		votes := a.n
		if nb := bins[b-1]; nb != nil {
			votes += nb.n
		}
		if nb := bins[b+1]; nb != nil {
			votes += nb.n
		}
		if votes < MinVotes {
			continue
		}
		out = append(out, Offset{OffsetMs: int32(a.sum / int64(a.n)), Votes: votes})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Votes != out[j].Votes {
			return out[i].Votes > out[j].Votes
		}
		return out[i].OffsetMs < out[j].OffsetMs
	})
	// Adjacent bins report the same neighbourhood twice; keep one per 1 s.
	var dedup []Offset
	for _, o := range out {
		keep := true
		for _, d := range dedup {
			if abs32(d.OffsetMs-o.OffsetMs) <= VoteBinMs {
				keep = false
				break
			}
		}
		if keep {
			dedup = append(dedup, o)
		}
	}
	if len(dedup) > MaxOffsetsTried {
		dedup = dedup[:MaxOffsetsTried]
	}
	return dedup
}

func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

// Segment is one aligned run.
type Segment struct {
	CopyStartMs, CopyEndMs int32 // half-open, in copy time
	OffsetMs               int32
	MatchedFrames          int
}

// Alignment is the result of walking both sequences.
type Alignment struct {
	Segments []Segment
	// Matched frame pairs (copy index, ref index) with their distance.
	pairs []framePair
}

type framePair struct {
	copyIdx, refIdx int
	hamming         int
}

// Align walks both full 2 fps sequences at each voted offset, matching a
// frame pair at Hamming ≤ τ, bridging gaps ≤ BridgeGapMs without weight,
// keeping runs ≥ MinSegmentMs, then assembles up to MaxSegments
// non-overlapping, monotonic segments across offsets. The time scale is
// fixed at 1.0.
func Align(copyF, refF []Frame, offsets []Offset) Alignment {
	refIndex := make(map[int32]int, len(refF))
	for i, f := range refF {
		refIndex[f.TMs] = i
	}
	type cand struct {
		seg   Segment
		pairs []framePair
	}
	var cands []cand
	for _, off := range offsets {
		// Snap the offset to the 2 fps grid so a query frame lands on a
		// reference frame within ±0.25 s.
		snap := int32(roundDiv(int64(off.OffsetMs), FrameMs)) * FrameMs
		var run []framePair
		var runStart, lastMatch int32
		flush := func() {
			if len(run) == 0 {
				return
			}
			if lastMatch+FrameMs-runStart >= MinSegmentMs {
				cands = append(cands, cand{seg: Segment{CopyStartMs: runStart, CopyEndMs: lastMatch + FrameMs,
					OffsetMs: snap, MatchedFrames: len(run)}, pairs: append([]framePair(nil), run...)})
			}
			run = nil
		}
		for ci, cf := range copyF {
			ri, ok := refIndex[cf.TMs+snap]
			if !ok {
				continue
			}
			d := Hamming(cf.Hash, refF[ri].Hash)
			// Phase tolerance: a trim that is not on the 2 fps grid puts
			// the copy's frame between two reference frames; the nearer
			// neighbour (±0.5 s) may be the closer picture. The distance
			// takes the best of the three, but coverage is credited to the
			// nominal frame, so two copy frames never share one reference
			// frame and leave its neighbour uncounted.
			for _, o := range [2]int32{-FrameMs, FrameMs} {
				if rj, ok := refIndex[cf.TMs+snap+o]; ok {
					if d2 := Hamming(cf.Hash, refF[rj].Hash); d2 < d {
						d = d2
					}
				}
			}
			matched := d <= AlignHamming && cf.Flags&FlagFlat == 0 && refF[ri].Flags&FlagFlat == 0
			if !matched {
				// A gap longer than the bridge ends the run.
				if len(run) > 0 && cf.TMs-lastMatch > BridgeGapMs {
					flush()
				}
				continue
			}
			if len(run) == 0 {
				runStart = cf.TMs
			}
			run = append(run, framePair{copyIdx: ci, refIdx: ri, hamming: d})
			lastMatch = cf.TMs
		}
		flush()
	}
	// Longest first, then keep what does not overlap in copy time and stays
	// monotonic in reference time.
	sort.Slice(cands, func(i, j int) bool {
		li := cands[i].seg.CopyEndMs - cands[i].seg.CopyStartMs
		lj := cands[j].seg.CopyEndMs - cands[j].seg.CopyStartMs
		if li != lj {
			return li > lj
		}
		return cands[i].seg.CopyStartMs < cands[j].seg.CopyStartMs
	})
	var chosen []cand
	for _, c := range cands {
		if len(chosen) >= MaxSegments {
			break
		}
		ok := true
		for _, k := range chosen {
			if c.seg.CopyStartMs < k.seg.CopyEndMs && k.seg.CopyStartMs < c.seg.CopyEndMs {
				ok = false
				break
			}
			// Monotonic: the later segment in copy time must also be later
			// in reference time.
			if c.seg.CopyStartMs >= k.seg.CopyEndMs && c.seg.CopyStartMs+c.seg.OffsetMs < k.seg.CopyEndMs+k.seg.OffsetMs {
				ok = false
				break
			}
			if k.seg.CopyStartMs >= c.seg.CopyEndMs && k.seg.CopyStartMs+k.seg.OffsetMs < c.seg.CopyEndMs+c.seg.OffsetMs {
				ok = false
				break
			}
		}
		if ok {
			chosen = append(chosen, c)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].seg.CopyStartMs < chosen[j].seg.CopyStartMs })
	var out Alignment
	for _, c := range chosen {
		out.pairs = append(out.pairs, c.pairs...)
		// Continuity across an insert: two runs whose copy-time gap AND
		// reference-time gap are both ≤ BridgeGapMs are one segment (a
		// 2.5 s insert is bridged, a 4 s insert splits, T10-7). The bridged
		// frames were never matched, so they add no weight.
		if n := len(out.Segments); n > 0 {
			prev := &out.Segments[n-1]
			copyGap := c.seg.CopyStartMs - prev.CopyEndMs
			refGap := (c.seg.CopyStartMs + c.seg.OffsetMs) - (prev.CopyEndMs + prev.OffsetMs)
			if copyGap <= BridgeGapMs && refGap >= 0 && refGap <= BridgeGapMs {
				prev.CopyEndMs = c.seg.CopyEndMs
				prev.MatchedFrames += c.seg.MatchedFrames
				continue
			}
		}
		out.Segments = append(out.Segments, c.seg)
	}
	return out
}

func roundDiv(a, b int64) int64 {
	if a >= 0 {
		return (a + b/2) / b
	}
	return -((-a + b/2) / b)
}

// Scores are the numbers a pair is classified on.
type Scores struct {
	RefCoverage         float64
	CopyCoverage        float64
	MatchedS            float64
	MatchedInformativeS float64
	MedianHamming       float64
	P90Hamming          float64
	Diversity           int
	RefInformativeS     float64
	CopyInformativeS    float64
	Truncated           bool
}

// Score computes coverage on informative weight from an alignment. Weights
// come from each side's own sequence; bridged frames carry no weight.
func Score(copyF, refF []Frame, a Alignment, truncated bool) Scores {
	wc, wr := Weights(copyF), Weights(refF)
	var s Scores
	s.CopyInformativeS = float64(InformativeMs(wc)) / 1000
	s.RefInformativeS = float64(InformativeMs(wr)) / 1000
	s.Truncated = truncated
	if len(a.pairs) == 0 {
		return s
	}
	var matchedCopy, matchedRef, matchedInf int32
	seenCopy := map[int]bool{}
	seenRef := map[int]bool{}
	dists := make([]int, 0, len(a.pairs))
	var distinct []uint64
	for _, p := range a.pairs {
		if !seenCopy[p.copyIdx] {
			seenCopy[p.copyIdx] = true
			matchedCopy += wc[p.copyIdx]
		}
		if !seenRef[p.refIdx] {
			seenRef[p.refIdx] = true
			matchedRef += wr[p.refIdx]
		}
		dists = append(dists, p.hamming)
		h := copyF[p.copyIdx].Hash
		if len(distinct) < DiversityCap {
			novel := true
			for _, d := range distinct {
				if Hamming(d, h) <= DiversityHamming {
					novel = false
					break
				}
			}
			if novel {
				distinct = append(distinct, h)
			}
		}
	}
	matchedInf = matchedCopy
	if matchedRef < matchedInf {
		matchedInf = matchedRef
	}
	s.MatchedS = float64(len(seenCopy)*FrameMs) / 1000
	s.MatchedInformativeS = float64(matchedInf) / 1000
	if s.CopyInformativeS > 0 {
		s.CopyCoverage = float64(matchedCopy) / 1000 / s.CopyInformativeS
	}
	if s.RefInformativeS > 0 {
		s.RefCoverage = float64(matchedRef) / 1000 / s.RefInformativeS
	}
	sort.Ints(dists)
	s.MedianHamming = float64(dists[len(dists)/2])
	s.P90Hamming = float64(dists[(len(dists)*9)/10])
	s.Diversity = len(distinct)
	return s
}

// Class is the pair class (plan 12.4).
type Class string

const (
	ClassFullOrNearFull       Class = "full_or_near_full"
	ClassContains             Class = "contains"
	ClassPartial              Class = "partial"
	ClassShortClipCandidate   Class = "short_clip_candidate"
	ClassInsufficientEvidence Class = "insufficient_evidence"
)

// Classification thresholds.
const (
	FullRefCoverage       = 0.85
	FullCopyCoverage      = 0.80
	FullMinMatchedS       = 8.0
	FullMinDiversity      = 3
	FullMinRefInformative = 10.0
	PartialMinMatchedS    = 30.0
	ShortMinCoverage      = 0.95
	ShortMaxMedianHamming = 6.0
	ShortMinDiversity     = 2
	MinSideInformativeS   = 8.0
)

// Classify applies the table in plan 12.4. A truncated lookup is never
// "no match"; it is insufficient evidence, counted.
func Classify(s Scores) Class {
	if s.Truncated {
		return ClassInsufficientEvidence
	}
	if s.RefInformativeS < FullMinRefInformative {
		// Short reference: the short-clip rule or nothing.
		if s.RefCoverage >= ShortMinCoverage && s.CopyCoverage >= ShortMinCoverage &&
			s.MedianHamming <= ShortMaxMedianHamming && s.Diversity >= ShortMinDiversity && s.MatchedInformativeS > 0 {
			return ClassShortClipCandidate
		}
		return ClassInsufficientEvidence
	}
	if s.CopyInformativeS < MinSideInformativeS || s.MatchedInformativeS <= 0 {
		return ClassInsufficientEvidence
	}
	if s.RefCoverage >= FullRefCoverage {
		if s.CopyCoverage >= FullCopyCoverage {
			if s.MatchedInformativeS >= FullMinMatchedS && s.Diversity >= FullMinDiversity {
				return ClassFullOrNearFull
			}
			return ClassInsufficientEvidence
		}
		return ClassContains
	}
	if s.MatchedInformativeS >= PartialMinMatchedS {
		return ClassPartial
	}
	return ClassInsufficientEvidence
}
