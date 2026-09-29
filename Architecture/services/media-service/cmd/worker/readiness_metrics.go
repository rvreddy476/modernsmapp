package main

import (
	"context"
	"log"
	"time"

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
	oldestPendingAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_transcode_oldest_pending_age_seconds",
		Help: "Age of the oldest asset still at uploaded/processing (OldestPendingTranscodeAge).",
	})
	oldestQueuedWait = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_transcode_queue_oldest_waiting_seconds",
		Help: "Age of the oldest video queued for a worker and not yet started.",
	})
)

// readinessProbe is the store surface the gauges poll.
type readinessProbe interface {
	OldestPendingTranscodeAge(ctx context.Context) (float64, error)
	OldestQueuedTranscodeWait(ctx context.Context) (float64, error)
}

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

func refreshReadinessGauges(ctx context.Context, store readinessProbe) {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if age, err := store.OldestPendingTranscodeAge(pctx); err != nil {
		if ctx.Err() == nil {
			log.Printf("Warning: oldest pending transcode age: %v", err)
		}
	} else {
		oldestPendingAge.Set(age)
	}
	if wait, err := store.OldestQueuedTranscodeWait(pctx); err != nil {
		if ctx.Err() == nil {
			log.Printf("Warning: oldest queued transcode wait: %v", err)
		}
	} else {
		oldestQueuedWait.Set(wait)
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
