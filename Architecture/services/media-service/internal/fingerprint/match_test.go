package fingerprint

import (
	"math/rand"
	"testing"
)

// perturb flips k random bits of every hash (a re-encode).
func perturb(frames []Frame, k int, seed int64) []Frame {
	r := rand.New(rand.NewSource(seed))
	out := make([]Frame, len(frames))
	for i, f := range frames {
		h := f.Hash
		for _, p := range r.Perm(64)[:k] {
			h ^= 1 << uint(p)
		}
		out[i] = Frame{TMs: f.TMs, Hash: h, Flags: f.Flags}
	}
	return out
}

// trim drops the first ms of a sequence and re-times it from 0.
func trim(frames []Frame, ms int32) []Frame {
	var out []Frame
	for _, f := range frames {
		if f.TMs >= ms {
			out = append(out, Frame{TMs: f.TMs - ms, Hash: f.Hash, Flags: f.Flags})
		}
	}
	return out
}

func postingsOf(frames []Frame, spacing int) []Posting {
	var out []Posting
	for _, a := range Anchors(frames, spacing) {
		out = append(out, Posting{AnchorMs: a.TMs, Hash: a.Hash})
	}
	return out
}

// T9-4: trims of 1 s, 0.4 s, 2.5 s and a 10% head all produce ≥ 2 votes in
// one bin with the offset within ±0.5 s of the truth.
func TestVoteFindsOffsetAfterTrims(t *testing.T) {
	ref := seq(120*FPS, distinctHash, nil) // 2 minutes, changing content
	FlagStatics(ref)
	spacing := AnchorSpacingS(InformativeMs(Weights(ref)))
	postings := postingsOf(ref, spacing)
	for _, trimMs := range []int32{1000, 400, 2500, 12_000} {
		cp := perturb(trim(ref, trimMs), 3, int64(trimMs))
		q := QueryFrames(cp)
		offsets := Vote(Hits(q, postings))
		if len(offsets) == 0 {
			t.Fatalf("trim %d ms: no offset voted", trimMs)
		}
		best := offsets[0]
		if best.Votes < MinVotes {
			t.Fatalf("trim %d ms: %d votes", trimMs, best.Votes)
		}
		if d := best.OffsetMs - trimMs; d > VoteBinMs || d < -VoteBinMs {
			t.Fatalf("trim %d ms: voted offset %d", trimMs, best.OffsetMs)
		}
	}
}

func TestVoteNeedsTwoHits(t *testing.T) {
	if got := Vote([]Hit{{QueryMs: 0, AnchorMs: 1000}}); got != nil {
		t.Fatalf("one hit voted: %+v", got)
	}
	// Two hits in adjacent bins count together.
	got := Vote([]Hit{{QueryMs: 0, AnchorMs: 1000}, {QueryMs: 500, AnchorMs: 1900}})
	if len(got) == 0 || got[0].Votes != 2 {
		t.Fatalf("adjacent bins: %+v", got)
	}
	// Two hits three bins apart do not.
	if got := Vote([]Hit{{QueryMs: 0, AnchorMs: 1000}, {QueryMs: 500, AnchorMs: 3000}}); got != nil {
		t.Fatalf("far bins voted: %+v", got)
	}
}

func alignScore(copyF, refF []Frame) (Alignment, Scores) {
	spacing := AnchorSpacingS(InformativeMs(Weights(refF)))
	postings := postingsOf(refF, spacing)
	q := QueryFrames(copyF)
	a := Align(copyF, refF, Vote(Hits(q, postings)))
	return a, Score(copyF, refF, a, false)
}

// A 5% head trim plus a re-encode is full_or_near_full.
func TestFullCopyAfterTrimAndReencode(t *testing.T) {
	ref := seq(60*FPS, distinctHash, nil)
	FlagStatics(ref)
	cp := perturb(trim(ref, 3000), 4, 5)
	a, s := alignScore(cp, ref)
	if len(a.Segments) != 1 {
		t.Fatalf("segments %+v", a.Segments)
	}
	if s.CopyCoverage < 0.99 || s.RefCoverage < 0.94 {
		t.Fatalf("coverage copy %.3f ref %.3f", s.CopyCoverage, s.RefCoverage)
	}
	if c := Classify(s); c != ClassFullOrNearFull {
		t.Fatalf("class %s: %+v", c, s)
	}
}

// T10-1: a 6-minute reference inside a 10-minute compilation is `contains`.
func TestCompilationIsContainsNotFull(t *testing.T) {
	ref := seq(360*FPS, distinctHash, nil)
	other := seq(240*FPS, func(i int) uint64 { return distinctHash(i + 100_000) }, nil)
	comp := append([]Frame{}, other[:120*FPS]...)
	for _, f := range ref {
		comp = append(comp, Frame{TMs: int32(len(comp) * FrameMs), Hash: f.Hash})
	}
	for _, f := range other[120*FPS:] {
		comp = append(comp, Frame{TMs: int32(len(comp) * FrameMs), Hash: f.Hash})
	}
	FlagStatics(ref)
	FlagStatics(comp)
	_, s := alignScore(comp, ref)
	if s.RefCoverage < 0.9 {
		t.Fatalf("ref coverage %.3f", s.RefCoverage)
	}
	if s.CopyCoverage > 0.65 {
		t.Fatalf("copy coverage %.3f should be ~0.6", s.CopyCoverage)
	}
	if c := Classify(s); c != ClassContains {
		t.Fatalf("class %s: %+v", c, s)
	}
}

// T10-5: a reference followed by five minutes of black: flat frames weigh
// 0 and the class is full_or_near_full.
func TestTrailingBlackIsIgnored(t *testing.T) {
	ref := seq(60*FPS, distinctHash, nil)
	cp := append([]Frame{}, ref...)
	for i := 0; i < 300*FPS; i++ {
		cp = append(cp, Frame{TMs: int32(len(cp) * FrameMs), Hash: 0, Flags: FlagFlat | FlagDegenerate})
	}
	FlagStatics(ref)
	FlagStatics(cp)
	_, s := alignScore(cp, ref)
	if c := Classify(s); c != ClassFullOrNearFull {
		t.Fatalf("class %s: %+v", c, s)
	}
	if s.CopyCoverage < 0.99 {
		t.Fatalf("copy coverage %.3f with black weighing 0", s.CopyCoverage)
	}
}

// T10-2: twenty 30 s slides cap at 2 s each; full_or_near_full needs
// diversity ≥ 3. A two-slide deck is insufficient evidence.
func TestSlideshowWeightsAndDiversity(t *testing.T) {
	slides := func(n int) []Frame {
		var out []Frame
		for s := 0; s < n; s++ {
			h := distinctHash(s + 1)
			for i := 0; i < 30*FPS; i++ {
				out = append(out, Frame{TMs: int32(len(out) * FrameMs), Hash: h})
			}
		}
		FlagStatics(out)
		return out
	}
	deck := slides(20)
	if inf := InformativeMs(Weights(deck)); inf != 20*StaticRunCapMs {
		t.Fatalf("informative %d, want %d", inf, 20*StaticRunCapMs)
	}
	cp := perturb(deck, 2, 9)
	_, s := alignScore(cp, deck)
	if c := Classify(s); c != ClassFullOrNearFull {
		t.Fatalf("20-slide copy: %s %+v", c, s)
	}
	two := slides(2)
	_, s2 := alignScore(perturb(two, 2, 10), two)
	if c := Classify(s2); c == ClassFullOrNearFull {
		t.Fatalf("two-slide deck must not be full: %+v", s2)
	}
}

// T10-7: a 2.5 s insert is bridged (no weight); a 4 s insert splits into
// two segments.
func TestGapBridging(t *testing.T) {
	ref := seq(60*FPS, distinctHash, nil)
	FlagStatics(ref)
	insert := func(at, lenMs int32) []Frame {
		var out []Frame
		for _, f := range ref {
			if f.TMs == at {
				for k := int32(0); k < lenMs; k += FrameMs {
					out = append(out, Frame{TMs: int32(len(out) * FrameMs), Hash: distinctHash(int(k) + 50_000)})
				}
			}
			out = append(out, Frame{TMs: int32(len(out) * FrameMs), Hash: f.Hash})
		}
		FlagStatics(out)
		return out
	}
	a, s := alignScore(insert(30_000, 2500), ref)
	if len(a.Segments) != 1 {
		t.Fatalf("2.5 s insert: %d segments %+v", len(a.Segments), a.Segments)
	}
	// The insert's 5 frames are not matched: coverage of the copy is 120/125.
	if s.CopyCoverage > 0.97 || s.CopyCoverage < 0.95 {
		t.Fatalf("bridged frames added weight: copy coverage %.3f", s.CopyCoverage)
	}
	a, _ = alignScore(insert(30_000, 4000), ref)
	if len(a.Segments) != 2 {
		t.Fatalf("4 s insert: %d segments %+v", len(a.Segments), a.Segments)
	}
	if a.Segments[0].OffsetMs != 0 || a.Segments[1].OffsetMs != -4000 {
		t.Fatalf("per-segment offsets %+v", a.Segments)
	}
}

// Different sources never align.
func TestUnrelatedSequencesDoNotMatch(t *testing.T) {
	a := seq(60*FPS, distinctHash, nil)
	b := seq(60*FPS, func(i int) uint64 { return distinctHash(i + 7777) }, nil)
	FlagStatics(a)
	FlagStatics(b)
	al, s := alignScore(a, b)
	if len(al.Segments) != 0 || s.MatchedInformativeS != 0 {
		t.Fatalf("unrelated matched: %+v %+v", al.Segments, s)
	}
	if Classify(s) != ClassInsufficientEvidence {
		t.Fatal("unrelated must be insufficient_evidence")
	}
}

// Short-clip and threshold table (T10-4 and the class rules).
func TestClassifyThresholds(t *testing.T) {
	base := Scores{RefCoverage: 0.9, CopyCoverage: 0.9, MatchedInformativeS: 20, MedianHamming: 3, Diversity: 5, RefInformativeS: 30, CopyInformativeS: 30}
	cases := []struct {
		name string
		mut  func(*Scores)
		want Class
	}{
		{"full", func(*Scores) {}, ClassFullOrNearFull},
		{"ref below 0.85 with 30 s matched", func(s *Scores) { s.RefCoverage = 0.84; s.MatchedInformativeS = 30 }, ClassPartial},
		{"ref below 0.85 with 29 s matched", func(s *Scores) { s.RefCoverage = 0.84; s.MatchedInformativeS = 29 }, ClassInsufficientEvidence},
		{"copy below 0.80", func(s *Scores) { s.CopyCoverage = 0.79 }, ClassContains},
		{"matched under 8 s", func(s *Scores) { s.MatchedInformativeS = 7.9 }, ClassInsufficientEvidence},
		{"diversity 2", func(s *Scores) { s.Diversity = 2 }, ClassInsufficientEvidence},
		{"truncated", func(s *Scores) { s.Truncated = true }, ClassInsufficientEvidence},
		{"copy side under 8 s informative", func(s *Scores) { s.CopyInformativeS = 7 }, ClassInsufficientEvidence},
		{"short reference identical", func(s *Scores) {
			s.RefInformativeS = 6
			s.CopyInformativeS = 6
			s.RefCoverage, s.CopyCoverage = 1, 1
			s.MatchedInformativeS = 6
			s.MedianHamming = 2
			s.Diversity = 2
		}, ClassShortClipCandidate},
		{"short reference median 7", func(s *Scores) {
			s.RefInformativeS = 6
			s.RefCoverage, s.CopyCoverage = 1, 1
			s.MedianHamming = 7
		}, ClassInsufficientEvidence},
		{"short reference coverage 0.94", func(s *Scores) {
			s.RefInformativeS = 6
			s.RefCoverage, s.CopyCoverage = 0.94, 1
		}, ClassInsufficientEvidence},
		{"short reference diversity 1", func(s *Scores) {
			s.RefInformativeS = 6
			s.RefCoverage, s.CopyCoverage = 1, 1
			s.Diversity = 1
		}, ClassInsufficientEvidence},
		{"reference 9.9 s is short", func(s *Scores) { s.RefInformativeS = 9.9; s.RefCoverage = 0.96; s.CopyCoverage = 0.96 }, ClassShortClipCandidate},
	}
	for _, c := range cases {
		s := base
		c.mut(&s)
		if got := Classify(s); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestScoreDiversityAndHamming(t *testing.T) {
	ref := seq(40, distinctHash, nil)
	cp := perturb(ref, 5, 3)
	FlagStatics(ref)
	FlagStatics(cp)
	_, s := alignScore(cp, ref)
	if s.Diversity != DiversityCap {
		t.Fatalf("diversity %d over 40 distinct frames, want the cap %d", s.Diversity, DiversityCap)
	}
	if s.MedianHamming != 5 || s.P90Hamming != 5 {
		t.Fatalf("hamming median %.0f p90 %.0f, want 5", s.MedianHamming, s.P90Hamming)
	}
	if s.MatchedS != 20 || s.MatchedInformativeS != 20 {
		t.Fatalf("matched %.1f informative %.1f", s.MatchedS, s.MatchedInformativeS)
	}
}

func TestDurationConsistent(t *testing.T) {
	if !DurationConsistent(60_500, 60_000) || !DurationConsistent(59_000, 60_000) {
		t.Fatal("within 1 s must pass")
	}
	// 2% of 60 s is 1.2 s: 1.2 s off passes, 1.201 s fails.
	if !DurationConsistent(61_200, 60_000) || DurationConsistent(61_201, 60_000) {
		t.Fatal("the 2% tolerance edge is wrong")
	}
	// Below 50 s the 1 s floor applies.
	if !DurationConsistent(11_000, 10_000) || DurationConsistent(11_001, 10_000) {
		t.Fatal("the 1 s floor is wrong")
	}
	if !DurationConsistent(5_000, 0) {
		t.Fatal("no recorded duration means nothing to disagree with")
	}
}
