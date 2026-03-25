package engagement

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Producer publishes EngagementEvent messages to Kafka (Redpanda).
type Producer struct {
	writer *kafka.Writer
}

// NewProducer creates a new engagement event producer.
func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

// NewProducerWithDialer creates a new engagement event producer with an explicit Kafka dialer.
func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

// Publish sends an EngagementEvent to Kafka. The event is serialized as JSON
// with the EventID as the message key (for partition affinity by event).
func (p *Producer) Publish(ctx context.Context, event *EngagementEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal engagement event: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(event.EventID),
		Value: data,
	})
}

// BuildEvent creates an EngagementEvent with all standard fields populated.
func BuildEvent(
	eventType string,
	postID, userID, authorID, targetID uuid.UUID,
	targetType, action string,
	isSet bool,
	seq int64,
	actionTSMicro int64,
) *EngagementEvent {
	return &EngagementEvent{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		ActionTS:   time.UnixMicro(actionTSMicro),
		UserSeqNo:  seq,
		PostID:     postID,
		UserID:     userID,
		AuthorID:   authorID,
		TargetType: targetType,
		TargetID:   targetID,
		Action:     action,
		IsSet:      isSet,
		Version:    1,
	}
}

// Close shuts down the Kafka writer.
func (p *Producer) Close() error {
	return p.writer.Close()
}
