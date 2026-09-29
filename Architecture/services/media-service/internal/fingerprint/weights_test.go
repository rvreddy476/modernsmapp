package fingerprint

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// seq builds a 2 fps sequence of n frames from a hash generator.
func seq(n int, hash func(i int) uint64, flags func(i int) uint8) []Frame {
	out := make([]Frame, n)
	for i := range out {
		out[i] = Frame{TMs: int32(i * FrameMs), Hash: hash(i)}
		if flags != nil {
			out[i].Flags = flags(i)
		}
	}
	return out
}

// distinctHash returns pseudo-random hashes (splitmix64 of i): pairwise
// Hamming ≈ 32, so two different indexes never read as the same picture.
func distinctHash(i int) uint64 { return splitmix64(uint64(i)) }

func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	z := x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func TestFlagStaticsAndWeightsCapStaticRuns(t *testing.T) {
	// 10 s of one slide with a black flash at 5 s, then 10 s of another.
	slideA, slideB := distinctHash(1), distinctHash(2)
	frames := seq(41, func(i int) uint64 {
		switch {
		case i == 10:
			return 0
		case i < 20:
			return slideA
		default:
			return slideB
		}
	}, func(i int) uint8 {
		if i == 10 {
			return FlagFlat
		}
		return 0
	})
	FlagStatics(frames)
	if frames[0].Flags&FlagStaticRepeat != 0 || frames[1].Flags&FlagStaticRepeat == 0 {
		t.Fatal("the first frame of a run is not a repeat; the second is")
	}
	if frames[11].Flags&FlagStaticRepeat == 0 {
		t.Fatal("a flat flash inside a slide must not break the run")
	}
	if frames[20].Flags&FlagStaticRepeat != 0 || frames[21].Flags&FlagStaticRepeat == 0 {
		t.Fatal("a new slide starts a new run")
	}
	w := Weights(frames)
	if w[10] != 0 {
		t.Fatal("flat frame weighs 0")
	}
	// Each slide contributes 2 s = 4 frames.
	if got := InformativeMs(w); got != 2*StaticRunCapMs {
		t.Fatalf("informative %d ms, want %d", got, 2*StaticRunCapMs)
	}
	// A changing sequence has full weight.
	changing := seq(20, distinctHash, nil)
	FlagStatics(changing)
	if got := InformativeMs(Weights(changing)); got != 20*FrameMs {
		t.Fatalf("changing informative %d, want %d", got, 20*FrameMs)
	}
}

func TestDurationBucketEdges(t *testing.T) {
	// Bucket boundaries at 1.25^k seconds.
	for k := 0; k < 30; k++ {
		edge := math.Pow(1.25, float64(k))
		ms := int32(math.Round(edge * 1000))
		// Exactly on the edge (rounded to ms) lands in k or k-1 depending on
		// rounding; 1 ms above the edge must be in bucket k, 1 ms below the
		// previous... just check monotonic and that the lookup range covers.
		above := DurationBucket(ms + 2)
		below := DurationBucket(ms - 2)
		if above < below {
			t.Fatalf("k=%d: bucket not monotonic (%d < %d)", k, above, below)
		}
		if above-below > 1 {
			t.Fatalf("k=%d: a 4 ms step crossed %d buckets", k, above-below)
		}
	}
	if DurationBucket(500) != 0 || DurationBucket(999) != 0 || DurationBucket(1000) != 0 {
		t.Fatal("sub-second durations must be bucket 0")
	}
	if DurationBucket(1250) != 1 || DurationBucket(1249) != 0 {
		t.Fatalf("1.25 s edge: %d/%d", DurationBucket(1250), DurationBucket(1249))
	}
}

// T9-5: copies on, 1 ms below and 1 ms above a bucket boundary, and at
// 0.75× and 1.25× the copy duration: the reference bucket is always in the
// list the copy queries.
func TestLookupBucketsCoverTheRange(t *testing.T) {
	for _, copyS := range []float64{3, 10, 12.5, 60, 600, 3600, 4 * 3600} {
		copyMs := int32(copyS * 1000)
		buckets := LookupBuckets(copyMs)
		have := map[int]bool{}
		for _, b := range buckets {
			have[b] = true
		}
		for _, factor := range []float64{0.75, 0.8, 1.0, 1.176, 1.25} {
			refS := copyS * factor
			for _, delta := range []float64{-0.001, 0, 0.001} {
				refMs := int32(math.Round((refS + delta) * 1000))
				if refMs < 1000 {
					continue
				}
				if b := DurationBucket(refMs); !have[b] {
					t.Errorf("copy %.3fs: reference %.3fs (bucket %d) not in %v", copyS, refS+delta, b, buckets)
				}
			}
		}
		// The list is contiguous.
		for i := 1; i < len(buckets); i++ {
			if buckets[i] != buckets[i-1]+1 {
				t.Errorf("copy %.0fs: buckets %v not contiguous", copyS, buckets)
			}
		}
	}
	// Exactly-on-boundary copies: a bucket edge at 1.25^k with the copy just
	// under it and the reference at 1.25× just over the next edge.
	for k := 1; k < 20; k++ {
		edge := math.Pow(1.25, float64(k))
		copyMs := int32(edge*1000) - 1
		refMs := int32(math.Round(float64(copyMs) * 1.25))
		have := false
		for _, b := range LookupBuckets(copyMs) {
			if b == DurationBucket(refMs) {
				have = true
			}
		}
		if !have {
			t.Errorf("k=%d: copy %d ms misses reference %d ms", k, copyMs, refMs)
		}
	}
}

func TestAnchorSpacing(t *testing.T) {
	cases := map[int32]int{5_000: 1, 11_999: 1, 12_000: 1, 24_000: 2, 59_000: 4, 60_000: 5, 3_600_000: 5}
	for ms, want := range cases {
		if got := AnchorSpacingS(ms); got != want {
			t.Errorf("spacing(%d) = %d, want %d", ms, got, want)
		}
	}
}

func TestAnchorsSkipFlatDegenerateAndRepeats(t *testing.T) {
	frames := seq(20, distinctHash, func(i int) uint8 {
		if i == 4 {
			return FlagFlat
		}
		if i == 6 {
			return FlagDegenerate
		}
		return 0
	})
	// Make the anchor at 8 s a near-repeat of the one at 7 s... spacing 1 s
	// means frames 0,2,4,...; make frame 10 within Hamming 2 of frame 8.
	frames[10].Hash = frames[8].Hash ^ 0x3
	a := Anchors(frames, 1)
	var times []int32
	for _, x := range a {
		times = append(times, x.TMs)
	}
	// index 4 (2000 ms) is flat, index 6 (3000 ms) degenerate, index 10
	// (5000 ms) a near-repeat of the 4000 ms anchor: all three skipped.
	want := []int32{0, 1000, 4000, 6000, 7000, 8000, 9000}
	if len(times) != len(want) {
		t.Fatalf("anchors %v, want %v", times, want)
	}
	for i := range want {
		if times[i] != want[i] {
			t.Fatalf("anchors %v, want %v", times, want)
		}
	}
	// Spacing 5 keeps only multiples of 5 s.
	for _, x := range Anchors(frames, 5) {
		if x.TMs%5000 != 0 {
			t.Fatalf("anchor at %d with spacing 5", x.TMs)
		}
	}
	// ≥ 12 anchors for any video ≥ 12 informative seconds (changing content).
	long := seq(2*3600*FPS, distinctHash, nil)
	if n := len(Anchors(long, AnchorSpacingS(int32(len(long)*FrameMs)))); n < 12 {
		t.Fatalf("2 h video has %d anchors", n)
	}
}

func TestQueryFramesWindowing(t *testing.T) {
	short := seq(100, distinctHash, func(i int) uint8 {
		if i%10 == 0 {
			return FlagFlat
		}
		return 0
	})
	if q := QueryFrames(short); len(q) != 90 {
		t.Fatalf("≤ 60 s: %d query frames, want every non-flat (90)", len(q))
	}
	// A 30 s slide contributes only its weighted 2 s (4 frames) of query
	// frames, not sixty probes of one picture.
	slide := seq(60, func(int) uint64 { return distinctHash(3) }, nil)
	FlagStatics(slide)
	if q := QueryFrames(slide); len(q) != StaticRunCapMs/FrameMs {
		t.Fatalf("static slide: %d query frames, want %d", len(q), StaticRunCapMs/FrameMs)
	}
	// 10 minutes: three 20 s windows at 2 fps = 120 frames.
	long := seq(600*FPS, distinctHash, nil)
	q := QueryFrames(long)
	if len(q) != QueryMaxFrames {
		t.Fatalf("10 min: %d query frames, want %d", len(q), QueryMaxFrames)
	}
	centres := []int32{120_000, 300_000, 480_000}
	for _, f := range q {
		ok := false
		for _, c := range centres {
			if f.TMs >= c-10_000 && f.TMs < c+10_000 {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("query frame at %d ms outside every window", f.TMs)
		}
	}
	// 90 s: windows of 9 s (10%).
	mid := seq(90*FPS, distinctHash, nil)
	if q := QueryFrames(mid); len(q) != 3*9*FPS {
		t.Fatalf("90 s: %d query frames, want %d", len(q), 3*9*FPS)
	}
	if QueryFrames(nil) != nil {
		t.Fatal("empty input")
	}
}
