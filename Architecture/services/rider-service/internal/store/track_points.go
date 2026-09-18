package store

import (
	"context"
	"fmt"

	"github.com/atpost/rider-service/internal/pricing"
	"github.com/google/uuid"
)

// AppendTrackPointForActiveRide records a partner GPS fix against the
// partner's ride in arrived / in_progress, if any. Throttled in SQL: a fix
// is skipped when the ride's last point is under five seconds old. Returns
// whether a point was written.
func (s *Store) AppendTrackPointForActiveRide(ctx context.Context, partnerID uuid.UUID, lat, lng float64, speedMPS *float64) (bool, error) {
	const q = `
        INSERT INTO rider_ride_track_points (ride_id, partner_id, lat, lng, speed_mps)
        SELECT r.id, $1, $2, $3, $4
        FROM rider_rides r
        WHERE r.partner_id = $1
          AND r.status IN ('arrived','otp_verified','in_progress')
          AND NOT EXISTS (
              SELECT 1 FROM rider_ride_track_points t
              WHERE t.ride_id = r.id AND t.recorded_at > NOW() - INTERVAL '5 seconds')
        LIMIT 1`
	tag, err := s.db.Exec(ctx, q, partnerID, lat, lng, speedMPS)
	if err != nil {
		return false, fmt.Errorf("append track point: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListTrackPoints returns the ride's points in recording order.
func (s *Store) ListTrackPoints(ctx context.Context, rideID uuid.UUID) ([]pricing.TrackPoint, error) {
	const q = `SELECT lat, lng, recorded_at FROM rider_ride_track_points WHERE ride_id = $1 ORDER BY recorded_at ASC, id ASC`
	rows, err := s.db.Query(ctx, q, rideID)
	if err != nil {
		return nil, fmt.Errorf("list track points: %w", err)
	}
	defer rows.Close()
	var out []pricing.TrackPoint
	for rows.Next() {
		var p pricing.TrackPoint
		if err := rows.Scan(&p.Lat, &p.Lng, &p.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
