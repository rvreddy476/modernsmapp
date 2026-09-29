package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func at(t0 time.Time, d time.Duration) *time.Time {
	x := t0.Add(d)
	return &x
}

// T11-6 and the precedence rules (plan 6.1): the earlier confirm is
// earlier; ≤ 60 s apart is contemporaneous; a legacy side within 24 h is
// ambiguous; a legacy side beyond 24 h is decided.
func TestUploadDirection(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	conf := func(d time.Duration) PairSide {
		return PairSide{UploadConfirmed: at(t0, d), UploadTimeSource: UploadTimeConfirmed}
	}
	legacy := func(d time.Duration) PairSide {
		return PairSide{UploadConfirmed: at(t0, d), UploadTimeSource: UploadTimeLegacy}
	}
	cases := []struct {
		name   string
		lo, hi PairSide
		want   string
	}{
		{"lo earlier by a day", conf(0), conf(24 * time.Hour), DirectionLoEarlier},
		{"hi earlier by a day", conf(24 * time.Hour), conf(0), DirectionHiEarlier},
		{"lo earlier by 61 s", conf(0), conf(61 * time.Second), DirectionLoEarlier},
		{"30 s apart", conf(0), conf(30 * time.Second), DirectionContemporaneous},
		{"exactly 60 s apart", conf(60 * time.Second), conf(0), DirectionContemporaneous},
		{"legacy within 24 h", legacy(0), conf(23 * time.Hour), DirectionAmbiguousLegacy},
		{"legacy at 24 h exactly is decided", legacy(0), conf(24 * time.Hour), DirectionLoEarlier},
		{"legacy 30 s apart is ambiguous, not contemporaneous", conf(0), legacy(30 * time.Second), DirectionAmbiguousLegacy},
		{"both legacy, 2 days apart", legacy(0), legacy(48 * time.Hour), DirectionLoEarlier},
		{"missing time", PairSide{}, conf(0), DirectionAmbiguousLegacy},
	}
	for _, c := range cases {
		if got := UploadDirection(c.lo, c.hi); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// Both jobs of a pair write the same row: lo/hi by byte order, and
// ref/copy coverage in the stored orientation (ref = the earlier side, or
// lo when there is none), whichever side computed it.
func TestPairWriteCanonicalOrientation(t *testing.T) {
	t0 := time.Now()
	a := PairSide{MediaID: uuid.MustParse("00000000-0000-4000-8000-00000000000a"), Generation: 1, UploadConfirmed: at(t0, 0), UploadTimeSource: UploadTimeConfirmed}
	b := PairSide{MediaID: uuid.MustParse("00000000-0000-4000-8000-00000000000b"), Generation: 2, UploadConfirmed: at(t0, 48*time.Hour), UploadTimeSource: UploadTimeConfirmed}
	// Job for b (the copy) found a (the ref): a is covered 0.9, b 0.6.
	fromB := PairWrite{Copy: b, Ref: a, Algo: 1, PairScores: PairScores{RefCoverage: 0.9, CopyCoverage: 0.6}}
	// Job for a (the copy) found b (the ref): a is covered 0.9, b 0.6.
	fromA := PairWrite{Copy: a, Ref: b, Algo: 1, PairScores: PairScores{RefCoverage: 0.6, CopyCoverage: 0.9}}
	lo1, hi1, dir1, ref1, copy1 := fromB.canonical()
	lo2, hi2, dir2, ref2, copy2 := fromA.canonical()
	if lo1 != a || hi1 != b || lo2 != a || hi2 != b {
		t.Fatalf("lo/hi order differs: %v/%v vs %v/%v", lo1.MediaID, hi1.MediaID, lo2.MediaID, hi2.MediaID)
	}
	if dir1 != DirectionLoEarlier || dir2 != DirectionLoEarlier {
		t.Fatalf("direction %s / %s", dir1, dir2)
	}
	if ref1 != 0.9 || copy1 != 0.6 || ref2 != 0.9 || copy2 != 0.6 {
		t.Fatalf("coverage orientation differs: (%.1f,%.1f) vs (%.1f,%.1f)", ref1, copy1, ref2, copy2)
	}
	// hi earlier: ref becomes hi's coverage.
	b.UploadConfirmed = at(t0, -48*time.Hour)
	fromA = PairWrite{Copy: a, Ref: b, Algo: 1, PairScores: PairScores{RefCoverage: 0.6, CopyCoverage: 0.9}}
	_, _, dir, ref, cp := fromA.canonical()
	if dir != DirectionHiEarlier || ref != 0.6 || cp != 0.9 {
		t.Fatalf("hi earlier: %s %.1f %.1f", dir, ref, cp)
	}
}

func TestScoresEqualToleratesFloatNoise(t *testing.T) {
	a := [6]float64{0.85, 0.8, 10, 9.5, 3, 5}
	b := [6]float64{0.8501, 0.8, 10, 9.5, 3, 5}
	c := [6]float64{0.851, 0.8, 10, 9.5, 3, 5}
	if !scoresEqual(a, b, 4, 4) {
		t.Fatal("a 1e-4 difference must not bump a revision")
	}
	if scoresEqual(a, c, 4, 4) || scoresEqual(a, a, 4, 5) {
		t.Fatal("a real change must")
	}
}

// The reprocess-of-a-ready-asset rule (O-obs-1): a ready asset is a
// candidate only with an outstanding reprocess; at the attempt cap it is
// abandoned, never marked failed.
func TestClassifyStalledReprocessOfReadyAsset(t *testing.T) {
	now := time.Now()
	p := DefaultStallPolicy()
	stale := now.Add(-20 * time.Minute)
	ready := StalledTranscodeCandidate{FileType: "video", ProcessingStatus: "ready", HeartbeatAt: &stale, UpdatedAt: now.Add(-time.Hour)}
	if v := ClassifyStalledTranscode(ready, p, now, false); v.Action != StallSkip {
		t.Fatalf("a finished ready asset with a stale heartbeat: %+v, want skip", v)
	}
	ready.OutstandingReprocess = true
	if v := ClassifyStalledTranscode(ready, p, now, false); v.Action != StallRequeue {
		t.Fatalf("dead reprocess: %+v, want requeue", v)
	}
	ready.Attempts = p.MaxAttempts
	if v := ClassifyStalledTranscode(ready, p, now, false); v.Action != StallAbandonReprocess {
		t.Fatalf("dead reprocess at the cap: %+v, want abandon", v)
	}
	// An unpublished re-request is in flight; leave it.
	ready.InFlight = true
	if v := ClassifyStalledTranscode(ready, p, now, false); v.Action != StallSkip {
		t.Fatalf("in flight: %+v", v)
	}
	// A processing asset at the cap is still failed, as before.
	proc := StalledTranscodeCandidate{FileType: "video", ProcessingStatus: "processing", HeartbeatAt: &stale, UpdatedAt: now.Add(-time.Hour), Attempts: p.MaxAttempts}
	if v := ClassifyStalledTranscode(proc, p, now, false); v.Action != StallGiveUp {
		t.Fatalf("processing at the cap: %+v", v)
	}
}
