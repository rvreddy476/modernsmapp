package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/segmentio/kafka-go"
)

// chatEventMessageRequestReported is chat-service's MessageRequestReported
// (chat-service/shared/events/events.go), emitted when a recipient reports a
// message request. Mirrored here: chat-service is a separate module.
const chatEventMessageRequestReported = "MessageRequestReported"

// chatReportFiler is the slice of the moderation service the consumer uses.
type chatReportFiler interface {
	FileReportWith(ctx context.Context, in service.FileReportInput) (*postgres.Report, error)
}

// ChatReportConsumer turns a reported message request into a trust.reports
// row in the existing admin queue (chat-parity CONTRACT §8). Idempotent: the
// row's source key is the conversation (service.MessageRequestReportSourceKey),
// so a redelivered or re-published event finds the first row.
type ChatReportConsumer struct {
	reader *kafka.Reader
	filer  chatReportFiler
	// retryDelay spaces attempts at a transient failure (tests shorten it).
	retryDelay time.Duration
}

// chatReportMaxAttempts bounds retries of one event on a transient
// (database) failure before it is logged and skipped, so one poisoned
// message cannot stall the partition forever.
const chatReportMaxAttempts = 5

func NewChatReportConsumer(brokers []string, topic, groupID string, dialer *kafka.Dialer, filer chatReportFiler) *ChatReportConsumer {
	return &ChatReportConsumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers, Topic: topic, GroupID: groupID, Dialer: dialer,
			MinBytes: 1, MaxBytes: 10e6, MaxWait: time.Second,
		}),
		filer:      filer,
		retryDelay: 2 * time.Second,
	}
}

// Start consumes until ctx is cancelled. Offsets are committed only after
// the event is filed (or judged permanently unfileable).
func (c *ChatReportConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("chat report consumer: fetch failed", "error", err)
			if !sleepCtx(ctx, c.retryDelay) {
				return
			}
			continue
		}
		var lastErr error
		for attempt := 1; attempt <= chatReportMaxAttempts; attempt++ {
			if lastErr = c.processMessage(ctx, m.Value); lastErr == nil {
				break
			}
			if ctx.Err() != nil || !sleepCtx(ctx, c.retryDelay*time.Duration(attempt)) {
				return
			}
		}
		if lastErr != nil {
			slog.Error("chat report consumer: event skipped after retries", "offset", m.Offset, "error", lastErr)
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
			slog.Warn("chat report consumer: commit failed", "error", err)
		}
	}
}

// processMessage files one event. nil means done (filed, duplicate, not a
// report event, or malformed); an error means a transient failure to retry.
func (c *ChatReportConsumer) processMessage(ctx context.Context, value []byte) error {
	var env struct {
		EventID   string          `json:"event_id"`
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(value, &env); err != nil {
		return nil
	}
	if env.EventType != chatEventMessageRequestReported {
		return nil
	}
	var e service.MessageRequestReported
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		slog.Warn("chat report consumer: undecodable payload skipped", "event_id", env.EventID)
		return nil
	}
	in, ok := service.MessageRequestReportInput(e)
	if !ok {
		slog.Warn("chat report consumer: malformed MessageRequestReported skipped", "event_id", env.EventID)
		return nil
	}
	_, err := c.filer.FileReportWith(ctx, in)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, postgres.ErrDuplicateSourceEvent), errors.Is(err, postgres.ErrActiveReportExists):
		return nil // already filed: redelivery, re-publish, or an earlier manual report
	case errors.Is(err, service.ErrInvalidReport):
		slog.Warn("chat report consumer: invalid report skipped", "event_id", env.EventID, "error", err)
		return nil
	default:
		return err
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (c *ChatReportConsumer) Close() error { return c.reader.Close() }
