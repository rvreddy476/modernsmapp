package service

import (
	"context"
	"os"
	"strconv"

	"github.com/atpost/shared/realtime"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	defaultDispatchRadiusKM              = 5.0
	defaultDispatchLocationMaxAgeSeconds = 120.0
)

// RealtimePublisher is the part of the shared realtime publishers the service
// uses; an interface so tests can capture published topics.
type RealtimePublisher interface {
	Publish(ctx context.Context, topic, eventType string, data any) error
}

// NewRealtimePublisher is the ONE constructor for food-service's live
// publisher. It must be the Redis Streams publisher (XADD rts:<topic>):
// notification-service's SSE gateway reads with realtime.NewStreamSubscriber
// (XREAD), so a Pub/Sub PUBLISH would never reach a client.
func NewRealtimePublisher(rdb *redis.Client) RealtimePublisher {
	return realtime.NewStreamPublisher(rdb)
}

// deliveryPartnerTopic is the ONE place the partner assignment topic is built.
// It is keyed by the partner's USER id: that is what the realtime token grants
// (the user can only prove who they are, not their delivery_partners.id), so
// the dispatch publishes must use the same key or the partner never hears the
// offer.
func deliveryPartnerTopic(userID uuid.UUID) string {
	return "food.delivery_partner." + userID.String() + ".assignments"
}

// orderTopic is the customer's live order topic (status, delivery and
// rider.location frames).
func orderTopic(orderID uuid.UUID) string {
	return "food.order." + orderID.String()
}

// restaurantOrdersTopic carries a restaurant's incoming orders.
func restaurantOrdersTopic(restaurantID uuid.UUID) string {
	return "food.restaurant." + restaurantID.String() + ".orders"
}

// restaurantTopic carries restaurant-level notices (FSSAI expiry).
func restaurantTopic(restaurantID uuid.UUID) string {
	return "food.restaurant." + restaurantID.String()
}

func envPositiveFloat(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
