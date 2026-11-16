package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	kafkago "github.com/segmentio/kafka-go"
)

// The DLQ write became a seam (dlqWrite) so consumer packages can test what
// they dead-letter. These pin that the loop around it is what it was.

// A consumer that does not ask for RetryForever still gives up after
// MaxRetries and dead-letters the event once, then commits.
func TestBoundedRetriesStillEndInOneDeadLetter(t *testing.T) {
	var order []string
	attempts := 0
	c := NewTestConsumer(ConsumerConfig{
		GroupID: "g", Topic: "test.topic", MaxRetries: 2, RetryBackoff: time.Millisecond,
	}, func(context.Context, *events.EventEnvelope) error {
		attempts++
		return errors.New("still down")
	}, TestSeams{
		Dedupe: true,
		Commit: func(context.Context, kafkago.Message) error { order = append(order, "commit"); return nil },
		DLQ: func(_ context.Context, m kafkago.Message) error {
			order = append(order, "dlq")
			for _, h := range m.Headers {
				if h.Key == "x-dlq-error" && string(h.Value) == "still down" {
					return nil
				}
			}
			t.Errorf("dead letter lacks the x-dlq-error header: %v", m.Headers)
			return nil
		},
	})

	msg := envelopeMsg(t, "evt-bounded")
	c.ProcessForTest(context.Background(), msg)

	if attempts != 3 {
		t.Fatalf("handler ran %d times, want 3 (one try and MaxRetries=2 retries)", attempts)
	}
	if len(order) != 2 || order[0] != "dlq" || order[1] != "commit" {
		t.Fatalf("order = %v, want [dlq commit]", order)
	}
	// The dead-lettered event holds a receipt: a redelivery is skipped.
	c.ProcessForTest(context.Background(), msg)
	if attempts != 3 {
		t.Fatalf("a dead-lettered event was handled again on redelivery (%d runs)", attempts)
	}
}

// NewTestConsumer applies the defaults NewConsumer applies.
func TestTestConsumerHasTheProductionDefaults(t *testing.T) {
	c := NewTestConsumer(ConsumerConfig{}, nil, TestSeams{})
	if c.cfg.MaxRetries != 3 || c.cfg.RetryBackoff != time.Second || c.cfg.DedupTTL != 24*time.Hour {
		t.Fatalf("defaults = %d / %s / %s, want 3 / 1s / 24h", c.cfg.MaxRetries, c.cfg.RetryBackoff, c.cfg.DedupTTL)
	}
	if c.dlqWrite != nil || c.dedupe != nil {
		t.Fatal("no DLQ and no dedupe were asked for, but one is set")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close on a test consumer: %v", err)
	}
}
