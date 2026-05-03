package store

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// geohashBase32 is the standard geohash alphabet (RFC).
const geohashBase32 = "0123456789bcdefghjkmnpqrstuvwxyz"

// EncodeGeohash encodes a (lat, lon) pair to a geohash string of `precision`
// characters. Implementation follows the standard interleaved-bit algorithm so
// we don't pull in a third-party dependency just for this.
//
// Sprint 2 plan: precision 7 yields ~150 m × 150 m cells — adequate for the
// 25 km default Pulse radius bucketing.
func EncodeGeohash(lat, lon float64, precision int) string {
	if precision <= 0 {
		precision = 7
	}
	if math.IsNaN(lat) || math.IsNaN(lon) {
		return ""
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return ""
	}

	latRange := [2]float64{-90.0, 90.0}
	lonRange := [2]float64{-180.0, 180.0}

	bits := 0
	bit := 0
	even := true
	out := make([]byte, 0, precision)

	for len(out) < precision {
		if even {
			mid := (lonRange[0] + lonRange[1]) / 2
			if lon >= mid {
				bits = (bits << 1) | 1
				lonRange[0] = mid
			} else {
				bits = bits << 1
				lonRange[1] = mid
			}
		} else {
			mid := (latRange[0] + latRange[1]) / 2
			if lat >= mid {
				bits = (bits << 1) | 1
				latRange[0] = mid
			} else {
				bits = bits << 1
				latRange[1] = mid
			}
		}
		even = !even
		bit++
		if bit == 5 {
			out = append(out, geohashBase32[bits])
			bits = 0
			bit = 0
		}
	}
	return string(out)
}

// DistanceKm returns the great-circle distance between (lat1, lon1) and
// (lat2, lon2) in kilometres using the haversine formula.
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	rad := math.Pi / 180.0
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

// SetProfileGeohash recomputes location_geohash for the given user from the
// current latitude/longitude. Idempotent — clears the column if either
// coordinate is NULL. Called from the Service layer after every profile
// upsert so the matcher's hard-filter index is always consistent with the
// raw lat/lon columns.
func (s *Store) SetProfileGeohash(ctx context.Context, userID uuid.UUID) error {
	row := s.db.QueryRow(ctx, `
        SELECT latitude, longitude
        FROM dating_profiles
        WHERE user_id = $1 AND deleted_at IS NULL`, userID)
	var lat, lon *float64
	if err := row.Scan(&lat, &lon); err != nil {
		return fmt.Errorf("read coords for geohash: %w", err)
	}
	if lat == nil || lon == nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET location_geohash = NULL WHERE user_id = $1`, userID)
		return nil
	}
	gh := EncodeGeohash(*lat, *lon, 7)
	if gh == "" {
		return nil
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET location_geohash = $2 WHERE user_id = $1`, userID, gh); err != nil {
		return fmt.Errorf("update location_geohash: %w", err)
	}
	return nil
}
