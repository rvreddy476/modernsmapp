package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

func riderCancelledNoShow(rideID, partnerID uuid.UUID) events.RideCancelledPayload {
	return events.RideCancelledPayload{
		RideID:            rideID.String(),
		CancelledByKind:   "partner",
		CancelledByUserID: partnerID.String(),
		Reason:            "customer_no_show",
		CancelledAt:       time.Now().UTC(),
	}
}

// MarkRideNoShow is the partner's "customer never appeared" path,
// available after the partner has reached the pickup point and waited
// the configured grace window (mobile enforces; backend just checks the
// ride is in an allowed pre-trip state).
//
// Side-effects:
//   - ride status → cancelled, no_show_* columns populated.
//   - rider-events Kafka publish so notification-service can ping the
//     customer + admin queue.
//   - realtime emit so any open SSE on rider.ride.{id} flips state.
func (s *Service) MarkRideNoShow(ctx context.Context, partnerUserID, rideID uuid.UUID, reason string) error {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	if err := s.store.MarkRideNoShow(ctx, rideID, partner.ID, reason); err != nil {
		return err
	}
	// Reuse the existing PublishRideCancelled with a no-show reason so
	// the downstream consumer doesn't need a brand-new event type.
	if perr := s.producer.PublishRideCancelled(ctx, riderCancelledNoShow(rideID, partner.ID)); perr != nil {
		slog.Warn("rider: publish ride.cancelled (no_show) failed", "ride_id", rideID, "error", perr)
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.no_show", map[string]any{
		"ride_id":    rideID.String(),
		"partner_id": partner.ID.String(),
		"reason":     reason,
	})
	return nil
}
