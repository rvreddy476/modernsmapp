package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/food-service/internal/geo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit 2026-10-30 L3: rider location pings are plausibility-checked.

const (
	// MaxRiderSpeedKMH is the fastest a rider is believed to move between two
	// pings. Anything faster is a teleport (spoofed GPS) and is dropped.
	MaxRiderSpeedKMH = 150.0
	// RiderJitterKM is GPS noise accepted regardless of the time between
	// pings, so two pings in the same second a few metres apart pass.
	RiderJitterKM = 0.25
	// minPingIntervalSeconds floors the elapsed time so a burst of pings is
	// not divided by zero.
	minPingIntervalSeconds = 1.0
)

// ErrLocationImplausible: the ping implies a speed above MaxRiderSpeedKMH
// since the rider's last accepted ping. HTTP 422 FOOD_DELIVERY_LOCATION_IMPLAUSIBLE.
var ErrLocationImplausible = errors.New("location update implies an impossible speed since the last one")

// PlausibleRiderMove reports whether moving distanceKM in elapsedSeconds is
// believable: within GPS jitter, or no faster than MaxRiderSpeedKMH. A
// refused ping is not recorded, so the elapsed time keeps growing and a rider
// who really travelled is accepted again once the speed is believable.
func PlausibleRiderMove(distanceKM, elapsedSeconds float64) bool {
	if distanceKM <= RiderJitterKM {
		return true
	}
	if elapsedSeconds < minPingIntervalSeconds {
		elapsedSeconds = minPingIntervalSeconds
	}
	return distanceKM/(elapsedSeconds/3600) <= MaxRiderSpeedKMH
}

// riderPingAllowed is the status rule for a ping: an ACTIVE partner, or an
// OFFLINE one still holding a job (so the customer keeps tracking a rider who
// toggled offline mid-delivery). PENDING_REVIEW, SUSPENDED, CLOSED and the
// rest are refused.
func riderPingAllowed(status string, onJob bool) bool {
	return status == "ACTIVE" || (status == "OFFLINE" && onJob)
}

// guardRiderPingTx locks the partner row (serialising the partner's pings),
// then applies the status rule and the speed check against the last accepted
// ping, using the database clock.
func guardRiderPingTx(ctx context.Context, tx pgx.Tx, partnerID uuid.UUID, lat, lng float64) error {
	var status string
	var onJob bool
	var prevLat, prevLng, elapsed *float64
	if err := tx.QueryRow(ctx, `
		SELECT dp.status::text,
			EXISTS (SELECT 1 FROM food.delivery_assignments a
				WHERE a.delivery_partner_id = dp.id
				  AND a.status IN ('ASSIGNED', 'ACCEPTED', 'ARRIVED_AT_RESTAURANT', 'PICKED_UP', 'ARRIVED_AT_CUSTOMER')),
			l.latitude::float8, l.longitude::float8,
			EXTRACT(EPOCH FROM (clock_timestamp() - l.recorded_at))::float8
		FROM food.delivery_partners dp
		LEFT JOIN LATERAL (
			SELECT latitude, longitude, recorded_at FROM food.delivery_partner_locations
			WHERE delivery_partner_id = dp.id ORDER BY recorded_at DESC LIMIT 1
		) l ON TRUE
		WHERE dp.id = $1
		FOR UPDATE OF dp
	`, partnerID).Scan(&status, &onJob, &prevLat, &prevLng, &elapsed); err != nil {
		return err
	}
	if !riderPingAllowed(status, onJob) {
		return fmt.Errorf("%w: partner is %s", ErrDeliveryPartnerNotActive, status)
	}
	if prevLat == nil || prevLng == nil || elapsed == nil {
		return nil
	}
	if d := geo.HaversineKM(*prevLat, *prevLng, lat, lng); !PlausibleRiderMove(d, *elapsed) {
		return fmt.Errorf("%w: %.1f km in %.0f s", ErrLocationImplausible, d, *elapsed)
	}
	return nil
}
