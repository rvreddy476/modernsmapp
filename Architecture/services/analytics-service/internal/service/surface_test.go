package service

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// counterValue reads a counter without prometheus/testutil, which the
// workspace does not vendor.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

// Plan 5D (issue M-22): reels is a surface of its own, not the home feed.
func TestNormalizeSurfaceAcceptsReels(t *testing.T) {
	for raw, want := range map[string]string{
		"reels":    "reels",
		" Reels ":  "reels",
		"feed":     "feed",
		"posttube": "posttube",
		"profile":  "profile",
		"search":   "search",
		"channel":  "channel",
	} {
		if got := normalizeSurface(raw); got != want {
			t.Fatalf("normalizeSurface(%q)=%q want %q", raw, got, want)
		}
	}
}

// A surface string the normaliser does not know still collapses to
// "other" — the request is not failed — but the collapse is counted, so
// a client sending the wrong string is visible on /metrics instead of
// silently filling the "other" bucket (99.8% of rows at audit time).
func TestSurfaceRejectionIsCounted(t *testing.T) {
	before := counterValue(t, surfaceRejected.WithLabelValues("flicks"))
	if got := normalizeSurface("Flicks"); got != "other" {
		t.Fatalf("normalizeSurface(Flicks)=%q want other", got)
	}
	if got := normalizeSurface("flicks"); got != "other" {
		t.Fatalf("normalizeSurface(flicks)=%q want other", got)
	}
	after := counterValue(t, surfaceRejected.WithLabelValues("flicks"))
	if after-before != 2 {
		t.Fatalf("analytics_surface_rejected_total{raw=flicks} rose by %v, want 2", after-before)
	}
	// An accepted surface is not a rejection.
	accepted := counterValue(t, surfaceRejected.WithLabelValues("reels"))
	normalizeSurface("reels")
	if counterValue(t, surfaceRejected.WithLabelValues("reels")) != accepted {
		t.Fatal("an accepted surface was counted as rejected")
	}
	// Empty is labelled, not dropped, and a long string is bounded so a
	// client cannot mint unbounded label values.
	emptyBefore := counterValue(t, surfaceRejected.WithLabelValues("(empty)"))
	normalizeSurface("")
	if counterValue(t, surfaceRejected.WithLabelValues("(empty)"))-emptyBefore != 1 {
		t.Fatal("an empty surface was not counted under (empty)")
	}
	if label := surfaceRejectionLabel(strings.Repeat("x", 200)); len(label) > maxSurfaceLabelLen {
		t.Fatalf("rejection label not bounded: %d bytes", len(label))
	}
}
