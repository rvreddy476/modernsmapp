package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
)

// StartMediaEventOutboxRelay drains transcode request and completion events.
// It publishes first and marks second: a crash can duplicate an event with the
// same ID, but can never erase the only durable copy of required work.
func (s *Service) StartMediaEventOutboxRelay(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			s.drainMediaEventOutbox(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Service) drainMediaEventOutbox(ctx context.Context) {
	if s == nil || s.pgStore == nil || s.producer == nil {
		return
	}
	rows, err := s.pgStore.PendingMediaEvents(ctx, "", 100)
	if err != nil {
		slog.Error("media outbox: list failed", "error", err)
		return
	}
	for _, row := range rows {
		envelope := events.EventEnvelope{
			EventID:     row.EventID,
			EventType:   row.EventType,
			OccurredAt:  row.OccurredAt,
			ActorUserID: row.ActorUserID,
			Payload:     row.Payload,
		}
		if err := s.producer.PublishEnvelope(ctx, envelope); err != nil {
			_ = s.pgStore.RecordMediaEventFailure(ctx, row.EventID, err)
			slog.Error("media outbox: publish failed", "event_id", row.EventID,
				"event_type", row.EventType, "error", err)
			return // preserve ordering; do not leapfrog a dependency failure
		}
		if err := s.pgStore.MarkMediaEventPublished(ctx, row.EventID); err != nil {
			slog.Error("media outbox: mark published failed; duplicate will be retried",
				"event_id", row.EventID, "error", err)
			return
		}
	}
}
