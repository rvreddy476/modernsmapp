package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Module 2 M2-P0-2 — automated dead-letter replay.
//
// A DLQ that only an operator can drain is not a recovery mechanism; it is
// a record of the outage. For search eligibility that matters more than
// usual, because the messages most likely to fail during an OpenSearch
// incident are exactly the removals — rejection, takedown, visibility
// downgrade, deletion — and every one of those left unapplied is content
// that stays publicly searchable when it must not be.
//
// The replayer consumes the DLQ topic in its own consumer group and
// re-runs the ORIGINAL handler. Every handler it can reach is idempotent
// (removals unconditionally, indexing guarded by the monotonic SearchRev),
// so replaying a message that actually did succeed is harmless.
//
// A message that keeps failing is requeued with an incremented attempt
// count, and once the budget is spent it is parked on a separate topic and
// counted on DLQParked, which pages. Parking rather than silently dropping
// keeps the payload available, and the reconciler
// (cmd/backfill -entity posts) repairs the index independently.

const (
	dlqAttemptHeader = "x-dlq-attempt"
	// dlqMaxAttempts bounds automated replay. With the delay below this
	// spans roughly half an hour of transient-failure tolerance, which
	// comfortably covers an OpenSearch rolling restart or a node
	// replacement without human involvement.
	dlqMaxAttempts = 8
	// dlqReplayDelay is how long a message rests before being retried, so
	// replay does not hammer a service that is still unhealthy.
	dlqReplayDelay = 30 * time.Second
)

// DLQReplayer drains the search DLQ back through the normal handler.
type DLQReplayer struct {
	reader   *kafka.Reader
	parked   *kafka.Writer
	dlqWrite *kafka.Writer
	consumer *Consumer
	delay    time.Duration
}

// NewDLQReplayer builds a replayer bound to the same consumer used by the
// live indexer. Returns nil when the DLQ is disabled, so callers can start
// it unconditionally.
func NewDLQReplayer(brokers []string, groupID string, c *Consumer, dialer *kafka.Dialer) *DLQReplayer {
	if c == nil || c.dlq == nil || c.dlqTopic == "" || c.dlqTopic == "-" {
		return nil
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		GroupID: groupID + ".dlq-replay",
		Topic:   c.dlqTopic,
		// Small MinBytes: the DLQ is normally empty and we want a lone
		// stuck removal picked up promptly, not held for a batch.
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	parked := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    c.dlqTopic + ".parked",
		Balancer: &kafka.Hash{},
	}
	requeue := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    c.dlqTopic,
		Balancer: &kafka.Hash{},
	}
	if dialer != nil {
		tr := &kafka.Transport{Dial: dialer.DialFunc}
		parked.Transport = tr
		requeue.Transport = tr
	}
	return &DLQReplayer{
		reader:   reader,
		parked:   parked,
		dlqWrite: requeue,
		consumer: c,
		delay:    dlqReplayDelay,
	}
}

// Start runs the replay loop until ctx is cancelled.
//
// M2-P0-3: FetchMessage + explicit commit, for the same reason as the
// main consumer and with more at stake. The DLQ holds the ONLY remaining
// copy of a failed removal. ReadMessage committed the DLQ offset up
// front, so if the replay then failed and both the requeue and park
// writes also failed, that last copy was gone.
func (r *DLQReplayer) Start(ctx context.Context) {
	slog.Info("search: DLQ replayer started", "topic", r.reader.Config().Topic)
	for {
		m, err := r.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("search: DLQ replayer shutting down")
				return
			}
			slog.Error("search: DLQ read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Re-review P0-1: `continue` here would fetch the NEXT DLQ record,
		// and committing that later offset would implicitly commit this
		// one — destroying the only remaining copy of a failed removal.
		// Hold the partition instead.
		if !r.replayUntilDurable(ctx, m) {
			return
		}
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := r.reader.CommitMessages(commitCtx, m); err != nil {
			slog.Error("search: DLQ offset commit failed; message will be redelivered",
				"offset", m.Offset, "error", err)
		}
		cancel()
	}
}

// replayUntilDurable keeps retrying one dead-lettered message until it
// reaches a durable resting place. Reports false only on shutdown.
//
// The DLQ record is the last copy of a message that already failed once,
// so advancing past it without resolving it is an unrecoverable loss.
func (r *DLQReplayer) replayUntilDurable(ctx context.Context, m kafka.Message) bool {
	stall := 5 * time.Second
	const maxStall = 2 * time.Minute

	for {
		if r.replayOne(ctx, m) {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		slog.Error("search: DLQ record has no durable outcome; holding the partition",
			"offset", m.Offset, "retry_in", stall)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(stall):
		}
		if stall < maxStall {
			stall *= 2
			if stall > maxStall {
				stall = maxStall
			}
		}
	}
}

// replayOne re-applies one dead-lettered message. It reports whether the
// message reached a durable resting place — applied, requeued, or parked
// — which is the only condition under which its offset may advance.
func (r *DLQReplayer) replayOne(ctx context.Context, m kafka.Message) bool {
	attempt := dlqAttempt(m) + 1

	// Rest before retrying so we don't spin against a service that is
	// still down. Abort cleanly if we're shutting down.
	select {
	case <-ctx.Done():
		return false
	case <-time.After(r.delay):
	}

	err := r.consumer.processMessage(ctx, m)
	if err == nil {
		DLQReplays.WithLabelValues("recovered").Inc()
		slog.Info("search: DLQ message recovered", "attempt", attempt, "offset", m.Offset)
		return true
	}
	if ctx.Err() != nil {
		return false // shutting down; do not consume the retry budget
	}

	if attempt >= dlqMaxAttempts {
		return r.park(ctx, m, attempt, err)
	}

	// Requeue with the attempt count advanced.
	headers := setDLQAttempt(m.Headers, attempt)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if werr := r.dlqWrite.WriteMessages(wctx, kafka.Message{
		Key: m.Key, Value: m.Value, Headers: headers,
	}); werr != nil {
		// We could not even requeue. Try to park it so the payload
		// survives somewhere.
		slog.Error("search: DLQ requeue failed", "error", werr)
		return r.park(ctx, m, attempt, werr)
	}
	DLQReplays.WithLabelValues("requeued").Inc()
	slog.Warn("search: DLQ message requeued", "attempt", attempt, "error", err)
	return true
}

// park writes the message to the parked topic and raises the hard alert.
// Returns false when parking itself failed, in which case the caller must
// not commit — the DLQ copy is all that is left.
func (r *DLQReplayer) park(ctx context.Context, m kafka.Message, attempt int, cause error) bool {
	eventType := eventTypeOf(m)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	headers := append(setDLQAttempt(m.Headers, attempt),
		kafka.Header{Key: "x-parked-reason", Value: []byte(cause.Error())},
		kafka.Header{Key: "x-parked-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	if err := r.parked.WriteMessages(wctx, kafka.Message{
		Key: m.Key, Value: m.Value, Headers: headers,
	}); err != nil {
		slog.Error("search: parking write failed; keeping the message in the DLQ",
			"error", err)
		return false
	}
	DLQReplays.WithLabelValues("parked").Inc()
	DLQParked.WithLabelValues(eventType).Inc()
	// Deliberately Error level: this is the operator-actionable state.
	// The index is now known to potentially disagree with Postgres, and
	// only cmd/backfill -entity posts will repair it.
	slog.Error("search: DLQ message parked after exhausting automated replay",
		"attempts", attempt, "event_type", eventType, "cause", cause,
		"remedy", "run: go run ./cmd/backfill -entity posts")
	return true
}

func (r *DLQReplayer) Close() error {
	_ = r.parked.Close()
	_ = r.dlqWrite.Close()
	return r.reader.Close()
}

// eventTypeOf labels metrics with the event that could not be applied, so
// an alert distinguishes "a stale engagement bump was lost" from "a
// takedown was never applied".
func eventTypeOf(m kafka.Message) string {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err == nil && envelope.EventType != "" {
		return string(envelope.EventType)
	}
	if v := headerValue(m.Headers, "x-event-type"); v != "" {
		return v
	}
	return "unknown"
}

func dlqAttempt(m kafka.Message) int {
	v := headerValue(m.Headers, dlqAttemptHeader)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func setDLQAttempt(headers []kafka.Header, attempt int) []kafka.Header {
	out := make([]kafka.Header, 0, len(headers)+1)
	for _, h := range headers {
		if h.Key == dlqAttemptHeader {
			continue
		}
		out = append(out, h)
	}
	return append(out, kafka.Header{
		Key: dlqAttemptHeader, Value: []byte(strconv.Itoa(attempt)),
	})
}

func headerValue(headers []kafka.Header, key string) string {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
