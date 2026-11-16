package kafka

import (
	"context"
	"log/slog"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Test support for packages that own a consumer.
//
// A service decides two things the retry loop then acts on: its
// ConsumerConfig (RetryForever, backoff) and which handler errors it marks
// Permanent. Neither can be proven from the service's own tests without
// running the loop, and the loop needs a broker. NewTestConsumer builds the
// same Consumer with the broker-facing parts replaced, so a service test can
// feed it raw messages and observe what was committed and what was
// dead-lettered. Nothing here is used by NewConsumer or Start.

// TestSeams replaces the broker-facing parts of a Consumer in tests.
type TestSeams struct {
	// Commit receives every offset commit. Nil: commits succeed silently.
	Commit func(ctx context.Context, msg kafkago.Message) error
	// DLQ receives every dead-letter write, with the x-dlq-* headers already
	// added. Nil: no DLQ is configured, as with an empty DLQTopic.
	DLQ func(ctx context.Context, msg kafkago.Message) error
	// Dedupe keeps processing receipts in memory, standing in for Redis.
	// False: no receipts, as with a nil Redis client.
	Dedupe bool
}

// NewTestConsumer builds a Consumer that has no reader, writer or Redis
// client. Feed it with ProcessForTest; do not call Start.
func NewTestConsumer(cfg ConsumerConfig, handler HandlerFunc, seams TestSeams) *Consumer {
	cfg.applyDefaults()
	c := &Consumer{cfg: cfg, handler: handler, dlqWrite: seams.DLQ}
	c.commit = seams.Commit
	if c.commit == nil {
		c.commit = func(context.Context, kafkago.Message) error { return nil }
	}
	if seams.Dedupe {
		c.dedupe = &memoryDedupe{keys: map[string]struct{}{}}
	}
	return c
}

// ProcessForTest runs one raw message through the path Start runs for every
// fetched message: decode, dedupe check, handler with retries, dead-letter
// write, receipt, commit. It returns when the message reached a terminal
// state or ctx was cancelled.
func (c *Consumer) ProcessForTest(ctx context.Context, msg kafkago.Message) {
	c.processWithRetry(ctx, slog.Default(), msg)
}

type memoryDedupe struct {
	mu   sync.Mutex
	keys map[string]struct{}
}

func (d *memoryDedupe) Seen(_ context.Context, key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.keys[key]
	return ok
}

func (d *memoryDedupe) Mark(_ context.Context, key string, _ time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keys[key] = struct{}{}
}
