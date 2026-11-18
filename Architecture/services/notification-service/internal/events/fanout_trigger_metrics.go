package events

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// lv-21 (18 Nov 2026): the two upload fan-out triggers (PostCreated and the
// "post became visible" PostSearchEligibilityChanged), counted by what the
// consumer did with each event. ignored_* is a normal answer (not an upload,
// not public yet, no channel); dropped_* lost an event that might have meant
// a notification; enqueue_error is returned to the consumer, which retries.

const (
	fanoutTriggerPostCreated = "post_created"
	fanoutTriggerPostVisible = "post_visible"
)

const (
	triggerEnqueued          = "enqueued"
	triggerEnqueueError      = "enqueue_error"
	triggerIgnoredNotUpload  = "ignored_not_upload"
	triggerIgnoredAudience   = "ignored_audience"
	triggerIgnoredOptedOut   = "ignored_opted_out"
	triggerIgnoredState      = "ignored_state"
	triggerIgnoredTooOld     = "ignored_too_old"
	triggerIgnoredNoChannel  = "ignored_no_channel"
	triggerDroppedMalformed  = "dropped_malformed"
	triggerDroppedNoPipeline = "dropped_no_pipeline"
)

var fanoutTriggerEvents = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "atpost", Subsystem: "notification_service",
	Name: "fanout_trigger_events_total",
	Help: "Upload fan-out trigger events received, by trigger (post_created, post_visible) and outcome (enqueued, enqueue_error, ignored_*, dropped_*).",
}, []string{"trigger", "outcome"})

var fanoutTriggerOutcomes = []string{
	triggerEnqueued, triggerEnqueueError, triggerIgnoredNotUpload, triggerIgnoredAudience,
	triggerIgnoredOptedOut, triggerIgnoredState, triggerIgnoredTooOld, triggerIgnoredNoChannel,
	triggerDroppedMalformed, triggerDroppedNoPipeline,
}

func countFanoutTrigger(trigger, outcome string) {
	fanoutTriggerEvents.WithLabelValues(trigger, outcome).Inc()
}

func enqueueOutcome(err error) (string, error) {
	if err != nil {
		return triggerEnqueueError, err
	}
	return triggerEnqueued, nil
}

// precreateFanoutTriggerMetrics creates every label value at zero.
func precreateFanoutTriggerMetrics() {
	for _, t := range []string{fanoutTriggerPostCreated, fanoutTriggerPostVisible} {
		for _, o := range fanoutTriggerOutcomes {
			fanoutTriggerEvents.WithLabelValues(t, o)
		}
	}
}

func init() { precreateFanoutTriggerMetrics() }
