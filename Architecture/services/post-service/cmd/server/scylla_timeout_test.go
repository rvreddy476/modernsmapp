package main

import (
	"testing"
	"time"
)

// The Scylla per-query timeout is bounded by default and configurable
// (2026-10-30 audit B6 leftover): gocql's own default is 11 s.
func TestDurationEnv_ScyllaTimeout(t *testing.T) {
	if defaultScyllaTimeout <= 0 || defaultScyllaTimeout > 5*time.Second {
		t.Fatalf("defaultScyllaTimeout = %v, want a bound well under gocql's 11 s", defaultScyllaTimeout)
	}
	for _, c := range []struct {
		value string
		want  time.Duration
	}{
		{"", defaultScyllaTimeout},
		{"750ms", 750 * time.Millisecond},
		{"not-a-duration", defaultScyllaTimeout},
		{"0s", defaultScyllaTimeout},
		{"-1s", defaultScyllaTimeout},
	} {
		t.Setenv("POST_SCYLLA_TIMEOUT", c.value)
		if got := durationEnv("POST_SCYLLA_TIMEOUT", defaultScyllaTimeout); got != c.want {
			t.Errorf("POST_SCYLLA_TIMEOUT=%q: %v, want %v", c.value, got, c.want)
		}
	}
}
