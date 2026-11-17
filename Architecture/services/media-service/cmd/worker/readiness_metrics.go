package main

import (
	"context"
	"log"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Readiness observability (Copyright Match plan P-16 / section 13). The
// budget for fingerprinting is "p95 and p99 confirm→ready regress by no
// more than max(5%, 15 s) and queue wait p95 does not rise", measured
// against a 14-day baseline. These are the numbers that baseline is built
// from, exported on the worker's existing :9091/metrics.
//
//   media_upload_ready_latency_seconds   confirm → ready (or failed), per job
//   media_transcode_queue_wait_seconds   request written → worker started
//   media_transcode_oldest_pending_age_seconds
//        OldestPendingTranscodeAge, which existed and was never called:
//        the oldest asset at 'uploaded' or 'processing', by created_at
//   media_transcode_queue_oldest_waiting_seconds
//        the oldest video queued for a worker (published request, no
//        heartbeat since it was queued); what admission reads

var (
	readyLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "media_upload_ready_latency_seconds",
		Help:    "Seconds from upload confirm to a terminal transcode outcome, by outcome.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600, 7200, 14400},
	}, []string{"outcome"})
	queueWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "media_transcode_queue_wait_seconds",
		Help:    "Seconds a transcode request waited before a worker started it.",
		Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
	})
	// Both gauges carry class ("short"/"long") and, since 2026-11-18 (N-1,
	// lv-21; migration 035), each class's OWN oldest age: the short consumer
	// records the class it routed each request into, and anything not yet
	// routed is waiting on the short intake. Every worker reports BOTH
	// classes (the values are the same on every pod; the rules take max), so
	// a long video waiting while every long worker is down still shows.
	oldestPendingAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_transcode_oldest_pending_age_seconds",
		Help: "Age of the oldest asset still at uploaded/processing (OldestPendingTranscodeAge), reported by the worker of each class.",
	}, []string{"class"})
	oldestQueuedWait = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_transcode_queue_oldest_waiting_seconds",
		Help: "Age of the oldest video queued for a worker and not yet started, reported by the worker of each class.",
	}, []string{"class"})
)

// readinessProbe is the store surface the gauges poll.
type readinessProbe interface {
	OldestTranscodeAgesByClass(ctx context.Context) (postgres.TranscodeAgesByClass, error)
}

// gaugeClasses: every worker reports every class (see the gauges above).
var gaugeClasses = []transcodeClass{classShort, classLong}

// runReadinessGauges refreshes the two gauges every interval.
func runReadinessGauges(ctx context.Context, store readinessProbe, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		refreshReadinessGauges(ctx, store)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshReadinessGauges sets each class's gauge to that class's own oldest
// age; a class with nothing waiting is 0. A failed read leaves the gauges.
func refreshReadinessGauges(ctx context.Context, store readinessProbe) {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ages, err := store.OldestTranscodeAgesByClass(pctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Warning: oldest transcode ages by class: %v", err)
		}
		return
	}
	for _, c := range gaugeClasses {
		oldestPendingAge.WithLabelValues(string(c)).Set(ages.Pending[string(c)])
		oldestQueuedWait.WithLabelValues(string(c)).Set(ages.Queued[string(c)])
	}
}

// observeQueueWait records request → start for a job that is starting now.
// A missing request row (a pre-outbox asset) records nothing.
func observeQueueWait(requestedAt *time.Time, startedAt time.Time) {
	if requestedAt == nil {
		return
	}
	if d := startedAt.Sub(*requestedAt); d >= 0 {
		queueWait.Observe(d.Seconds())
	}
}

// observeReadyLatency records confirm → outcome for a job that just
// recorded its terminal state. A missing confirm time (a legacy asset
// with no upload_confirmed_at) records nothing rather than a lie.
func observeReadyLatency(confirmedAt *time.Time, finishedAt time.Time, outcome string) {
	if confirmedAt == nil {
		return
	}
	if d := finishedAt.Sub(*confirmedAt); d >= 0 {
		readyLatency.WithLabelValues(outcome).Observe(d.Seconds())
	}
}
