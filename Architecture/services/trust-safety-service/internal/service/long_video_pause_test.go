package service

import (
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The repeat-uploader rule (founder quick wins 2026-10-24): three upheld
// copyright blocks within 90 days pause long-video uploads for 7 days from
// the block that made the third; not after two, not when the three are
// spread over more than 90 days, and the pause ends.
func TestLongVideoPause(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	for _, c := range []struct {
		name   string
		blocks []time.Time
		want   *time.Time
	}{
		{"none", nil, nil},
		{"two in the window", []time.Time{ago(10 * day), ago(1 * day)}, nil},
		{"three in the window", []time.Time{ago(80 * day), ago(40 * day), ago(1 * day)}, ptr(ago(1 * day).Add(7 * day))},
		{"three, order does not matter", []time.Time{ago(1 * day), ago(80 * day), ago(40 * day)}, ptr(ago(1 * day).Add(7 * day))},
		{"three spread over more than 90 days", []time.Time{ago(95 * day), ago(50 * day), ago(1 * day)}, nil},
		{"exactly 90 days apart is outside", []time.Time{ago(91 * day), ago(50 * day), ago(1 * day)}, nil},
		{"pause already over", []time.Time{ago(30 * day), ago(20 * day), ago(8 * day)}, nil},
		{"pause still running from the third", []time.Time{ago(30 * day), ago(20 * day), ago(6 * day)}, ptr(ago(6 * day).Add(7 * day))},
		{"a fourth extends it", []time.Time{ago(60 * day), ago(30 * day), ago(10 * day), ago(2 * day)}, ptr(ago(2 * day).Add(7 * day))},
		{"an old third and a later pair", []time.Time{ago(96 * day), ago(94 * day), ago(93 * day), ago(3 * day)}, nil},
		// A later block never counts back toward an earlier one: -91.5d is in
		// the window of the -2d block only if the -1d block counted for it.
		{"later blocks do not count back", []time.Time{ago(91*day + 12*time.Hour), ago(2 * day), ago(1 * day)}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := LongVideoPause(now, c.blocks)
			if (got == nil) != (c.want == nil) || (got != nil && !got.Equal(*c.want)) {
				t.Fatalf("LongVideoPause = %v, want %v", got, c.want)
			}
		})
	}
}

// The pause rides beside the standing: an otherwise clean user stays "ok"
// (reels and posts publish), with long_video_paused_until set.
func TestEvaluateStanding_PauseDoesNotSuspend(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	snap := &postgres.StandingSnapshot{
		ActiveStrikes:   []postgres.UserStrike{},
		CopyrightBlocks: []time.Time{now.Add(-20 * 24 * time.Hour), now.Add(-10 * 24 * time.Hour), now.Add(-time.Hour)},
	}
	st, err := EvaluateStanding(uuid.New(), now, snap)
	if err != nil {
		t.Fatal(err)
	}
	if st.Standing != StandingOK || st.SuspendedUntil != nil {
		t.Fatalf("standing = %s until %v; the pause must not suspend", st.Standing, st.SuspendedUntil)
	}
	if st.LongVideoPausedUntil == nil || !st.LongVideoPausedUntil.Equal(now.Add(-time.Hour).Add(LongVideoPauseDuration)) {
		t.Fatalf("paused until %v", st.LongVideoPausedUntil)
	}
	snap.CopyrightBlocks = snap.CopyrightBlocks[1:]
	if st, _ := EvaluateStanding(uuid.New(), now, snap); st.LongVideoPausedUntil != nil {
		t.Fatalf("two blocks paused uploads until %v", st.LongVideoPausedUntil)
	}
}
