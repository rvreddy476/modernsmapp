package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// The retry-age gauge is what the FeedEventRetryStuck alert reads: it must
// grow while an event keeps failing and return to 0 once the event is done,
// or the alert would never clear.
func TestRetryAgeGrowsWhileFailingAndClearsOnSuccess(t *testing.T) {
	m := metrics.NewKafkaConsumerMetrics("retry-age-test")
	gauge := m.RetryAge
	read := func() float64 { return gaugeValue(t, gauge) }

	var seen []float64
	attempts := 0
	c := NewTestConsumer(ConsumerConfig{
		GroupID: "g", Topic: "test.topic", RetryForever: true, RetryBackoff: 5 * time.Millisecond,
	}, func(context.Context, *events.EventEnvelope) error {
		seen = append(seen, read()) // the value left by the previous attempt
		attempts++
		if attempts <= 3 {
			return errors.New("dependency down")
		}
		return nil
	}, TestSeams{})
	c.metrics = m

	c.ProcessForTest(context.Background(), envelopeMsg(t, "evt-retry-age"))

	if attempts != 4 {
		t.Fatalf("handler ran %d times, want 4", attempts)
	}
	if seen[0] != 0 {
		t.Fatalf("gauge was %v before the first failure, want 0", seen[0])
	}
	if !(seen[3] > seen[2] && seen[2] > 0) {
		t.Fatalf("gauge did not grow across failed attempts: %v", seen)
	}
	if got := read(); got != 0 {
		t.Fatalf("gauge = %v after the event succeeded, want 0", got)
	}
}

// Shutdown in the middle of a retry also clears it: the pod that stops is
// no longer the one retrying.
func TestRetryAgeClearsOnShutdown(t *testing.T) {
	m := metrics.NewKafkaConsumerMetrics("retry-age-shutdown-test")
	gauge := m.RetryAge
	ctx, cancel := context.WithCancel(context.Background())
	c := NewTestConsumer(ConsumerConfig{
		GroupID: "g", Topic: "test.topic", RetryForever: true, RetryBackoff: time.Hour,
	}, func(context.Context, *events.EventEnvelope) error {
		cancel()
		return errors.New("dependency down")
	}, TestSeams{})
	c.metrics = m

	c.ProcessForTest(ctx, envelopeMsg(t, "evt-retry-age-shutdown"))

	if got := gaugeValue(t, gauge); got != 0 {
		t.Fatalf("gauge = %v after shutdown, want 0", got)
	}
}

func gaugeValue(t *testing.T, gauge *prometheus.GaugeVec) float64 {
	t.Helper()
	var out dto.Metric
	if err := gauge.WithLabelValues("test.topic", "g").Write(&out); err != nil {
		t.Fatalf("read gauge: %v", err)
	}
	return out.GetGauge().GetValue()
}
