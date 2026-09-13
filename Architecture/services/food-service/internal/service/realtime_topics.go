package service

import (
	"os"
	"strconv"

	"github.com/google/uuid"
)

const (
	defaultDispatchRadiusKM              = 5.0
	defaultDispatchLocationMaxAgeSeconds = 120.0
)

// deliveryPartnerTopic is the ONE place the partner assignment topic is built.
// It is keyed by the partner's USER id: that is what the realtime token grants
// (the user can only prove who they are, not their delivery_partners.id), so
// the dispatch publishes must use the same key or the partner never hears the
// offer.
func deliveryPartnerTopic(userID uuid.UUID) string {
	return "food.delivery_partner." + userID.String() + ".assignments"
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
