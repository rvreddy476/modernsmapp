package model

import "testing"

// The one clamp on watch time (M-26): watched <= duration x (loops + 1)
// whenever the duration is known. Heartbeat totals and the play_end
// figure both go through it.
func TestClampWatched(t *testing.T) {
	cases := []struct {
		name        string
		reported    int64
		duration    int64
		loops       int64
		want        int64
		wantClamped bool
	}{
		{"under the ceiling is untouched", 17_000, 5_000, 3, 17_000, false},
		{"exactly the ceiling is untouched", 20_000, 5_000, 3, 20_000, false},
		{"over the ceiling is clamped", 130_000, 5_000, 20, 105_000, true},
		{"a rewatch with no loop is one pass", 40_000, 30_000, 0, 30_000, true},
		{"a heartbeat before play_end has no loops yet", 130_000, 5_000, 0, 5_000, true},
		{"unknown duration passes through", 130_000, 0, 20, 130_000, false},
		{"negative duration passes through", 130_000, -1, 20, 130_000, false},
		{"a negative loop count reads as zero", 12_000, 5_000, -3, 5_000, true},
		{"zero reported stays zero", 0, 5_000, 2, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, clamped := ClampWatched(tc.reported, tc.duration, tc.loops)
			if got != tc.want || clamped != tc.wantClamped {
				t.Fatalf("ClampWatched(%d, %d, %d) = (%d, %v), want (%d, %v)",
					tc.reported, tc.duration, tc.loops, got, clamped, tc.want, tc.wantClamped)
			}
		})
	}
}

// GREATEST-then-clamp must be monotonic: with the ceiling only ever
// growing (duration and loops are GREATEST'd too), a later, larger
// running total can never land below an earlier clamped one.
func TestClampWatchedIsMonotonic(t *testing.T) {
	const duration = 5_000
	prev := int64(0)
	loops := int64(0)
	for reported := int64(0); reported <= 130_000; reported += 5_000 {
		if reported == 130_000 {
			loops = 20 // the play_end arrives with its loop count
		}
		got, _ := ClampWatched(reported, duration, loops)
		if got < prev {
			t.Fatalf("clamped total went down: %d after %d (reported %d, loops %d)", got, prev, reported, loops)
		}
		prev = got
	}
	if prev != 105_000 {
		t.Fatalf("final clamped total = %d, want 105000", prev)
	}
}

// Audit M6: the server-duration clamp. 5% slack, capped at 1s, per pass.
func TestClampToServerDuration(t *testing.T) {
	cases := []struct {
		name                  string
		reported, server, lps int64
		want                  int64
		wantClamped           bool
	}{
		{"a long video gets one second", 7_200_000, 60_000, 0, 61_000, true},
		{"a short clip gets five percent", 200_000, 2_000, 20, 44_100, true},
		{"within the slack is untouched", 60_900, 60_000, 0, 60_900, false},
		{"exactly the slack is untouched", 61_000, 60_000, 0, 61_000, false},
		{"loops multiply the slackened pass", 300_000, 60_000, 2, 183_000, true},
		{"unknown server duration passes through", 130_000, 0, 3, 130_000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, clamped := ClampToServerDuration(tc.reported, tc.server, tc.lps)
			if got != tc.want || clamped != tc.wantClamped {
				t.Fatalf("ClampToServerDuration(%d, %d, %d) = %d, %v; want %d, %v",
					tc.reported, tc.server, tc.lps, got, clamped, tc.want, tc.wantClamped)
			}
		})
	}
	for server, want := range map[int64]int64{0: 0, -5: 0, 2_000: 100, 20_000: 1_000, 19_980: 999, 7_200_000: 1_000} {
		if got := ServerDurationTolerance(server); got != want {
			t.Fatalf("ServerDurationTolerance(%d)=%d want %d", server, got, want)
		}
	}
}
