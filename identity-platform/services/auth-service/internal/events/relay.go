package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/identity-platform/auth-service/internal/store"
)

// OutboxRelay polls the outbox table and publishes events to Kafka.
type OutboxRelay struct {
	store    *store.Store
	producer *Producer
	log      *slog.Logger
	interval time.Duration
}

func NewOutboxRelay(s *store.Store, p *Producer, logger *slog.Logger, interval time.Duration) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxRelay{store: s, producer: p, log: logger, interval: interval}
}

// Start runs the relay loop until context is cancelled.
func (r *OutboxRelay) Start(ctx context.Context) {
	r.log.Info("starting outbox relay", "interval", r.interval)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.log.Info("outbox relay stopped")
			return
		case <-ticker.C:
			r.process(ctx)
		}
	}
}

func (r *OutboxRelay) process(ctx context.Context) {
	events, err := r.store.FetchUnpublishedOutboxEvents(ctx, 50)
	if err != nil {
		r.log.Error("failed to fetch outbox events", "err", err)
		return
	}
	for _, e := range events {
		if err := r.producer.PublishRaw(ctx, e.EventType, "", json.RawMessage(e.Payload)); err != nil {
			r.log.Warn("failed to publish outbox event", "err", err, "event_id", e.ID, "event_type", e.EventType)
			continue
		}
		if err := r.store.MarkOutboxEventPublished(ctx, e.ID); err != nil {
			r.log.Warn("failed to mark outbox event published", "err", err, "event_id", e.ID)
		}
	}
}
