package aggregation

import (
	"testing"
	"time"
)

// The cap is read once, at construction, from the environment. These
// pin the three answers that matter: the default (one), an explicit
// override, and the "no cap" escape hatch that reproduces the uncapped
// per-session numbers.
func TestViewCapFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
		raw  string
		want int
	}{
		{name: "unset defaults to one", want: defaultViewCapPerViewerPerDay},
		{name: "empty defaults to one", set: true, raw: "  ", want: defaultViewCapPerViewerPerDay},
		{name: "explicit value is honoured", set: true, raw: "3", want: 3},
		{name: "zero disables the cap", set: true, raw: "0", want: 0},
		{name: "negative disables the cap", set: true, raw: "-1", want: -1},
		{name: "garbage falls back to the default", set: true, raw: "many", want: defaultViewCapPerViewerPerDay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(viewCapEnv, tc.raw)
			}
			if got := viewCapFromEnv(); got != tc.want {
				t.Fatalf("viewCapFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
	if defaultViewCapPerViewerPerDay != 1 {
		t.Fatalf("defaultViewCapPerViewerPerDay = %d, want 1 (one rewarded view per viewer per video per day)", defaultViewCapPerViewerPerDay)
	}
}

func TestWithViewCapOverridesTheEnvironment(t *testing.T) {
	t.Setenv(viewCapEnv, "9")
	rollup := (&DailyRollup{viewCapPerViewerPerDay: viewCapFromEnv()}).WithViewCap(2)
	if rollup.ViewCap() != 2 {
		t.Fatalf("ViewCap() = %d, want 2", rollup.ViewCap())
	}
}

// The freeze is a pure function of the day and the clock. A day is
// frozen exactly 48 hours after it ends: at 2026-09-11T00:00Z the days
// still open are the 9th, the 10th and the 11th; the 8th froze at that
// instant.
func TestFreezeWindow(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		day    time.Time
		at     time.Time
		frozen bool
	}{
		{day(11), now, false},
		{day(10), now, false},
		{day(9), now, false},
		{day(8), now, true},
		{day(1), now, true},
		// One nanosecond before the 8th's window closes it is open.
		{day(8), now.Add(-time.Nanosecond), false},
		// Mid-day clocks: the 9th stays open until the 12th at 00:00.
		{day(9), time.Date(2026, 9, 11, 23, 59, 59, 0, time.UTC), false},
		{day(9), time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), true},
		// A non-midnight day value is truncated to its day first.
		{time.Date(2026, 9, 8, 15, 30, 0, 0, time.UTC), now, true},
	} {
		if got := IsFrozen(tc.day, tc.at); got != tc.frozen {
			t.Errorf("IsFrozen(%s at %s) = %v, want %v", tc.day.Format(time.RFC3339), tc.at.Format(time.RFC3339), got, tc.frozen)
		}
	}
}

// A nil resolver, or one that answers anything but "sessions", is the
// play_end path. Money must never switch tables on a typo.
func TestResolveViewSourceFailsToPlayEnd(t *testing.T) {
	at := time.Now()
	if got := resolveViewSource(nil, at); got != ViewSourcePlayEnd {
		t.Fatalf("nil resolver = %q, want play_end", got)
	}
	for _, answer := range []string{"", "session", "SESSIONS", "play_end", "playback_sessions"} {
		if got := resolveViewSource(fixedAnswer(answer), at); got != ViewSourcePlayEnd {
			t.Fatalf("resolver answering %q = %q, want play_end", answer, got)
		}
	}
	if got := resolveViewSource(fixedAnswer(ViewSourceSessions), at); got != ViewSourceSessions {
		t.Fatalf("resolver answering sessions = %q", got)
	}
}

type fixedAnswer string

func (f fixedAnswer) ViewSourceFor(time.Time) string { return string(f) }
