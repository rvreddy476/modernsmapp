package accessauth

import (
	"testing"
	"time"
)

func TestFreshReauthAcceptsAStepUpOrASignInWithinFiveMinutes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	n := now.Unix()
	cases := []struct {
		id   Identity
		want bool
	}{
		{Identity{}, false},
		{Identity{AuthTime: n - 3600}, false},
		{Identity{StepUpAt: n - 301}, false},
		{Identity{StepUpAt: n + 61}, false}, // beyond the clock-skew allowance
		{Identity{StepUpAt: n - 300}, true},
		{Identity{AuthTime: n - 10}, true},
		{Identity{AuthTime: n - 3600, StepUpAt: n - 5}, true},
	}
	for i, c := range cases {
		if got := c.id.FreshReauth(now); got != c.want {
			t.Errorf("case %d %+v: got %v want %v", i, c.id, got, c.want)
		}
	}
}
