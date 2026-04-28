package consumers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// EntitlementChangedConsumer listens on monetization.events for
// entitlement.changed events and immediately invalidates the
// (subscriber, creator) entry in post-service's local entitlement
// cache. Pairs with the Tier-1a TTL-based read-through cache in
// internal/service/membership.go: the TTL handles the slow path
// (fan reads keep working with stale-but-bounded data after a
// monetization restart), this consumer handles the fast path (a
// new subscription unlocks gated content within seconds, not
// minutes).
//
// Idempotent: invalidating an already-empty key is a no-op.
type EntitlementChangedConsumer struct {
	svc      *service.Service
	consumer *sharedkafka.Consumer
}

const monetizationTopic = "monetization.events"

// EntitlementChangedPayload mirrors monetization-service's
// internal/events.EntitlementChangedPayload. Inlined here rather
// than imported to avoid a cross-service Go module dependency on
// monetization-service's internal package.
type EntitlementChangedPayload struct {
	SubscriptionID string `json:"subscription_id"`
	SubscriberID   string `json:"subscriber_id"`
	CreatorID      string `json:"creator_id"`
	Action         string `json:"action"`
}

func NewEntitlementChangedConsumer(
	svc *service.Service,
	brokers []string,
	rdb *redis.Client,
	m *metrics.KafkaConsumerMetrics,
) *EntitlementChangedConsumer {
	c := &EntitlementChangedConsumer{svc: svc}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:  brokers,
			GroupID:  "post-service-entitlement-changed",
			Topic:    monetizationTopic,
			DLQTopic: monetizationTopic + ".dlq",
		},
		rdb, m, c.handle,
	)
	return c
}

func (c *EntitlementChangedConsumer) Start(ctx context.Context) {
	c.consumer.Start(ctx)
}

func (c *EntitlementChangedConsumer) Close() error {
	return c.consumer.Close()
}

// handle is invoked by the shared kafka consumer for every event on
// the topic. We're only interested in entitlement.changed; everything
// else is silently ignored so the consumer group can stay subscribed
// to the broad monetization topic without re-routing on every new
// event type the monetization team adds.
func (c *EntitlementChangedConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != "entitlement.changed" {
		return nil
	}
	var p EntitlementChangedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		slog.Warn("entitlement consumer: bad payload", "error", err)
		return nil
	}
	subID, err := uuid.Parse(p.SubscriberID)
	if err != nil {
		return nil
	}
	creID, err := uuid.Parse(p.CreatorID)
	if err != nil {
		return nil
	}
	if err := c.svc.InvalidateEntitlementCache(ctx, subID, creID); err != nil {
		// Log + drop. The TTL will eventually clean up; a failed
		// invalidation just means up to 60s of staleness. Don't
		// retry-loop into the DLQ.
		slog.Warn("entitlement consumer: invalidate failed",
			"subscriber_id", p.SubscriberID,
			"creator_id", p.CreatorID,
			"action", p.Action,
			"error", err)
		return nil
	}
	slog.Debug("entitlement cache invalidated",
		"subscriber_id", p.SubscriberID,
		"creator_id", p.CreatorID,
		"action", p.Action)
	return nil
}
