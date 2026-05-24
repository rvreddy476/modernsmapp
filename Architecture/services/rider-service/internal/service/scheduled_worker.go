package service

import (
	"context"
	"log/slog"
	"time"
)

// StartScheduledRideActivationWorker promotes scheduled rides to
// `requested` once `scheduled_for - scheduled_lead_min` is in the
// past. Runs every 30 seconds. Idempotent via SKIP LOCKED + the
// status='scheduled' guard on the UPDATE.
//
// Activation publishes ride.requested through both the Kafka producer
// and the realtime topics, so dispatch consumer + admin live board
// see the ride as if it had just been freshly booked.
func (s *Service) StartScheduledRideActivationWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	slog.Info("rider-service: scheduled-ride activation worker started")
	// Single immediate pass on startup so dev loops aren't stuck
	// waiting 30 s after a code change.
	s.activatePass(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("rider-service: scheduled-ride activation worker stopped")
			return
		case <-ticker.C:
			s.activatePass(ctx)
		}
	}
}

func (s *Service) activatePass(ctx context.Context) {
	rides, err := s.store.ListScheduledRidesDue(ctx, 25)
	if err != nil {
		slog.Warn("rider: list scheduled-due failed", "error", err)
		return
	}
	if len(rides) == 0 {
		return
	}
	for _, ride := range rides {
		if err := s.store.ActivateScheduledRide(ctx, ride.ID); err != nil {
			slog.Warn("rider: activate scheduled ride failed",
				"ride_id", ride.ID, "error", err)
			continue
		}
		// Re-fetch so downstream consumers see the promoted status.
		updated, err := s.store.GetRide(ctx, ride.ID)
		if err != nil {
			slog.Warn("rider: reload activated ride failed",
				"ride_id", ride.ID, "error", err)
			continue
		}
		cityID := ""
		if updated.CityID != nil {
			cityID = updated.CityID.String()
		}
		if perr := s.producer.PublishRideRequested(
			ctx, updated.ID, updated.CustomerUserID,
			updated.VehicleType, cityID,
		); perr != nil {
			slog.Warn("rider: publish ride.requested on activation failed",
				"ride_id", updated.ID, "error", perr)
		}
		s.emit(ctx, "rider.ride."+updated.ID.String(),
			"rider.ride.requested", updated)
		s.publishRealtime(ctx, "rider.admin.live_rides",
			"rider.ride.requested", updated)
	}
	slog.Info("rider-service: activated scheduled rides", "count", len(rides))
}
