package service

import (
	"context"
	"log/slog"
	"time"
)

// StartStaleGPSWorker runs every 30s and force-offlines partners
// whose last location ping is older than `staleAfter`. The mobile
// app pings every ~5-15s while online; we treat 90 seconds without
// any update as "GPS is stale or app got killed" and remove the
// partner from the matchable set so customers don't get phantom-pin
// matches.
//
// The implementation uses SKIP LOCKED so multiple replicas running
// the worker can coexist without contending.
func (s *Service) StartStaleGPSWorker(ctx context.Context) {
	const staleAfter = 90 * time.Second
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	slog.Info("rider-service: stale-GPS worker started", "stale_after", staleAfter)
	for {
		select {
		case <-ctx.Done():
			slog.Info("rider-service: stale-GPS worker stopped")
			return
		case <-ticker.C:
			ids, err := s.store.ForceOfflineStaleGPS(ctx, staleAfter)
			if err != nil {
				slog.Warn("rider-service: force-offline pass failed", "error", err)
				continue
			}
			if len(ids) == 0 {
				continue
			}
			slog.Info("rider-service: forced offline due to stale GPS", "count", len(ids))
			for _, pid := range ids {
				// Publish partner.offline so downstream consumers (matcher
				// cache, admin board) react. Best-effort.
				if perr := s.producer.PublishPartnerOffline(ctx, pid); perr != nil {
					slog.Warn("rider-service: publish partner.offline failed", "partner_id", pid, "error", perr)
				}
				s.publishRealtime(ctx, "rider.partner."+pid.String()+".offers", "rider.partner.forced_offline", map[string]any{
					"partner_id": pid.String(),
					"reason":     "stale_gps",
				})
			}
		}
	}
}
