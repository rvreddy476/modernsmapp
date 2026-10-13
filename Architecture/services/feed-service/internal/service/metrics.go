package service

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Feed-specific metrics, served on /metrics next to the shared HTTP, DB-pool
// and Kafka-consumer collectors (consumer lag and DLQ counts come from
// shared/kafka). Before these the service exported nothing about its own
// work: a stalled fan-out, a ranker that had quietly fallen back to
// chronological order or a hydration outage showed up only in logs.
//
// Alert rules on them: deploy/services/feed-service/values-*.yaml,
// prometheusRule.
var (
	fanoutDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "atpost", Subsystem: "feed_service",
		Name:    "fanout_duration_seconds",
		Help:    "Wall-clock time of one post fan-out (follower/circle/subscriber resolution plus timeline writes).",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 15), // 10ms .. ~160s
	})
	fanoutWrites = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "feed_service",
		Name: "fanout_timeline_writes_total",
		Help: "Home-timeline rows written by fan-out, by result.",
	}, []string{"result"}) // ok | failed
	fanoutIncomplete = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "feed_service",
		Name: "fanout_incomplete_total",
		Help: "Fan-outs that returned an error (a fetch or write failed) and were handed back for retry.",
	})
	rankFallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "feed_service",
		Name: "rank_fallback_total",
		Help: "Pages served in chronological order because the ranker failed or hit its time budget.",
	}, []string{"surface"})
	hydrationFailures = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "feed_service",
		Name: "hydration_failures_total",
		Help: "HydratePosts calls that failed (the request is answered with an error).",
	})
	homePageItems = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "atpost", Subsystem: "feed_service",
		Name:    "home_page_items",
		Help:    "Items on a served home-feed page before hydration (short pages mean filters are eating the window).",
		Buckets: []float64{0, 1, 5, 10, 15, 20, 30, 50, 100},
	})
)
