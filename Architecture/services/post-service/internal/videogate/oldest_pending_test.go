package videogate

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// lv-21: post_video_gate_oldest_pending_seconds is the age of the oldest
// post a full pass left pending, set at the end of the pass, and 0 once
// nothing is left pending.

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetGauge().GetValue()
}

func TestSweepPublishesOldestPendingAtTheEndOfAPass(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	// Oldest first: one that resolves (approved), then two that stay pending,
	// then one more that resolves. Batch 2: the pass takes three ticks.
	st.add(now.Add(-5*time.Hour), "ready", "passed")
	st.add(now.Add(-3*time.Hour), "processing", "pending")
	st.add(now.Add(-2*time.Hour), "processing", "pending")
	st.add(now.Add(-time.Hour), "ready", "passed")
	w := NewSweeper(st, Config{MinAge: 5 * time.Minute, Batch: 2}, nil).WithClock(func() time.Time { return now })

	oldestPendingGauge.Set(-1) // a sentinel the first, unfinished tick must not touch
	w.Tick(context.Background())
	if got := gaugeValue(t, oldestPendingGauge); got != -1 {
		t.Fatalf("the gauge moved before the pass ended: %v", got)
	}
	w.Tick(context.Background()) // the two left pending + the one resolved: still a full batch
	w.Tick(context.Background()) // short: the pass ends
	if got, want := gaugeValue(t, oldestPendingGauge), (3 * time.Hour).Seconds(); got != want {
		t.Fatalf("oldest pending = %v s, want %v (the 3 h post; the 5 h one was approved)", got, want)
	}

	// The two pending posts finish; the next full pass leaves nothing pending.
	for id := range st.status {
		if st.status[id] == "pending" {
			for i := range st.rows {
				if st.rows[i].PostID == id {
					st.rows[i].ProcessingStatus, st.rows[i].ModerationStatus = "ready", "passed"
				}
			}
		}
	}
	w.Tick(context.Background()) // both approved; a full batch, the pass goes on
	w.Tick(context.Background()) // short: the pass ends with nothing left pending
	if got := gaugeValue(t, oldestPendingGauge); got != 0 {
		t.Fatalf("nothing is pending, gauge = %v, want 0", got)
	}
}
