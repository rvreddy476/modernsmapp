package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/chat-call-service/internal/store/postgres"
	events "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// OutboxRelay polls the outbox table and publishes events to Kafka topics.
type OutboxRelay struct {
	store        *postgres.CallStore
	lifecycle    *kafka.Writer
	notification *kafka.Writer
	analytics    *kafka.Writer
	log          *slog.Logger
	interval     time.Duration
}

func NewOutboxRelay(
	store *postgres.CallStore,
	lifecycle, notification, analytics *kafka.Writer,
	log *slog.Logger,
	interval time.Duration,
) *OutboxRelay {
	return &OutboxRelay{
		store:        store,
		lifecycle:    lifecycle,
		notification: notification,
		analytics:    analytics,
		log:          log,
		interval:     interval,
	}
}

func (r *OutboxRelay) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.poll(ctx)
		}
	}
}

func (r *OutboxRelay) poll(ctx context.Context) {
	evts, err := r.store.FetchUnpublished(ctx, 100)
	if err != nil {
		r.log.Warn("outbox fetch failed", "err", err)
		return
	}

	for _, e := range evts {
		envelope := events.EventEnvelope{
			EventID:    uuid.New().String(),
			EventType:  e.EventType,
			OccurredAt: e.CreatedAt,
			Payload:    e.Payload,
		}

		writer := r.topicForEvent(e.EventType)
		if writer == nil {
			r.log.Warn("no topic for event type", "event_type", e.EventType)
			_ = r.store.MarkPublished(ctx, e.ID)
			continue
		}

		envelopeBytes, err := json.Marshal(envelope)
		if err != nil {
			r.log.Warn("marshal envelope failed", "err", err, "event_type", e.EventType)
			continue
		}

		if err := writer.WriteMessages(ctx, kafka.Message{
			Key:   []byte(e.EventType),
			Value: envelopeBytes,
		}); err != nil {
			r.log.Warn("kafka publish failed", "err", err, "event_type", e.EventType)
			continue
		}

		_ = r.store.MarkPublished(ctx, e.ID)
	}
}

func (r *OutboxRelay) topicForEvent(eventType string) *kafka.Writer {
	switch eventType {
	case events.CallInvited, events.CallExpired:
		return r.notification
	case events.CallCreated, events.CallAccepted, events.CallDeclined,
		events.CallJoined, events.CallLeft, events.CallEnded,
		events.CallParticipantMuted, events.CallParticipantRemoved, events.CallUpgraded:
		return r.lifecycle
	default:
		return r.analytics
	}
}
