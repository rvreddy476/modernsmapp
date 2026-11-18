package service

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// lv-21 (18 Nov 2026): the outbox relay's own signals. PostCreated and the
// "post became visible" event reach notification-service's upload fan-out
// only through this relay, so a relay that cannot publish silently stops
// every subscriber notification. The alert rules in
// deploy/services/post-service/values-{prod,qa}.yaml read these.

// What one pass did with one outbox row.
const (
	outboxPublished     = "published"      // written to Kafka and marked
	outboxPublishFailed = "publish_failed" // Kafka refused it; retried next pass
	outboxMarkFailed    = "mark_failed"    // written but not marked; re-sent next pass (consumers dedupe on the row id)
	outboxBuildFailed   = "build_failed"   // the message cannot be built; it never will be
	outboxHeld          = "held"           // an earlier row of the same aggregate is unsettled
)

var (
	outboxRelayRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "outbox_events_total",
		Help: "Outbox rows the relay handled, by result (published, publish_failed, mark_failed, build_failed, held).",
	}, []string{"result"})
	outboxRelayErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "outbox_relay_errors_total",
		Help: "Relay passes that could not start, by stage (lock, list).",
	}, []string{"stage"})
	outboxOldestUnpublished = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "outbox_oldest_unpublished_seconds",
		Help: "Age of the oldest outbox row the last relay pass on this pod listed and could not publish (rows that cannot be built excluded); 0 when every row went out or another replica holds the relay. Use max across pods.",
	})
)

func init() {
	for _, r := range []string{outboxPublished, outboxPublishFailed, outboxMarkFailed, outboxBuildFailed, outboxHeld} {
		outboxRelayRows.WithLabelValues(r)
	}
	for _, s := range []string{"lock", "list"} {
		outboxRelayErrors.WithLabelValues(s)
	}
}
