// Package events publishes live-service-v2 lifecycle events to the
// shared social.events.v1 Kafka topic.
//
// TODO: notification-service consumer pending
//
// notification-service does not yet subscribe to live.stream.started — when
// it does (planned in the v2 frontend sprint), the payload shape and
// actor partition key emitted here are the contract it must consume. The
// service publishes to the existing social.events.v1 topic so no new
// topic provisioning is required.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	return &Producer{
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  brokers,
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
			Dialer:   dialer,
		}),
	}
}

func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

// PublishStreamStarted emits live.stream.started. Partition key is the
// creatorID per the platform's recent Kafka-partition-key convention:
// every event whose downstream consumers shard by actor uses the actor
// as the key so order is preserved per-creator.
func (p *Producer) PublishStreamStarted(ctx context.Context, streamID, creatorID uuid.UUID, title, visibility string, startedAt time.Time) error {
	payload := events.LiveStreamStartedPayload{
		StreamID:   streamID.String(),
		CreatorID:  creatorID.String(),
		Title:      title,
		Visibility: visibility,
		StartedAt:  startedAt,
	}
	return p.publish(ctx, events.LiveStreamStarted, creatorID, payload)
}

func (p *Producer) PublishStreamEnded(ctx context.Context, streamID, creatorID uuid.UUID, endedAt time.Time, peak int) error {
	payload := events.LiveStreamEndedPayload{
		StreamID:   streamID.String(),
		CreatorID:  creatorID.String(),
		EndedAt:    endedAt,
		ViewerPeak: peak,
	}
	return p.publish(ctx, events.LiveStreamEnded, creatorID, payload)
}

func (p *Producer) PublishVODReady(ctx context.Context, streamID, creatorID uuid.UUID, recordingURL string, durationSec int) error {
	payload := events.LiveStreamVODReadyPayload{
		StreamID:     streamID.String(),
		CreatorID:    creatorID.String(),
		RecordingURL: recordingURL,
		DurationSec:  durationSec,
	}
	return p.publish(ctx, events.LiveStreamVODReady, creatorID, payload)
}

// publish builds the standard EventEnvelope and writes it on a 5s
// detached context so request cancellation cannot drop the event. Errors
// are logged at WARN and not surfaced; the publish path is best-effort
// (consumers tolerate at-least-once via dedup).
func (p *Producer) publish(ctx context.Context, eventType string, actorID uuid.UUID, payload any) error {
	if p == nil || p.writer == nil {
		return nil
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	actorStr := actorID.String()
	envelope := events.NewEnvelope(ctx, eventType, &actorStr, payloadBytes)
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	msg := kafka.Message{
		Key:   []byte(actorStr),
		Value: envelopeBytes,
	}
	go func() {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.writer.WriteMessages(writeCtx, msg); err != nil {
			slog.Warn("live-v2: async Kafka publish failed",
				"event_type", eventType,
				"event_id", envelope.EventID,
				"err", err)
		}
	}()
	return nil
}
