package service

import (
	"context"
	"log/slog"
	"time"
)

// StartSequentialDispatchWorker advances expired/declined offers and recovers
// lost dispatch events. Multiple replicas are safe: creation locks the ride.
func (s *Service) StartSequentialDispatchWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if _, err := s.store.ExpireStaleOffers(ctx); err != nil {
			slog.Warn("rider: offer expiry failed", "error", err)
			continue
		}
		ids, err := s.store.ListRidesAwaitingOffer(ctx)
		if err != nil {
			slog.Warn("rider: dispatch recovery failed", "error", err)
			continue
		}
		for _, id := range ids {
			if _, err = s.MatchRide(ctx, id, MatchRideOptions{}); err != nil {
				slog.Warn("rider: sequential dispatch failed", "ride_id", id, "error", err)
			}
		}
	}
}
