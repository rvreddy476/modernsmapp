package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The coverage bitmap is what makes percent_covered honest: a second of
// content is covered once however many times it is replayed, so
// watching the first half twice cannot read as 100% (audit M-18).
func TestCoverageBitmapCountsEachSecondOnce(t *testing.T) {
	const duration = 10_000 // 10s -> 10 bits, 2 bytes

	var bm []byte
	// First half, twice.
	bm = markCoverage(bm, duration, 0, 5_000)
	bm = markCoverage(bm, duration, 0, 5_000)
	if got := coverageSeconds(bm); got != 5 {
		t.Fatalf("first half twice covered %d seconds, want 5", got)
	}
	// The bitmap grows lazily to what was marked, not to the duration.
	if len(bm) != 1 {
		t.Fatalf("five marked seconds need 1 byte, got %d", len(bm))
	}
	// Second half once: now complete, and two bytes.
	bm = markCoverage(bm, duration, 5_000, 10_000)
	if got := coverageSeconds(bm); got != 10 {
		t.Fatalf("both halves covered %d seconds, want 10", got)
	}
	if len(bm) != 2 {
		t.Fatalf("a fully covered 10s clip needs 2 bytes, got %d", len(bm))
	}
	// Overrun past the end is clamped, not grown.
	bm = markCoverage(bm, duration, 9_000, 14_000)
	if got := coverageSeconds(bm); got != 10 || len(bm) != 2 {
		t.Fatalf("overrun: seconds=%d bytes=%d", got, len(bm))
	}
}

func TestCoverageBitmapMarksALoopWrapAsTwoRanges(t *testing.T) {
	const duration = 10_000
	// A 2s beat that crossed the loop point: playhead 1s, so it covered
	// 9s..10s of the previous pass and 0..1s of this one.
	bm := markCoverage(nil, duration, 1_000-2_000, 1_000)
	if got := coverageSeconds(bm); got != 2 {
		t.Fatalf("wrap covered %d seconds, want 2 (second 9 and second 0)", got)
	}
	if bm[0]&1 == 0 {
		t.Fatal("second 0 not marked")
	}
	if bm[1]&(1<<1) == 0 {
		t.Fatal("second 9 not marked")
	}
}

func TestCoverageBitmapGrowsWhenDurationIsUnknown(t *testing.T) {
	// play_start was lost; heartbeats arrive with no duration. The bitmap
	// grows to the playhead and is trimmed against the duration later.
	bm := markCoverage(nil, 0, 0, 20_000)
	if got := coverageSeconds(bm); got != 20 {
		t.Fatalf("unknown duration covered %d seconds, want 20", got)
	}
	bm = markCoverage(bm, 0, 20_000, 70_000)
	if got := coverageSeconds(bm); got != 70 {
		t.Fatalf("grown bitmap covered %d seconds, want 70", got)
	}
}

// A 300s flick is 38 bytes: one bit per second, rounded up.
func TestCoverageBitmapSize(t *testing.T) {
	bm := markCoverage(nil, 300_000, 0, 300_000)
	if len(bm) != 38 {
		t.Fatalf("300s bitmap is %d bytes, want 38", len(bm))
	}
	if got := coverageSeconds(bm); got != 300 {
		t.Fatalf("full watch covered %d seconds, want 300", got)
	}
}

// The measures derived from a row: percent_covered reads the bitmap
// when there is one and falls back to percent_viewed when there is not,
// so a session that only ever sent play_end is not scored as zero.
func TestDeriveSessionMeasuresFallsBackWithoutABitmap(t *testing.T) {
	row := &PlaybackSession{ContentDurationMS: 10_000, WatchedMS: 8_000}
	deriveSessionMeasures(row)
	if row.PercentViewed != 80 || row.PercentCovered != 80 || row.CoveredMS != 8_000 {
		t.Fatalf("no bitmap: viewed=%v covered=%v covered_ms=%d", row.PercentViewed, row.PercentCovered, row.CoveredMS)
	}

	// Looped: 20s watched of 10s, but only the first half was ever
	// covered — the bitmap says 50%, the legacy measure says 100%.
	row = &PlaybackSession{ContentDurationMS: 10_000, WatchedMS: 20_000, LoopCount: 1}
	row.Coverage = markCoverage(nil, 10_000, 0, 5_000)
	row.Coverage = markCoverage(row.Coverage, 10_000, 0, 5_000)
	deriveSessionMeasures(row)
	if row.PercentViewed != 100 {
		t.Fatalf("legacy percent_viewed=%v want 100", row.PercentViewed)
	}
	if row.PercentCovered != 50 || row.CoveredMS != 5_000 {
		t.Fatalf("percent_covered=%v covered_ms=%d want 50 / 5000", row.PercentCovered, row.CoveredMS)
	}

	// The flags: a display view scores its covered fraction, not the
	// rewatched one.
	row.ContentType = "flick"
	isDisplay, score := sessionFlags(row)
	if !isDisplay || score != 0.5 {
		t.Fatalf("flags: display=%v score=%v want true / 0.5", isDisplay, score)
	}
	row.WatchedMS, row.PercentViewed, row.LoopCount = 1_000, 10, 0
	row.Coverage, row.CoveredMS, row.PercentCovered = nil, 0, 0
	deriveSessionMeasures(row)
	if isDisplay, score := sessionFlags(row); isDisplay || score != 0 {
		t.Fatalf("1s of a 10s flick: display=%v score=%v want false / 0", isDisplay, score)
	}
}

func TestSessionUpdateWithoutSessionIDIsRefused(t *testing.T) {
	err := applySessionUpdate(nil, nil, Event{Session: &SessionUpdate{Kind: "play_end"}, Timestamp: time.Now()})
	if err == nil {
		t.Fatal("a playback update with the nil session was applied")
	}
	if err := applySessionUpdate(nil, nil, Event{SessionID: uuid.New()}); err != nil {
		t.Fatalf("a non-playback event (nil Session) must be a no-op, got %v", err)
	}
}
