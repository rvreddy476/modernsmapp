// Package consumers wires Kafka consumers for rider-service.
//
// P0.2 — ride dispatch worker. Closes the gap the 2026-05-21 audit
// flagged: CreateRide publishes `rider.ride.requested` but no
// consumer was calling MatchRide, so a fresh ride could sit in
// `requested` indefinitely. This consumer subscribes to the
// rider-events topic, switches on event_type, and invokes the
// service's MatchRide on every `rider.ride.requested` payload.
//
// Resilience: the shared kafka.Consumer wraps us with retry, DLQ,
// and Redis-backed dedup, so duplicate `rider.ride.requested`
// envelopes (same event_id) are silently dropped — no duplicate
// offer rows. MatchRide is itself idempotent on rides already in
// `searching_partner` so even a dedup miss is safe.
package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	riderevents "github.com/atpost/rider-service/internal/events"
	ridersvc "github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// DispatchConsumer reads the rider-events topic and dispatches each
// envelope to the appropriate service handler. Currently handles:
//
//   - rider.ride.requested → svc.MatchRide
//
// New event handlers slot into the switch below without touching the
// transport plumbing.
type DispatchConsumer struct {
	svc      *ridersvc.Service
	consumer *kafka.Consumer
}

// NewDispatchConsumer wires the shared kafka.Consumer with a handler
// that translates rider-events envelopes into MatchRide calls.
func NewDispatchConsumer(
	svc *ridersvc.Service,
	brokers []string,
	topic string,
	rdb *redis.Client,
	m *metrics.KafkaConsumerMetrics,
) *DispatchConsumer {
	dc := &DispatchConsumer{svc: svc}
	dc.consumer = kafka.NewConsumer(
		kafka.ConsumerConfig{
			Brokers:    brokers,
			GroupID:    "rider-service-dispatch",
			Topic:      topic,
			DLQTopic:   topic + ".dlq",
			MaxRetries: 3,
		},
		rdb, m, dc.handle,
	)
	return dc
}

// Start blocks until ctx is cancelled. Called from main in a goroutine.
func (dc *DispatchConsumer) Start(ctx context.Context) {
	dc.consumer.Start(ctx)
}

// handle is the per-envelope switch. Unknown event types are a
// non-error no-op so the same topic can carry many event types and a
// future addition doesn't dead-letter every message.
func (dc *DispatchConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.EventRiderRideRequested:
		return dc.handleRideRequested(ctx, env)
	default:
		// Side consumer of the topic — every other event type belongs
		// to a different subscriber. Ack without doing anything.
		return nil
	}
}

func (dc *DispatchConsumer) handleRideRequested(ctx context.Context, env *events.EventEnvelope) error {
	var payload riderevents.RideRequestedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("decode ride_requested payload: %w", err)
	}
	rideID, err := uuid.Parse(payload.RideID)
	if err != nil {
		return fmt.Errorf("invalid ride id %q: %w", payload.RideID, err)
	}
	start := time.Now()
	// MatchRide is idempotent: a ride already in `searching_partner`
	// just runs the next batch, which is what we want when the same
	// event_id slips past the dedup cache.
	result, err := dc.svc.MatchRide(ctx, rideID, ridersvc.MatchRideOptions{})
	if err != nil {
		slog.Error("rider dispatch: MatchRide failed",
			"ride_id", rideID,
			"event_id", env.EventID,
			"error", err)
		return err
	}
	slog.Info("rider dispatch: matched",
		"ride_id", rideID,
		"event_id", env.EventID,
		"offers", result.OffersCreated,
		"no_candidates", result.NoCandidates,
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}
