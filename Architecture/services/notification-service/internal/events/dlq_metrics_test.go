package events

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/segmentio/kafka-go"
)

// lv-21: a message the call consumer dead-letters is counted once, under its
// source topic, only after the DLQ write succeeded.
func TestCallConsumerDeadLetterIsCounted(t *testing.T) {
	const topic = "call.notifications.metrics-test"
	read := func() float64 {
		var m dto.Metric
		if err := dlqMessages.WithLabelValues(topic).Write(&m); err != nil {
			t.Fatal(err)
		}
		return m.GetCounter().GetValue()
	}
	before := read()
	quarantine := &recordingQuarantine{failFirst: 2}
	c := newCallConsumerForTest(&scriptedSource{}, quarantine, &flakyNotifier{}, time.Millisecond)
	if !c.handleUntilDurable(context.Background(), kafka.Message{Topic: topic, Offset: 9, Value: []byte(`{not json`)}) {
		t.Fatal("the poison message never reached a durable outcome")
	}
	if got := read() - before; got != 1 {
		t.Fatalf("atpost_notification_service_dlq_messages_total{%s} moved by %v, want 1 (two failed writes, one success)", topic, got)
	}
}
