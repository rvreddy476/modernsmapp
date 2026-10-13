package events

import (
	"context"
	"github.com/segmentio/kafka-go"
	"log/slog"
	"time"
)

// Do not fetch/commit a later offset while a Partner queue insert is failing.
// Once persisted, provider retries happen independently and cannot stall Kafka.
func processAndCommit(ctx context.Context, r interface {
	CommitMessages(context.Context, ...kafka.Message) error
}, m kafka.Message, process func(context.Context, kafka.Message) error) bool {
	for ctx.Err() == nil {
		if err := process(ctx, m); err != nil {
			slog.Warn("notification queue persistence pending", "topic", m.Topic, "partition", m.Partition, "offset", m.Offset)
			if !waitForQueueRetry(ctx) {
				return false
			}
			continue
		}
		for ctx.Err() == nil {
			if err := r.CommitMessages(ctx, m); err == nil {
				return true
			}
			if !waitForQueueRetry(ctx) {
				return false
			}
		}
	}
	return false
}
func waitForQueueRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
