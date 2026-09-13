package postgres

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/atpost/food-service/internal/geo"
	"github.com/google/uuid"
)

// DispatchQuery asks for delivery partners near a pickup point.
type DispatchQuery struct {
	Lat, Lng float64
	RadiusKM float64
	// MaxLocationAge drops partners whose latest location ping is older.
	MaxLocationAge time.Duration
	Limit          int
}

// DispatchCandidate is one partner the dispatcher may offer a job to.
// UserID keys the realtime topic; PartnerID keys the offer row.
type DispatchCandidate struct {
	PartnerID  uuid.UUID
	UserID     uuid.UUID
	Latitude   float64
	Longitude  float64
	DistanceKM float64
	RecordedAt time.Time
}

// ListDispatchCandidates returns ACTIVE, online partners whose LATEST location
// row is fresh and within RadiusKM of the pickup point, nearest first.
//
// delivery_partners has no location timestamp of its own, so freshness comes
// from the newest food.delivery_partner_locations row (indexed on
// (delivery_partner_id, recorded_at DESC)). A bounding box prefilters in SQL;
// rankCandidates applies the exact radius.
func (s *Store) ListDispatchCandidates(ctx context.Context, q DispatchQuery) ([]DispatchCandidate, error) {
	if q.RadiusKM <= 0 {
		return nil, fmt.Errorf("dispatch radius must be positive")
	}
	if q.MaxLocationAge <= 0 {
		return nil, fmt.Errorf("dispatch location max age must be positive")
	}
	minLat, maxLat, minLng, maxLng := geo.BoundingBox(q.Lat, q.Lng, q.RadiusKM)
	rows, err := s.db.Query(ctx, `
		SELECT dp.id, dp.user_id, loc.latitude::float8, loc.longitude::float8, loc.recorded_at
		FROM food.delivery_partners dp
		JOIN LATERAL (
			SELECT l.latitude, l.longitude, l.recorded_at
			FROM food.delivery_partner_locations l
			WHERE l.delivery_partner_id = dp.id
			ORDER BY l.recorded_at DESC
			LIMIT 1
		) loc ON TRUE
		WHERE dp.status = 'ACTIVE'
		  AND dp.is_online = TRUE
		  AND loc.recorded_at >= NOW() - make_interval(secs => $1::float8)
		  AND loc.latitude::float8 BETWEEN $2::float8 AND $3::float8
		  AND loc.longitude::float8 BETWEEN $4::float8 AND $5::float8
		LIMIT 500
	`, q.MaxLocationAge.Seconds(), minLat, maxLat, minLng, maxLng)
	if err != nil {
		return nil, fmt.Errorf("list dispatch candidates: %w", err)
	}
	defer rows.Close()
	var out []DispatchCandidate
	for rows.Next() {
		var c DispatchCandidate
		if err := rows.Scan(&c.PartnerID, &c.UserID, &c.Latitude, &c.Longitude, &c.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rankCandidates(out, q.Lat, q.Lng, q.RadiusKM, q.Limit), nil
}

// rankCandidates fills DistanceKM, drops anyone beyond radiusKM, sorts nearest
// first and truncates to limit (limit <= 0 means no cap).
func rankCandidates(in []DispatchCandidate, lat, lng, radiusKM float64, limit int) []DispatchCandidate {
	out := make([]DispatchCandidate, 0, len(in))
	for _, c := range in {
		d := geo.HaversineKM(lat, lng, c.Latitude, c.Longitude)
		if d > radiusKM {
			continue
		}
		c.DistanceKM = d
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DistanceKM < out[j].DistanceKM })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
