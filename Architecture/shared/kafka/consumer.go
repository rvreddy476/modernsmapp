package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/facebook-like/shared/events"
	"github.com/facebook-like/shared/o11y/logging"
	"github.com/facebook-like/shared/o11y/metrics"
	"github.com/facebook-like/shared/o11y/trace"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"
)

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

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})

	var writer *kafkago.Writer
	if cfg.DLQTopic != "" {
		writer = &kafkago.Writer{
			Addr:     kafkago.TCP(cfg.Brokers...),
			Topic:    cfg.DLQTopic,
			Balancer: &kafkago.LeastBytes{},
		}
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

		c.processWithRetry(ctx, logger, msg)
	}
}

func (c *Consumer) processWithRetry(ctx context.Context, logger *slog.Logger, msg kafkago.Message) {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &envelope); err != nil {
		logger.Error("unmarshal error, sending to DLQ", "error", err, "offset", msg.Offset)
		c.sendToDLQRaw(ctx, logger, msg, err)
		c.commitMessage(ctx, logger, msg)
		return
	}

	// Dedup check
	if c.rdb != nil {
		dedupKey := fmt.Sprintf("consumed:%s:%s", c.cfg.GroupID, envelope.EventID)
		set, err := c.rdb.SetNX(ctx, dedupKey, "1", c.cfg.DedupTTL).Result()
		if err == nil && !set {
			if c.metrics != nil {
				c.metrics.DedupHits.WithLabelValues(c.cfg.Topic, c.cfg.GroupID).Inc()
			}
			c.commitMessage(ctx, logger, msg)
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

	// Retry with exponential backoff
	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
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
			c.commitMessage(ctx, logger, msg)
			return
		}

		lastErr = err
		if c.metrics != nil {
			c.metrics.ProcessingErrors.WithLabelValues(
				c.cfg.Topic, c.cfg.GroupID, envelope.EventType, "processing",
			).Inc()
		}

		if attempt < c.cfg.MaxRetries {
			backoff := c.cfg.RetryBackoff * (1 << attempt)
			msgLogger.Warn("retrying message",
				"attempt", attempt+1,
				"max_retries", c.cfg.MaxRetries,
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
	c.sendToDLQ(ctx, msgLogger, msg, envelope, lastErr)
	c.commitMessage(ctx, logger, msg)
}

func (c *Consumer) sendToDLQ(ctx context.Context, logger *slog.Logger, msg kafkago.Message, env events.EventEnvelope, lastErr error) {
	if c.writer == nil {
		return
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
		return
	}

	if c.metrics != nil {
		c.metrics.DLQMessages.WithLabelValues(c.cfg.Topic, c.cfg.GroupID, env.EventType).Inc()
	}
	logger.Warn("message sent to DLQ", "dlq_topic", c.cfg.DLQTopic)
}

func (c *Consumer) sendToDLQRaw(ctx context.Context, logger *slog.Logger, msg kafkago.Message, lastErr error) {
	if c.writer == nil {
		return
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
		return
	}

	if c.metrics != nil {
		c.metrics.DLQMessages.WithLabelValues(c.cfg.Topic, c.cfg.GroupID, "unknown").Inc()
	}
	logger.Warn("unparseable message sent to DLQ", "dlq_topic", c.cfg.DLQTopic)
}

func (c *Consumer) commitMessage(ctx context.Context, logger *slog.Logger, msg kafkago.Message) {
	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		logger.Error("commit error", "error", err, "offset", msg.Offset)
	}
}

// Close shuts down the consumer and DLQ writer.
func (c *Consumer) Close() error {
	if c.writer != nil {
		c.writer.Close()
	}
	return c.reader.Close()
}
