package events

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// dlqMessages (lv-21, 18 Nov 2026): messages notification-service wrote to a
// dead-letter topic (<topic>.dlq), by source topic. Today only the call
// consumer dead-letters (call_consumer.go); the alert in
// deploy/services/notification-service/values-{prod,qa}.yaml reads it, and
// cmd/dlq-replay in post-service lists and replays the topic.
var dlqMessages = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "atpost", Subsystem: "notification_service",
	Name: "dlq_messages_total",
	Help: "Messages written to a dead-letter topic, by source topic.",
}, []string{"topic"})
