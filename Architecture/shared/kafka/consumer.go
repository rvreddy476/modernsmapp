package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/transport"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"
)

// traceContextKey is an unexported context key for the W3C traceparent header value.
type traceContextKey struct{}

// HandlerFunc processes a single event envelope. Return nil on success.
type HandlerFunc func(ctx context.Context, envelope *events.EventEnvelope) error

// ConsumerConfig configures a resilient Kafka consumer.
type ConsumerConfig struct {
	Brokers      []string
	GroupID      string
	Topic        string
	DLQTopic     string        // e.g. "social.events.v1.dlq" — empty to disable DLQ
	MaxRetries   int           // default 3
	RetryBackoff time.Duration // default 1s (exponential: 1s, 2s, 4s)
	DedupTTL     time.Duration // default 24h
	// RetryForever is for launch-critical state transitions whose transient
	// failure may not be converted into a parked DLQ item. Handlers must wrap
	// genuinely unprocessable input with Permanent so poison can advance only
	// after a durable DLQ write.
	RetryForever bool
}

type permanentError struct{ error }

// Permanent marks a handler error that redelivery cannot repair (malformed or
// cryptographically invalid input). It is durably DLQed rather than stalling.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{error: err}
}

func isPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// Consumer is a resilient Kafka consumer with retry, DLQ, dedup, and metrics.
type Consumer struct {
	cfg     ConsumerConfig
	reader  *kafkago.Reader
	writer  *kafkago.Writer // for DLQ
	rdb     *redis.Client   // for dedup (nil = no dedup)
	metrics *metrics.KafkaConsumerMetrics
	handler HandlerFunc
}

// NewConsumer creates a new resilient consumer.
func NewConsumer(
	cfg ConsumerConfig,
	rdb *redis.Client,
	m *metrics.KafkaConsumerMetrics,
	handler HandlerFunc,
) *Consumer {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryBackoff == 0 {
		cfg.RetryBackoff = 1 * time.Second
	}
	if cfg.DedupTTL == 0 {
		cfg.DedupTTL = 24 * time.Hour
	}

	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		panic(fmt.Sprintf("kafka consumer dialer config invalid: %v", err))
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})

	var writer *kafkago.Writer
	if cfg.DLQTopic != "" {
		writer = kafkago.NewWriter(kafkago.WriterConfig{
			Brokers:  cfg.Brokers,
			Topic:    cfg.DLQTopic,
			Balancer: &kafkago.LeastBytes{},
			Dialer:   dialer,
		})
	}

	return &Consumer{
		cfg:     cfg,
		reader:  reader,
		writer:  writer,
		rdb:     rdb,
		metrics: m,
		handler: handler,
	}
}

// Start blocks, consuming messages until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	logger := logging.FromContext(ctx).With(
		"component", "kafka_consumer",
		"group", c.cfg.GroupID,
		"topic", c.cfg.Topic,
	)
	logger.Info("starting kafka consumer")

	// MS6: publish consumer lag every 10s so dashboards can alert
	// when a slow handler is falling behind production. Stats() is
	// cheap (in-memory counters maintained by kafka-go); we only
	// read Lag + skip the rest. Goroutine exits when ctx cancels.
	go c.reportLag(ctx)

	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("consumer shutting down")
				return
			}
			logger.Error("fetch error", "error", err)
			continue
		}

		// Extract W3C traceparent header from Kafka message headers and inject
		// it into the context so downstream handlers can propagate trace context.
		msgCtx := ctx
		for _, header := range msg.Headers {
			if header.Key == "traceparent" {
				msgCtx = context.WithValue(msgCtx, traceContextKey{}, string(header.Value))
				break
			}
		}
		c.processWithRetry(msgCtx, logger, msg)
	}
}

func (c *Consumer) processWithRetry(ctx context.Context, logger *slog.Logger, msg kafkago.Message) {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &envelope); err != nil {
		logger.Error("unmarshal error, sending to DLQ", "error", err, "offset", msg.Offset)
		if c.sendToDLQRawUntilDurable(ctx, logger, msg, err) {
			c.commitMessageUntilDurable(ctx, logger, msg)
		}
		return
	}

	// Dedup check — key includes topic so the same event ID on different topics
	// is treated as distinct and not incorrectly de-duplicated.
	if c.rdb != nil {
		dedupKey := fmt.Sprintf("consumed:%s:%s:%s", c.cfg.GroupID, msg.Topic, envelope.EventID)
		set, err := c.rdb.SetNX(ctx, dedupKey, "1", c.cfg.DedupTTL).Result()
		if err == nil && !set {
			if c.metrics != nil {
				c.metrics.DedupHits.WithLabelValues(c.cfg.Topic, c.cfg.GroupID).Inc()
			}
			c.commitMessageUntilDurable(ctx, logger, msg)
			return
		}
	}

	// Propagate trace ID from envelope into context
	msgCtx := ctx
	if envelope.TraceID != "" {
		msgCtx = trace.WithTraceID(msgCtx, envelope.TraceID)
	}
	msgLogger := logger.With("event_id", envelope.EventID, "event_type", envelope.EventType)
	if envelope.TraceID != "" {
		msgLogger = msgLogger.With("trace_id", envelope.TraceID)
	}
	msgCtx = logging.WithLogger(msgCtx, msgLogger)

	// Retry with exponential backoff. Launch-critical consumers retry
	// transient failures forever; only handler-marked poison reaches DLQ.
	var lastErr error
	for attempt := 0; c.cfg.RetryForever || attempt <= c.cfg.MaxRetries; attempt++ {
		start := time.Now()
		err := c.handler(msgCtx, &envelope)
		duration := time.Since(start)

		if err == nil {
			if c.metrics != nil {
				c.metrics.MessagesProcessed.WithLabelValues(
					c.cfg.Topic, c.cfg.GroupID, envelope.EventType,
				).Inc()
				c.metrics.ProcessDuration.WithLabelValues(
					c.cfg.Topic, c.cfg.GroupID, envelope.EventType,
				).Observe(duration.Seconds())
			}
			// Log processed message details for consumer lag awareness.
			slog.Debug("kafka message processed",
				"topic", msg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
			)
			c.commitMessageUntilDurable(ctx, logger, msg)
			return
		}

		lastErr = err
		if c.metrics != nil {
			c.metrics.ProcessingErrors.WithLabelValues(
				c.cfg.Topic, c.cfg.GroupID, envelope.EventType, "processing",
			).Inc()
		}

		if isPermanent(err) {
			if c.sendToDLQUntilDurable(ctx, msgLogger, msg, envelope, err) {
				c.commitMessageUntilDurable(ctx, logger, msg)
			}
			return
		}

		if c.cfg.RetryForever || attempt < c.cfg.MaxRetries {
			shift := attempt
			if shift > 5 {
				shift = 5
			}
			backoff := c.cfg.RetryBackoff * time.Duration(1<<shift)
			msgLogger.Warn("retrying message",
				"attempt", attempt+1,
				"retry_forever", c.cfg.RetryForever,
				"backoff", backoff,
				"error", err,
			)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
		}
	}

	// All retries exhausted — send to DLQ
	msgLogger.Error("max retries exhausted",
		"error", lastErr,
		"retries", c.cfg.MaxRetries,
	)
	if c.sendToDLQUntilDurable(ctx, msgLogger, msg, envelope, lastErr) {
		c.commitMessageUntilDurable(ctx, logger, msg)
	}
}

func (c *Consumer) sendToDLQ(ctx context.Context, logger *slog.Logger, msg kafkago.Message, env events.EventEnvelope, lastErr error) error {
	if c.writer == nil {
		return fmt.Errorf("no DLQ writer configured")
	}

	dlqHeaders := append(msg.Headers,
		kafkago.Header{Key: "x-dlq-error", Value: []byte(lastErr.Error())},
		kafkago.Header{Key: "x-dlq-consumer-group", Value: []byte(c.cfg.GroupID)},
		kafkago.Header{Key: "x-dlq-original-topic", Value: []byte(c.cfg.Topic)},
	)

	err := c.writer.WriteMessages(ctx, kafkago.Message{
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: dlqHeaders,
	})
	if err != nil {
		logger.Error("failed to write to DLQ", "error", err)
		return err
	}

	if c.metrics != nil {
		c.metrics.DLQMessages.WithLabelValues(c.cfg.Topic, c.cfg.GroupID, env.EventType).Inc()
	}
	logger.Warn("message sent to DLQ", "dlq_topic", c.cfg.DLQTopic)
	return nil
}

func (c *Consumer) sendToDLQRaw(ctx context.Context, logger *slog.Logger, msg kafkago.Message, lastErr error) error {
	if c.writer == nil {
		return fmt.Errorf("no DLQ writer configured")
	}

	dlqHeaders := append(msg.Headers,
		kafkago.Header{Key: "x-dlq-error", Value: []byte(lastErr.Error())},
		kafkago.Header{Key: "x-dlq-consumer-group", Value: []byte(c.cfg.GroupID)},
		kafkago.Header{Key: "x-dlq-original-topic", Value: []byte(c.cfg.Topic)},
	)

	err := c.writer.WriteMessages(ctx, kafkago.Message{
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: dlqHeaders,
	})
	if err != nil {
		logger.Error("failed to write to DLQ", "error", err)
		return err
	}

	if c.metrics != nil {
		c.metrics.DLQMessages.WithLabelValues(c.cfg.Topic, c.cfg.GroupID, "unknown").Inc()
	}
	logger.Warn("unparseable message sent to DLQ", "dlq_topic", c.cfg.DLQTopic)
	return nil
}

func (c *Consumer) sendToDLQUntilDurable(ctx context.Context, logger *slog.Logger, msg kafkago.Message, env events.EventEnvelope, cause error) bool {
	return retryDurable(ctx, logger, "DLQ write", func() error { return c.sendToDLQ(ctx, logger, msg, env, cause) })
}

func (c *Consumer) sendToDLQRawUntilDurable(ctx context.Context, logger *slog.Logger, msg kafkago.Message, cause error) bool {
	return retryDurable(ctx, logger, "raw DLQ write", func() error { return c.sendToDLQRaw(ctx, logger, msg, cause) })
}

func (c *Consumer) commitMessageUntilDurable(ctx context.Context, logger *slog.Logger, msg kafkago.Message) bool {
	return retryDurable(ctx, logger, "offset commit", func() error {
		return c.reader.CommitMessages(ctx, msg)
	})
}

// retryDurable blocks this consumer partition at the current message until the
// required durable action succeeds. Returning to FetchMessage after a failed
// DLQ write/commit lets a later commit leapfrog and erase this message.
func retryDurable(ctx context.Context, logger *slog.Logger, operation string, fn func() error) bool {
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		if err := fn(); err == nil {
			return true
		} else {
			logger.Error(operation+" failed; partition remains blocked", "attempt", attempt, "error", err)
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

// reportLag emits the consumer's lag (messages behind the partition
// head) as the kafka_consumer_lag gauge every 10s. kafka-go's
// Reader.Stats() returns lag as part of its in-memory counters; we
// read once per tick + publish per-partition. Falls silent if the
// metrics handle is nil (test paths).
func (c *Consumer) reportLag(ctx context.Context) {
	if c.metrics == nil || c.metrics.ConsumerLag == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := c.reader.Stats()
			// kafka-go aggregates lag across the partitions assigned
			// to this reader; we surface it on the partition="all"
			// label rather than per-partition because per-partition
			// lag isn't broken out by the library.
			c.metrics.ConsumerLag.WithLabelValues(c.cfg.Topic, "all", c.cfg.GroupID).Set(float64(stats.Lag))
		}
	}
}

// Close shuts down the consumer and DLQ writer.
func (c *Consumer) Close() error {
	if c.writer != nil {
		c.writer.Close()
	}
	return c.reader.Close()
}
