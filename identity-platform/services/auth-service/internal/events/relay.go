package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
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

// backlogWarnCount / backlogWarnAge bound a healthy outbox. Crossing either
// means the relay or Kafka has stalled and downstream projections are drifting.
const (
	backlogWarnCount = 100
	backlogWarnAge   = 2 * time.Minute
)

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

	// Surface a stalled relay: a backlog that is large or aging means
	// user.registered events are not reaching the projections.
	if count, oldest, err := r.store.OutboxBacklog(ctx); err == nil {
		if count > backlogWarnCount || (count > 0 && time.Since(oldest) > backlogWarnAge) {
			r.log.Warn("outbox backlog — relay or Kafka may be stalled",
				"unpublished", count, "oldest_age_seconds", int(time.Since(oldest).Seconds()))
		}
	}
}
