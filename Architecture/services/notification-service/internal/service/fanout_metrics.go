package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// lv-21 (18 Nov 2026): the subscriber fan-out's own signals. Before these, a
// stuck or failing fan-out wrote log lines only. The alert rules in
// deploy/services/notification-service/values-{prod,qa}.yaml read them.
//
// Every label is bounded: kind is upload|live, outcomes and reasons are the
// fixed sets below (an unknown reason is "other"). Every value an alert
// selects is created at zero when the worker starts (precreateFanoutMetrics),
// so increase() sees the first event after a restart.

// Job kinds.
const (
	fanoutKindUpload = "upload"
	fanoutKindLive   = "live"
)

// How one pass over a job ended (settle, drainOnce).
const (
	fanoutSettledCompleted = "completed" // every recipient decided
	fanoutSettledSkipped   = "skipped"   // ends without telling anyone (deleted, opted out, policy)
	fanoutSettledDeferred  = "deferred"  // post not visible yet; waits on the backoff curve
	fanoutSettledExpired   = "expired"   // never became visible within the wait ceiling
	fanoutSettledRetried   = "retried"   // transient error; released with backoff
	fanoutSettledFailed    = "failed"    // attempts exhausted; replay sweep or a trigger may reopen it
)

// What happened to one recipient (deliverPage).
const (
	fanoutRecipientNotified   = "notified"
	fanoutRecipientSelf       = "skipped_self"
	fanoutRecipientAlready    = "skipped_already_told"
	fanoutRecipientIneligible = "skipped_ineligible"
	fanoutRecipientFailed     = "failed"
)

// Job-level reasons. Skip reasons and wait reasons come from classifyUpload
// and confirmGone; the review_* and visibility_* families collapse to one
// value each so a new review status cannot add a series.
var (
	fanoutSkipReasons = []string{"deleted", "creator_opted_out", "retired_audience", "members_only", "age_restricted", "other"}
	fanoutWaitReasons = []string{"review", "restricted", "processing", "scheduled", "visibility", "not_readable", "other"}
)

const (
	fanoutReasonNone              = "none"
	fanoutReasonError             = "error"
	fanoutReasonAttemptsExhausted = "attempts_exhausted"
)

// Queue states of the gauges.
const (
	fanoutQueueDue     = "due"
	fanoutQueueWaiting = "waiting"
	fanoutQueueBackoff = "backoff"
	fanoutQueueRunning = "running"
)

var (
	fanoutJobsEnqueued = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_jobs_enqueued_total",
		Help: "Fan-out enqueues (a new job, or a repeat trigger that wakes or reopens the one row per post), by kind and result (ok, error).",
	}, []string{"kind", "result"})
	fanoutJobsSettled = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_jobs_settled_total",
		Help: "Passes over a fan-out job by how they ended (completed, skipped, deferred, expired, retried, failed) and a bounded reason.",
	}, []string{"kind", "outcome", "reason"})
	fanoutRecipients = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_recipients_total",
		Help: "Fan-out recipients by outcome (notified, skipped_self, skipped_already_told, skipped_ineligible, failed). A failed recipient is retried with its page.",
	}, []string{"kind", "outcome"})
	fanoutReplayedJobs = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_replayed_jobs_total",
		Help: "Failed upload fan-out jobs the hourly replay sweep put back to pending.",
	})
	fanoutReplayErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_replay_errors_total",
		Help: "Replay sweeps that failed to run (database error).",
	})
	fanoutQueueJobs = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_queue_jobs",
		Help: "Unfinished fan-out jobs by state (due, waiting, backoff, running), sampled every 30 s by every replica (same value: use max).",
	}, []string{"state"})
	fanoutQueueOldest = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_queue_oldest_seconds",
		Help: "Age of the oldest unfinished fan-out job by state: due (since it became due, unclaimed), waiting (since enqueue, post not visible yet), running (since its last progress). 0 when none.",
	}, []string{"state"})
	fanoutQueueReadErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "notification_service",
		Name: "fanout_queue_read_errors_total",
		Help: "Failed reads of the fan-out queue gauges; the gauges keep their last value.",
	})
)

func fanoutKind(notifType string) string {
	if notifType == LiveNotifType {
		return fanoutKindLive
	}
	return fanoutKindUpload
}

// fanoutSkipReason and fanoutWaitReason map a gate reason onto its bounded
// label value.
func fanoutSkipReason(r string) string {
	for _, k := range fanoutSkipReasons {
		if r == k {
			return k
		}
	}
	return "other"
}

func fanoutWaitReason(r string) string {
	switch {
	case strings.HasPrefix(r, "review_"):
		return "review"
	case strings.HasPrefix(r, "visibility_"):
		return "visibility"
	}
	for _, k := range fanoutWaitReasons {
		if r == k {
			return k
		}
	}
	return "other"
}

func countFanoutSettled(notifType, outcome, reason string) {
	fanoutJobsSettled.WithLabelValues(fanoutKind(notifType), outcome, reason).Inc()
}

func countFanoutRecipient(notifType, outcome string) {
	fanoutRecipients.WithLabelValues(fanoutKind(notifType), outcome).Inc()
}

// precreateFanoutMetrics creates every label value at zero.
func precreateFanoutMetrics() {
	for _, k := range []string{fanoutKindUpload, fanoutKindLive} {
		for _, r := range []string{"ok", "error"} {
			fanoutJobsEnqueued.WithLabelValues(k, r)
		}
		fanoutJobsSettled.WithLabelValues(k, fanoutSettledCompleted, fanoutReasonNone)
		fanoutJobsSettled.WithLabelValues(k, fanoutSettledRetried, fanoutReasonError)
		fanoutJobsSettled.WithLabelValues(k, fanoutSettledFailed, fanoutReasonAttemptsExhausted)
		for _, r := range fanoutSkipReasons {
			fanoutJobsSettled.WithLabelValues(k, fanoutSettledSkipped, r)
		}
		for _, r := range fanoutWaitReasons {
			fanoutJobsSettled.WithLabelValues(k, fanoutSettledDeferred, r)
			fanoutJobsSettled.WithLabelValues(k, fanoutSettledExpired, r)
		}
		for _, o := range []string{fanoutRecipientNotified, fanoutRecipientSelf, fanoutRecipientAlready, fanoutRecipientIneligible, fanoutRecipientFailed} {
			fanoutRecipients.WithLabelValues(k, o)
		}
	}
	for _, s := range []string{fanoutQueueDue, fanoutQueueWaiting, fanoutQueueBackoff, fanoutQueueRunning} {
		fanoutQueueJobs.WithLabelValues(s)
	}
	for _, s := range []string{fanoutQueueDue, fanoutQueueWaiting, fanoutQueueRunning} {
		fanoutQueueOldest.WithLabelValues(s)
	}
}

// fanoutStatsReader is the optional store method behind the queue gauges;
// *postgres.Store has it. Kept out of fanoutStore so the pipeline's fakes
// need not implement it.
type fanoutStatsReader interface {
	FanoutQueueStats(ctx context.Context) (postgres.FanoutQueueStats, error)
}

const fanoutGaugeEvery = 30 * time.Second

// sampleFanoutQueue sets the queue gauges from one read of the job table.
// A failed read keeps the last values and is counted.
func (f *SubscriberFanout) sampleFanoutQueue(ctx context.Context) {
	r, ok := f.pg.(fanoutStatsReader)
	if !ok {
		return
	}
	st, err := r.FanoutQueueStats(ctx)
	if err != nil {
		fanoutQueueReadErrors.Inc()
		slog.Warn("fanout: queue gauge read failed", "error", err)
		return
	}
	fanoutQueueJobs.WithLabelValues(fanoutQueueDue).Set(float64(st.Due))
	fanoutQueueJobs.WithLabelValues(fanoutQueueWaiting).Set(float64(st.Waiting))
	fanoutQueueJobs.WithLabelValues(fanoutQueueBackoff).Set(float64(st.Backoff))
	fanoutQueueJobs.WithLabelValues(fanoutQueueRunning).Set(float64(st.Running))
	fanoutQueueOldest.WithLabelValues(fanoutQueueDue).Set(st.OldestDue.Seconds())
	fanoutQueueOldest.WithLabelValues(fanoutQueueWaiting).Set(st.OldestWaiting.Seconds())
	fanoutQueueOldest.WithLabelValues(fanoutQueueRunning).Set(st.OldestRunning.Seconds())
}
