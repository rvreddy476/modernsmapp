package postschedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// lv-21: the schedule worker counts what each tick did, observes the lag
// from publish_at, and leaves the backlog gauges set after every tick.

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetGauge().GetValue()
}

func lagCount(t *testing.T) (uint64, float64) {
	t.Helper()
	var m dto.Metric
	if err := scheduledPublishLag.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
}

func results(t *testing.T) map[string]float64 {
	out := map[string]float64{}
	for _, r := range []string{resultDue, resultPublished, resultSkipped, resultFailed, resultMediaFailed, resultListFailed} {
		out[r] = counterValue(t, scheduledPosts.WithLabelValues(r))
	}
	return out
}

func delta(t *testing.T, before map[string]float64) map[string]float64 {
	after := results(t)
	out := map[string]float64{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

// backlogStore adds the backlog read to the fake.
type backlogStore struct {
	*fakeStore
	b   postgres.ScheduledBacklog
	err error
}

func (s *backlogStore) ScheduledPostBacklog(context.Context, time.Time) (postgres.ScheduledBacklog, error) {
	return s.b, s.err
}

func TestTickMetrics_CountsDuePublishedAndMediaFailed(t *testing.T) {
	st := newFakeStore()
	now := time.Now()
	st.schedule(now.Add(-2 * time.Minute))
	st.schedule(now.Add(-time.Minute))
	failed := st.schedule(now.Add(-time.Minute))
	st.failMedia(failed)

	before := results(t)
	n, sum := lagCount(t)
	w := NewWorker(st, st, Config{}, nil).WithClock(func() time.Time { return now })
	w.Tick(context.Background())

	got := delta(t, before)
	want := map[string]float64{resultMediaFailed: 1, resultDue: 2, resultPublished: 2}
	if len(got) != len(want) {
		t.Fatalf("scheduled_posts_total moved %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("scheduled_posts_total moved %v, want %v", got, want)
		}
	}
	n2, sum2 := lagCount(t)
	if n2-n != 2 {
		t.Fatalf("lag observed %d times, want 2", n2-n)
	}
	if s := sum2 - sum; s < 179 || s > 181 {
		t.Fatalf("lag sum = %v s, want 180 (120 + 60)", s)
	}
}

func TestTickMetrics_CountsFailuresAndListErrors(t *testing.T) {
	st := newFakeStore()
	st.schedule(time.Now().Add(-time.Minute))
	before := results(t)
	NewWorker(st, failingPublisher{err: errors.New("db down")}, Config{}, nil).Tick(context.Background())
	if got := delta(t, before); got[resultFailed] != 1 || got[resultDue] != 1 || got[resultPublished] != 0 {
		t.Fatalf("a failing publish moved %v", got)
	}

	st.listErr = errors.New("db down")
	before = results(t)
	NewWorker(st, st, Config{}, nil).Tick(context.Background())
	if got := delta(t, before); got[resultListFailed] != 1 || len(got) != 1 {
		t.Fatalf("a failed list moved %v", got)
	}
}

func TestTickMetrics_BacklogGaugesAfterEveryTick(t *testing.T) {
	bs := &backlogStore{fakeStore: newFakeStore(), b: postgres.ScheduledBacklog{
		Due: 3, OldestDue: 15 * time.Minute, HeldVideo: 2, OldestHeldVideo: 4 * time.Hour,
	}}
	// Even a tick whose list fails leaves the gauges current.
	bs.listErr = errors.New("db down")
	NewWorker(bs, bs.fakeStore, Config{}, nil).Tick(context.Background())

	for state, want := range map[string]float64{stateDue: 900, stateHeldVideo: 14400} {
		if got := gaugeValue(t, scheduledOldestOverdue.WithLabelValues(state)); got != want {
			t.Errorf("scheduled_oldest_overdue_seconds{%s} = %v, want %v", state, got, want)
		}
	}
	for state, want := range map[string]float64{stateDue: 3, stateHeldVideo: 2} {
		if got := gaugeValue(t, scheduledOverdue.WithLabelValues(state)); got != want {
			t.Errorf("scheduled_overdue_posts{%s} = %v, want %v", state, got, want)
		}
	}

	errs := counterValue(t, scheduledBacklogReadErrors)
	bs.err = errors.New("db down")
	NewWorker(bs, bs.fakeStore, Config{}, nil).Tick(context.Background())
	if got := counterValue(t, scheduledBacklogReadErrors) - errs; got != 1 {
		t.Fatalf("read errors moved by %v, want 1", got)
	}
	if got := gaugeValue(t, scheduledOldestOverdue.WithLabelValues(stateDue)); got != 900 {
		t.Fatalf("a failed read changed the gauge to %v", got)
	}
}
