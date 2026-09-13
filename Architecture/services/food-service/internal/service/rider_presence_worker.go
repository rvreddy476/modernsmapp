package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	// RiderPingTimeout: an online rider silent this long is switched offline.
	RiderPingTimeout = 5 * time.Minute
	// LocationHistoryRetention: rider location history older than this is
	// deleted.
	LocationHistoryRetention = 30 * 24 * time.Hour

	riderPresenceTick = 30 * time.Second
	// locationPurgeEveryTicks runs the purge on the first tick, then hourly.
	locationPurgeEveryTicks = 120
	locationPurgeBatch      = 5000
)

// EventRiderLocation is the live rider position frame on food.order.<id>.
// Realtime only: it is never written to the outbox.
const EventRiderLocation = "rider.location"

// StartRiderPresenceWorker switches silent riders offline every 30 seconds and
// purges location history past its retention once an hour.
func (s *Service) StartRiderPresenceWorker(ctx context.Context) {
	ticker := time.NewTicker(riderPresenceTick)
	defer ticker.Stop()
	slog.Info("food-service: rider presence worker started",
		"ping_timeout", RiderPingTimeout, "location_retention", LocationHistoryRetention)
	tick := 0
	for {
		select {
		case <-ctx.Done():
			slog.Info("food-service: rider presence worker stopped")
			return
		case <-ticker.C:
			s.runRiderPresencePass(ctx, tick)
			tick++
		}
	}
}

func (s *Service) runRiderPresencePass(ctx context.Context, tick int) {
	if ids, err := s.store.AutoOfflineStaleDeliveryPartners(ctx, RiderPingTimeout); err != nil {
		slog.Warn("food-service: rider auto-offline pass failed", "error", err)
	} else if len(ids) > 0 {
		slog.Info("food-service: switched silent riders offline", "count", len(ids))
	}
	if tick%locationPurgeEveryTicks != 0 {
		return
	}
	res, err := s.store.PurgeDeliveryLocationHistory(ctx, LocationHistoryRetention, locationPurgeBatch)
	if err != nil {
		slog.Warn("food-service: location history purge failed", "error", err)
		return
	}
	if res.Locations > 0 || res.TrackingPings > 0 {
		slog.Info("food-service: purged rider location history",
			"locations", res.Locations, "tracking_pings", res.TrackingPings)
	}
}

// UpdateDeliveryLocation records the ping on every active assignment,
// recomputes the ETA of each order the store claimed for it (at most once a
// minute per order), and publishes a rider.location frame for each order the
// store cleared (inside the accepted-to-delivered window, and not throttled).
func (s *Service) UpdateDeliveryLocation(ctx context.Context, userID uuid.UUID, in postgres.LocationUpdate) (*postgres.DeliveryLocationResult, error) {
	res, err := s.store.UpdateDeliveryLocation(ctx, userID, in)
	if err != nil {
		return nil, err
	}
	fresh := s.recomputeETAs(ctx, res)
	for _, f := range res.Frames {
		s.publishRealtime(ctx, orderTopic(f.OrderID), EventRiderLocation, riderLocationPayload(res, f, fresh))
	}
	return res, nil
}

// riderLocationPayload is the rider.location frame: lat, lng, heading (only
// when the rider sent one), recorded_at, and eta_at + eta_source (B6, only
// when the order has an ETA). The ETA is this ping's recomputation when it
// made one, otherwise the order's stored ETA.
//
// The ETA rides this frame rather than a separate order.eta frame: the
// tracking screen already consumes rider.location, which is throttled to one
// per order per 5 s, so a client that joins late has the ETA within 5 s
// without another subscription or event type, and no ETA frame can go out for
// an order whose rider position is not shareable. Realtime only: never the
// outbox (no Kafka consumer reads ETAs).
func riderLocationPayload(res *postgres.DeliveryLocationResult, f postgres.RiderLocationFrame, fresh map[uuid.UUID]orderETA) map[string]any {
	frame := map[string]any{
		"order_id":    f.OrderID.String(),
		"lat":         res.Latitude,
		"lng":         res.Longitude,
		"recorded_at": res.RecordedAtTime.UTC().Format(time.RFC3339Nano),
	}
	if res.Heading != nil {
		frame["heading"] = *res.Heading
	}
	etaAt, etaSource := f.ETAAt, f.ETASource
	if eta, ok := fresh[f.OrderID]; ok {
		etaAt, etaSource = &eta.At, eta.Source
	}
	if etaAt != nil && etaSource != "" {
		frame["eta_at"] = postgres.FormatETA(*etaAt)
		frame["eta_source"] = etaSource
	}
	return frame
}
