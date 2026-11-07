package aggregation

import (
	"strings"
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

// Copyright quick wins (migration 014): the rollup's eligibility gate has
// the floor, so a held day never reaches the summary once the content is
// eligible again (eligibility_floor_integration_test.go runs it on
// Postgres). Pinned here so the clause cannot be dropped silently.
func TestDailyInsertHonoursTheEligibilityFloor(t *testing.T) {
	for _, want := range []string{
		"o.eligibility_eligible_from IS NULL OR o.eligibility_eligible_from < $3",
		"o.eligibility_state = 'eligible'",
		"o.eligibility_effective_from >= $3",
	} {
		if !strings.Contains(dailyInsertSQL, want) {
			t.Fatalf("dailyInsertSQL lost %q", want)
		}
	}
}

// FinalBefore is the rule monetization prices on (migration 016's view).
// A day it calls final must be frozen, so the catch-up walk can never
// rewrite it; and the watermark can only hold finality back, never bring
// it forward.
func TestFinalBeforeNeverFinalisesAnOpenDay(t *testing.T) {
	now := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	today := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	if _, ok := FinalBefore(time.Time{}, false, now); ok {
		t.Fatal("no watermark yet must mean nothing is final")
	}

	// Watermark where the walk leaves it: the earliest open day.
	fb, ok := FinalBefore(today.AddDate(0, 0, -2), true, now)
	if !ok || !fb.Equal(today.AddDate(0, 0, -2)) {
		t.Fatalf("final_before = %s, want %s", fb.Format("2006-01-02"), today.AddDate(0, 0, -2).Format("2006-01-02"))
	}

	// A watermark ahead of the clock (a hand edit, a skewed pod) must not
	// finalise yesterday or the day before.
	fb, _ = FinalBefore(today.AddDate(0, 0, 5), true, now)
	if !fb.Equal(today.AddDate(0, 0, -2)) {
		t.Fatalf("a future watermark finalised %s", fb.Format("2006-01-02"))
	}

	// A watermark the walk has not advanced (rollup down) holds days back.
	stuck := today.AddDate(0, 0, -6)
	fb, _ = FinalBefore(stuck, true, now)
	if !fb.Equal(stuck) {
		t.Fatalf("a stuck watermark must hold finality at %s, got %s", stuck.Format("2006-01-02"), fb.Format("2006-01-02"))
	}

	// Every day FinalBefore calls final is frozen, and the first day it
	// does not is still open.
	for h := 0; h < 24*4; h++ {
		at := today.Add(time.Duration(h) * time.Hour)
		fb, _ := FinalBefore(at.AddDate(0, 0, 30), true, at)
		if !IsFrozen(fb.AddDate(0, 0, -1), at) {
			t.Fatalf("at %s the last final day %s is not frozen", at, fb.AddDate(0, 0, -1).Format("2006-01-02"))
		}
		if IsFrozen(fb, at) {
			t.Fatalf("at %s day %s is frozen but not yet final", at, fb.Format("2006-01-02"))
		}
	}
}
