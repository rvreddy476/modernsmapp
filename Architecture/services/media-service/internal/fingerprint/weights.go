package fingerprint

import "math"

// Per-video derivations (plan 12.2, 12.3): static flags and informative
// weights, the duration bucket, anchor selection and query-frame selection.

// FlagStatics sets FlagStaticRepeat on every frame within StaticHamming of
// the previous frame. Flat frames neither carry nor break a static run's
// comparison: the previous NON-flat frame is what the next one is compared
// to, so a black flash inside a still slide does not restart the run.
func FlagStatics(frames []Frame) {
	havePrev := false
	var prev uint64
	for i := range frames {
		frames[i].Flags &^= FlagStaticRepeat
		if frames[i].Flags&FlagFlat != 0 {
			continue
		}
		if havePrev && Hamming(prev, frames[i].Hash) <= StaticHamming {
			frames[i].Flags |= FlagStaticRepeat
		}
		prev, havePrev = frames[i].Hash, true
	}
}

// Weights returns each frame's informative weight in milliseconds: 0 for a
// flat frame; FrameMs otherwise, except that a static run (the first frame
// of the run plus its repeats) contributes at most StaticRunCapMs in total.
func Weights(frames []Frame) []int32 {
	w := make([]int32, len(frames))
	runMs := int32(0)
	for i, f := range frames {
		if f.Flags&FlagFlat != 0 {
			continue
		}
		if f.Flags&FlagStaticRepeat == 0 {
			runMs = 0 // a new run starts on a non-repeat frame
		}
		if runMs+FrameMs <= StaticRunCapMs {
			w[i] = FrameMs
		}
		runMs += FrameMs
	}
	return w
}

// InformativeMs sums the weights.
func InformativeMs(w []int32) int32 {
	var s int32
	for _, x := range w {
		s += x
	}
	return s
}

// DurationBucket is floor(ln(informative_s)/ln(1.25)); part of every index
// key. Informative durations under one second all land in bucket 0
// (ln of a value below 1 is negative, and nothing that short is indexed).
func DurationBucket(informativeMs int32) int {
	s := float64(informativeMs) / 1000
	if s < 1 {
		return 0
	}
	return int(math.Floor(math.Log(s) / math.Log(1.25)))
}

// BucketsForRange lists every bucket that overlaps [lowS, highS] seconds:
// both endpoint buckets and everything between, so no pair is lost at a
// boundary (plan 12.3). Callers pass [0.75·C, 1.25·C].
func BucketsForRange(lowS, highS float64) []int {
	if lowS < 1 {
		lowS = 1
	}
	if highS < lowS {
		highS = lowS
	}
	lo := int(math.Floor(math.Log(lowS) / math.Log(1.25)))
	hi := int(math.Floor(math.Log(highS) / math.Log(1.25)))
	out := make([]int, 0, hi-lo+1)
	for b := lo; b <= hi; b++ {
		out = append(out, b)
	}
	return out
}

// LookupBuckets is BucketsForRange for a copy of informative duration C:
// [0.75·C, 1.25·C].
func LookupBuckets(informativeMs int32) []int {
	c := float64(informativeMs) / 1000
	return BucketsForRange(0.75*c, 1.25*c)
}

// AnchorSpacingS is the grid spacing: clamp(floor(informative_s / 12), 1, 5),
// so every video with ≥ 12 informative seconds has ≥ 12 anchors.
func AnchorSpacingS(informativeMs int32) int {
	s := int(informativeMs/1000) / 12
	if s < 1 {
		return 1
	}
	if s > 5 {
		return 5
	}
	return s
}

// Anchor is one indexed frame.
type Anchor struct {
	TMs  int32
	Hash uint64
}

// Anchors picks the indexed frames: the frame on each grid second (spacing
// s), skipping flat frames, degenerate hashes, and any anchor within
// StaticHamming of the previous anchor.
func Anchors(frames []Frame, spacingS int) []Anchor {
	if spacingS < 1 {
		spacingS = 1
	}
	step := int32(spacingS * 1000)
	var out []Anchor
	var prev uint64
	havePrev := false
	for _, f := range frames {
		if f.TMs%step != 0 {
			continue
		}
		if f.Flags&(FlagFlat|FlagDegenerate) != 0 {
			continue
		}
		if havePrev && Hamming(prev, f.Hash) <= StaticHamming {
			continue
		}
		out = append(out, Anchor{TMs: f.TMs, Hash: f.Hash})
		prev, havePrev = f.Hash, true
	}
	return out
}

const (
	// QueryAllBelowMs: at or below this informative duration every frame
	// is a query frame.
	QueryAllBelowMs = 60_000
	// QueryWindowMaxMs bounds each of the three query windows.
	QueryWindowMaxMs = 20_000
	// QueryMaxFrames is the ceiling the windowing rule yields.
	QueryMaxFrames = 120
)

// QueryFrames selects the frames that probe the index: every weighted
// frame when the informative duration is ≤ 60 s; otherwise three windows of
// min(20 s, 10% of the duration) centred at 20%, 50% and 80% of the
// (raw) duration, giving ≤ 120 frames.
//
// Frames of weight 0 (flat, or a static repeat past the 2 s cap) never
// probe, and neither do degenerate hashes. A zero-weight frame can add no
// coverage, and letting the sixty frames of a 30 s slide all probe spreads
// the vote over sixty offsets (every one of them "matches" the slide's one
// anchor), so the winning offset would be arbitrary and the alignment would
// land on frames that carry no weight.
func QueryFrames(frames []Frame) []Frame {
	if len(frames) == 0 {
		return nil
	}
	w := Weights(frames)
	informativeMs := InformativeMs(w)
	var out []Frame
	if informativeMs <= QueryAllBelowMs {
		for i, f := range frames {
			if w[i] > 0 && f.Flags&FlagDegenerate == 0 {
				out = append(out, f)
			}
		}
		return out
	}
	durationMs := int64(frames[len(frames)-1].TMs) + FrameMs
	windowMs := durationMs / 10
	if windowMs > QueryWindowMaxMs {
		windowMs = QueryWindowMaxMs
	}
	inWindow := func(t int64) bool {
		for _, c := range []int64{durationMs * 20 / 100, durationMs * 50 / 100, durationMs * 80 / 100} {
			if t >= c-windowMs/2 && t < c+windowMs/2 {
				return true
			}
		}
		return false
	}
	for i, f := range frames {
		if w[i] == 0 || f.Flags&FlagDegenerate != 0 {
			continue
		}
		if inWindow(int64(f.TMs)) {
			out = append(out, f)
		}
	}
	if len(out) > QueryMaxFrames {
		out = out[:QueryMaxFrames]
	}
	return out
}
