package http

import "testing"

// Tube watch-progress contract (2026-09-05): completed=true from the player
// is honored; otherwise 90% of a known duration counts as finished.
func TestWatchProgressState(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name          string
		pos, dur      int
		explicit      *bool
		wantPct       float32
		wantCompleted bool
	}{
		{"halfway", 50_000, 100_000, nil, 50, false},
		{"ninety percent", 90_000, 100_000, nil, 90, true},
		{"explicit completed early", 10_000, 100_000, &yes, 10, true},
		{"explicit false does not override the rule", 95_000, 100_000, &no, 95, true},
		{"unknown duration never completes on its own", 10_000, 0, nil, 0, false},
		{"unknown duration but the player says done", 10_000, 0, &yes, 0, true},
		{"position past the end is clamped", 120_000, 100_000, nil, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pct, completed := watchProgressState(tc.pos, tc.dur, tc.explicit)
			if pct != tc.wantPct || completed != tc.wantCompleted {
				t.Fatalf("got pct=%v completed=%v want pct=%v completed=%v", pct, completed, tc.wantPct, tc.wantCompleted)
			}
		})
	}
}

// duration_ms: 0 from the player means "I don't know yet", not "the video is
// zero seconds long". The handler substitutes the duration the server already
// holds before deriving the percent, so a 30 s position into a 60 s video is
// 50%, not the 0% oddity. effectiveWatchDurationMs is that substitution.
func TestWatchProgressZeroDurationUsesKnownDuration(t *testing.T) {
	cases := []struct {
		name          string
		sent, known   int
		pos           int
		wantPct       float32
		wantCompleted bool
	}{
		{"client 0, server knows 60s", 0, 60_000, 30_000, 50, false},
		{"client 0, server knows 60s, near the end", 0, 60_000, 57_000, 95, true},
		{"client 0, server knows nothing", 0, 0, 30_000, 0, false},
		{"client sent a duration: it wins over the server's", 120_000, 60_000, 30_000, 25, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dur := effectiveWatchDurationMs(tc.sent, tc.known)
			pct, completed := watchProgressState(tc.pos, dur, nil)
			if pct != tc.wantPct || completed != tc.wantCompleted {
				t.Fatalf("got pct=%v completed=%v want pct=%v completed=%v", pct, completed, tc.wantPct, tc.wantCompleted)
			}
		})
	}
}

func TestParseContentTypeFilter(t *testing.T) {
	got, err := parseContentTypeFilter(" long_video, video ,flick")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "long_video" || got[1] != "flick" {
		t.Fatalf("got %v want [long_video flick] (legacy spelling folded, deduped)", got)
	}
	if got, err := parseContentTypeFilter(""); err != nil || got != nil {
		t.Fatalf("empty filter: %v %v", got, err)
	}
	if _, err := parseContentTypeFilter("movie"); err == nil {
		t.Fatal("unknown content type accepted")
	}
}
