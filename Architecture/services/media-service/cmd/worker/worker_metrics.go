package main

import (
	"errors"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Counters the alert rules read (2026-11-17). Every label value an alert
// selects is created at zero when the worker starts (precreateWorkerMetrics),
// so increase() sees the first event after a restart instead of a series
// that begins at 1.

// Quarantine reasons.
const (
	quarantinePermanent         = "permanent"          // unprocessable input
	quarantineAttemptsExhausted = "attempts_exhausted" // in-process retry cap
	quarantineLostWorker        = "lost_worker"        // lv-6: crash/OOM/eviction cap
)

// Delivery start kinds.
const (
	startFresh           = "fresh"             // no earlier run of this request was lost
	startAfterLostWorker = "after_lost_worker" // an earlier run died without returning
)

var (
	transcodeQuarantined = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_transcode_quarantined_total",
		Help: "Transcode requests given up on and recorded terminal, by class and reason.",
	}, []string{"class", "reason"})
	transcodeStarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_transcode_starts_total",
		Help: "Transcode runs started, by class and whether an earlier run of the same request lost its worker (crash, OOM kill or eviction).",
	}, []string{"class", "kind"})
)

// quarantineReason classifies a message the retry loop quarantined. The
// lost-worker give-up is counted where it is decided (processTranscodeLocked):
// it records a terminal failure rather than quarantining the message.
func quarantineReason(err error) string {
	if errors.Is(err, errAttemptsExhausted) {
		return quarantineAttemptsExhausted
	}
	return quarantinePermanent
}

func labelOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// precreateWorkerMetrics creates every alerted label value at zero.
func precreateWorkerMetrics() {
	for _, c := range []transcodeClass{classShort, classLong} {
		for _, r := range []string{quarantinePermanent, quarantineAttemptsExhausted, quarantineLostWorker} {
			transcodeQuarantined.WithLabelValues(string(c), r)
		}
		for _, k := range []string{startFresh, startAfterLostWorker} {
			transcodeStarts.WithLabelValues(string(c), k)
		}
	}
	for _, a := range []postgres.StallAction{postgres.StallRequeue, postgres.StallGiveUp, postgres.StallAbandonReprocess} {
		stallReclaims.WithLabelValues(string(a))
	}
	transcodeRouted.WithLabelValues(string(classShort), "local")
	for _, o := range []string{"forwarded", "forward_failed"} {
		transcodeRouted.WithLabelValues(string(classLong), o)
	}
	for _, r := range processing.RungPlan(&processing.VideoMeta{Height: 1 << 20}, false) {
		rungFailures.WithLabelValues(r.Name)
	}
	for _, o := range []string{"encoded", "reused"} {
		rungOutcomes.WithLabelValues(o)
	}
}
