package engagement

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// BaseConsumer provides dedup and Kafka message deserialization for all
// engagement consumers. Each consumer embeds this and implements its own
// HandleEvent method.
type BaseConsumer struct {
	rdb  *redis.Client
	name string // consumer name, used as dedup namespace
}

// NewBaseConsumer creates a base consumer with dedup support.
func NewBaseConsumer(rdb *redis.Client, name string) *BaseConsumer {
	return &BaseConsumer{rdb: rdb, name: name}
}

// IsDuplicate checks if this event was already processed by this consumer.
// Uses Redis SETNX with 24h TTL for dedup.
// Returns true if the event is a duplicate (already processed).
func (c *BaseConsumer) IsDuplicate(ctx context.Context, eventID string) bool {
	key := fmt.Sprintf("consumed:%s:%s", c.name, eventID)
	set, err := c.rdb.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil {
		// On Redis error, assume not duplicate (fail-open, consumer is idempotent anyway)
		return false
	}
	// SetNX returns true if key was SET (not a duplicate), false if key already exists (duplicate)
	return !set
}

// ParseEvent deserializes a Kafka message into an EngagementEvent.
// The message value is expected to be the EngagementEvent JSON directly
// (the engagement producer writes it without the old EventEnvelope wrapper).
func ParseEvent(msg kafka.Message) (*EngagementEvent, error) {
	var event EngagementEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		return nil, fmt.Errorf("unmarshal engagement event: %w", err)
	}
	return &event, nil
}

// ConsumerLoop is a generic Kafka consumer loop that reads messages, parses them,
// and delegates to a handler function. It runs until the context is canceled.
func ConsumerLoop(ctx context.Context, reader *kafka.Reader, base *BaseConsumer, handler func(context.Context, *EngagementEvent) error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[%s] read error: %v", base.name, err)
			time.Sleep(time.Second)
			continue
		}

		event, err := ParseEvent(msg)
		if err != nil {
			log.Printf("[%s] parse error: %v", base.name, err)
			continue
		}

		if base.IsDuplicate(ctx, event.EventID) {
			continue
		}

		if err := handler(ctx, event); err != nil {
			log.Printf("[%s] handler error for event %s: %v", base.name, event.EventID, err)
		}
	}
}

// NewKafkaReader creates a Kafka reader for a consumer group on the engagement topic.
func NewKafkaReader(brokers []string, topic, groupID string) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: time.Second,
		StartOffset:    kafka.LastOffset,
	})
}
