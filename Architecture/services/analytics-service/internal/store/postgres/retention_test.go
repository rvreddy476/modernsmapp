package postgres

import (
	"math"
	"testing"
)

// The retention curve from coverage bitmaps, pinned without a database.

func coveredSeconds(from, to int64, durationMS int64) []byte {
	return markCoverage(nil, durationMS, from*1000, to*1000)
}

func TestRetentionBucketsAveragesCoverageAcrossSessions(t *testing.T) {
	sessions := []SessionCoverage{
		{DurationMS: 100_000, WatchedMS: 50_000, PercentCovered: 50, Coverage: coveredSeconds(0, 50, 100_000)},
		{DurationMS: 100_000, WatchedMS: 100_000, PercentCovered: 100, Coverage: coveredSeconds(0, 100, 100_000)},
	}
	curve := RetentionBuckets(sessions, 100)
	if len(curve) != 100 {
		t.Fatalf("len=%d", len(curve))
	}
	for b, v := range curve {
		want := 0.5
		if b < 50 {
			want = 1
		}
		if math.Abs(v-want) > 1e-9 {
			t.Fatalf("bucket %d = %v, want %v", b, v, want)
		}
	}
}

func TestRetentionBucketsNormalisesDifferentDurations(t *testing.T) {
	// A 30-second flick watched to the end and a 2-hour film watched to
	// the middle: the film's second half is the only uncovered part.
	sessions := []SessionCoverage{
		{DurationMS: 30_000, Coverage: coveredSeconds(0, 30, 30_000)},
		{DurationMS: 7_200_000, Coverage: coveredSeconds(0, 3600, 7_200_000)},
	}
	curve := RetentionBuckets(sessions, 100)
	if curve[0] != 1 || curve[49] != 1 {
		t.Fatalf("first half: %v %v", curve[0], curve[49])
	}
	if curve[50] != 0.5 || curve[99] != 0.5 {
		t.Fatalf("second half: %v %v", curve[50], curve[99])
	}
}

func TestRetentionBucketsLeavesOutSessionsWithoutABitmapOrDuration(t *testing.T) {
	sessions := []SessionCoverage{
		{DurationMS: 100_000, Coverage: coveredSeconds(0, 100, 100_000)},
		{DurationMS: 100_000, Coverage: nil},    // backfilled: no position data
		{DurationMS: 0, Coverage: []byte{0xff}}, // unknown duration
	}
	curve := RetentionBuckets(sessions, 100)
	for b, v := range curve {
		if v != 1 {
			t.Fatalf("bucket %d = %v; sessions without a bitmap must not drag the curve", b, v)
		}
	}
	empty := RetentionBuckets(nil, 100)
	if len(empty) != 100 {
		t.Fatalf("no sessions: len=%d, want a stable 100-zero array", len(empty))
	}
	for _, v := range empty {
		if v != 0 {
			t.Fatal("no sessions must be all zeros")
		}
	}
}

func TestRetentionBucketsShortContentSamplesEveryBucket(t *testing.T) {
	// 5 seconds of content across 100 buckets: every bucket still reads
	// one whole second, so nothing divides by zero and the curve is
	// monotone in the coverage.
	sessions := []SessionCoverage{{DurationMS: 5_000, Coverage: coveredSeconds(0, 3, 5_000)}}
	curve := RetentionBuckets(sessions, 100)
	if curve[0] != 1 || curve[59] != 1 || curve[60] != 0 || curve[99] != 0 {
		t.Fatalf("curve = %v", curve)
	}
}

func TestSessionAverages(t *testing.T) {
	sessions := []SessionCoverage{
		{DurationMS: 100_000, WatchedMS: 50_000, PercentCovered: 50},
		{DurationMS: 100_000, WatchedMS: 100_000, PercentCovered: 100},
		{DurationMS: 100_000, WatchedMS: 30_000, PercentCovered: 30}, // no bitmap: still a session
		{DurationMS: 0, WatchedMS: 999_999, PercentCovered: 999},     // unknown duration: skipped
	}
	watched, percent := SessionAverages(sessions)
	if watched != 60_000 || percent != 60 {
		t.Fatalf("averages = %v ms, %v%%; want 60000, 60", watched, percent)
	}
	if w, p := SessionAverages(nil); w != 0 || p != 0 {
		t.Fatalf("no sessions: %v %v", w, p)
	}
}
