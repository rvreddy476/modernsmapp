package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

func riderCancelledNoShow(rideID, partnerID uuid.UUID, feePaise int64) events.RideCancelledPayload {
	return events.RideCancelledPayload{
		RideID:               rideID.String(),
		CancelledByKind:      "partner",
		CancelledByUserID:    partnerID.String(),
		Reason:               "customer_no_show",
		CancellationFeePaise: feePaise,
		CancelledAt:          time.Now().UTC(),
	}
}

// MarkRideNoShow is the partner's "customer never appeared" path,
// available after the partner has reached the pickup point and waited
// the configured grace window (mobile enforces; backend checks the ride is
// in a pre-trip state with this partner assigned).
//
// Side-effects:
//   - ride status -> cancelled_by_partner with reason customer_no_show,
//     no_show_* columns populated; the coupon and any reserved outstanding
//     lines are released;
//   - the customer owes the rule's cancellation fee, exactly as if they had
//     cancelled after the free window (pricing.CancelNoShow), recorded on
//     rider_customer_outstanding for the next quote;
//   - rider-events Kafka publish so notification-service can ping the
//     customer + admin queue, and a realtime emit.
func (s *Service) MarkRideNoShow(ctx context.Context, partnerUserID, rideID uuid.UUID, reason string) error {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return fmt.Errorf("not_found: ride")
		}
		return err
	}
	if ride.PartnerID == nil || *ride.PartnerID != partner.ID {
		return fmt.Errorf("forbidden: ride not assigned to this partner")
	}
	switch ride.Status {
	case "partner_assigned", "partner_arriving", "arrived":
	default:
		return fmt.Errorf("conflict: invalid state transition: no-show is only possible before the trip starts (current: %s)", ride.Status)
	}
	r := strings.TrimSpace(reason)
	if r == "" {
		r = "customer_no_show"
	}
	noShowBy := partner.ID
	updated, feePaise, err := s.cancelRide(ctx, ride, cancelParams{
		by: "partner", feeBy: pricing.CancelNoShow, to: "cancelled_by_partner",
		actorUserID: &partner.UserID, expectedRevision: ride.Revision, reason: r,
		noShowBy: &noShowBy,
	})
	if err != nil {
		return err
	}
	if perr := s.producer.PublishRideCancelled(ctx, riderCancelledNoShow(rideID, partner.ID, feePaise)); perr != nil {
		slog.Warn("rider: publish ride.cancelled (no_show) failed", "ride_id", rideID, "error", perr)
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.no_show", map[string]any{
		"ride_id":                rideID.String(),
		"partner_id":             partner.ID.String(),
		"reason":                 r,
		"cancellation_fee_paise": feePaise,
		"status":                 updated.Status,
	})
	return nil
}
