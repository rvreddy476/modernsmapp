package events

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Module 2 M2-P0-2 — alerting surface for search-eligibility propagation.
//
// The approval sets a measurable objective rather than "it should be
// fast": p95 propagation from the Postgres commit to public-index
// visibility must be ≤ 30s, and anything still unapplied after 5 minutes
// must page rather than sit in a log nobody reads.
//
// EligibilityApplySeconds gives the p95. StaleApplies and DLQParked are
// the two hard-alert signals — both are counters, so an alert rule is
// simply "increase() > 0".

const (
	// eligibilitySLO is the p95 objective for propagation.
	eligibilitySLO = 30 * time.Second
	// eligibilityHardAlert is the age past which an applied transition is
	// counted as a breach worth paging on.
	eligibilityHardAlert = 5 * time.Minute
)

var (
	// EligibilityApplySeconds measures ChangedAt → applied-to-index. The
	// buckets straddle the 30s SLO and the 5m hard alert so both can be
	// read off a single histogram.
	EligibilityApplySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "atpost", Subsystem: "search",
			Name:    "eligibility_apply_seconds",
			Help:    "Seconds from the eligibility transition commit to the public index reflecting it.",
			Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		},
		// direction: "index" (became searchable) or "remove" (left the index).
		[]string{"direction"},
	)

	// EligibilityStaleApplies counts transitions applied only after the
	// hard-alert threshold. A removal counted here means unsafe or private
	// content was publicly searchable for over five minutes.
	EligibilityStaleApplies = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atpost", Subsystem: "search",
			Name: "eligibility_stale_applies_total",
			Help: "Eligibility transitions applied after exceeding the hard-alert age.",
		},
		[]string{"direction"},
	)

	// IndexOpFailures counts terminal failures of an index/delete op after
	// the in-process retry budget is exhausted.
	IndexOpFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atpost", Subsystem: "search",
			Name: "index_op_failures_total",
			Help: "Index or delete operations that failed after in-process retries.",
		},
		[]string{"event_type"},
	)

	// DLQReplays counts automated dead-letter replay outcomes.
	DLQReplays = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atpost", Subsystem: "search",
			Name: "dlq_replays_total",
			Help: "Automated dead-letter replay attempts by outcome.",
		},
		[]string{"result"}, // recovered | requeued | parked
	)

	// DLQParked counts operations that exhausted every automated recovery
	// path. This is the page-a-human signal: the index is now known to
	// disagree with Postgres and only the reconciler can repair it.
	DLQParked = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atpost", Subsystem: "search",
			Name: "dlq_parked_total",
			Help: "Messages that exhausted automated replay and require operator action.",
		},
		[]string{"event_type"},
	)
)

// observeEligibilityApply records propagation latency for one applied
// transition. changedAt is the Postgres commit time carried on the event;
// a zero value means the producer did not stamp it and is not measurable.
func observeEligibilityApply(changedAt time.Time, direction string) {
	if changedAt.IsZero() {
		return
	}
	age := time.Since(changedAt)
	if age < 0 {
		age = 0 // clock skew between producer and consumer hosts
	}
	EligibilityApplySeconds.WithLabelValues(direction).Observe(age.Seconds())
	if age > eligibilityHardAlert {
		EligibilityStaleApplies.WithLabelValues(direction).Inc()
	}
}
