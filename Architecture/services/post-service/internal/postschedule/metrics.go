package postschedule

import (
	"context"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// lv-21 (18 Nov 2026): the schedule publisher's own signals. Before these a
// stuck schedule wrote log lines only. The alert rules in
// deploy/services/post-service/values-{prod,qa}.yaml read them. Every
// replica runs the worker and reads the same table, so the gauges carry the
// same value on every pod: alert on max, never sum.

// Tick results.
const (
	resultDue         = "due"          // listed as due and publishable this tick
	resultPublished   = "published"    // made live (or live under a title hold)
	resultSkipped     = "skipped"      // not scheduled any more when flipped (raced, parked)
	resultFailed      = "failed"       // publish returned an error; retried next tick
	resultMediaFailed = "media_failed" // REG-6: video failed, taken off the schedule
	resultListFailed  = "list_failed"  // a list query failed; the tick did nothing
)

// Backlog states.
const (
	stateDue       = "due"
	stateHeldVideo = "held_video"
)

var (
	scheduledPosts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "scheduled_posts_total",
		Help: "Scheduled posts the schedule worker handled, by result (due, published, skipped, failed, media_failed, list_failed). A post that keeps failing is counted every tick.",
	}, []string{"result"})
	scheduledPublishLag = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "scheduled_publish_lag_seconds",
		Help: "Seconds between a scheduled post's publish_at and the tick that made it live. A video held until it is ready lands in the high buckets.",
		// 30 s tick, up to two days for a long video that transcoded late.
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400, 172800},
	})
	scheduledOverdue = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "scheduled_overdue_posts",
		Help: "Scheduled posts past publish_at and not live after the last tick: due (publishable; should have gone live) or held_video (video still processing or in review).",
	}, []string{"state"})
	scheduledOldestOverdue = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "scheduled_oldest_overdue_seconds",
		Help: "How far past publish_at the oldest overdue scheduled post is after the last tick, by state (due, held_video); 0 when none.",
	}, []string{"state"})
	scheduledBacklogReadErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "scheduled_backlog_read_errors_total",
		Help: "Failed reads of the scheduled backlog gauges; the gauges keep their last value.",
	})
)

// precreateMetrics creates every alerted label value at zero.
func precreateMetrics() {
	for _, r := range []string{resultDue, resultPublished, resultSkipped, resultFailed, resultMediaFailed, resultListFailed} {
		scheduledPosts.WithLabelValues(r)
	}
	for _, s := range []string{stateDue, stateHeldVideo} {
		scheduledOverdue.WithLabelValues(s)
		scheduledOldestOverdue.WithLabelValues(s)
	}
}

func init() { precreateMetrics() }

// BacklogReader is the optional store method behind the backlog gauges;
// *postgres.Store has it. Kept out of Store so test fakes need not.
type BacklogReader interface {
	ScheduledPostBacklog(ctx context.Context, now time.Time) (postgres.ScheduledBacklog, error)
}

// sampleBacklog sets the backlog gauges after a tick. A failed read keeps
// the last values and is counted.
func (w *Worker) sampleBacklog(ctx context.Context) {
	r, ok := w.store.(BacklogReader)
	if !ok {
		return
	}
	b, err := r.ScheduledPostBacklog(ctx, w.now())
	if err != nil {
		scheduledBacklogReadErrors.Inc()
		w.log.Warn("post schedule: backlog gauge read failed", "err", err)
		return
	}
	scheduledOverdue.WithLabelValues(stateDue).Set(float64(b.Due))
	scheduledOverdue.WithLabelValues(stateHeldVideo).Set(float64(b.HeldVideo))
	scheduledOldestOverdue.WithLabelValues(stateDue).Set(b.OldestDue.Seconds())
	scheduledOldestOverdue.WithLabelValues(stateHeldVideo).Set(b.OldestHeldVideo.Seconds())
}
