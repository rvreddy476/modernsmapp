package pricing

import (
	"math"
	"time"
)

// MaxHopSpeedMPS discards GPS hops faster than 180 km/h: a teleporting fix,
// not a moped.
const MaxHopSpeedMPS = 50.0

// Deviation thresholds: completion re-prices distance only when the tracked
// route is longer than the quote by BOTH more than 20% AND more than 1 km.
const (
	DeviationRatioBPS = 2000
	DeviationMinM     = 1000
)

// TrackPoint is one rider_ride_track_points row.
type TrackPoint struct {
	Lat        float64
	Lng        float64
	RecordedAt time.Time
}

// TrackedDistanceMeters sums the haversine hops between consecutive points,
// ignoring a hop whose implied speed exceeds MaxHopSpeedMPS (the point is
// still the origin of the next hop, so one bad fix costs at most one hop).
func TrackedDistanceMeters(points []TrackPoint) int {
	var total float64
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		d := haversineM(a.Lat, a.Lng, b.Lat, b.Lng)
		dt := b.RecordedAt.Sub(a.RecordedAt).Seconds()
		if dt <= 0 {
			if d > 0 {
				continue // same instant, different place: a bad fix
			}
			continue
		}
		if d/dt > MaxHopSpeedMPS {
			continue
		}
		total += d
	}
	return int(math.Round(total))
}

// Deviates reports whether the tracked distance exceeds the quoted one by
// more than 20% and by more than 1 km.
func Deviates(quotedMeters, trackedMeters int) bool {
	if quotedMeters <= 0 || trackedMeters <= quotedMeters {
		return false
	}
	over := trackedMeters - quotedMeters
	return over > DeviationMinM && int64(over)*10000 > int64(quotedMeters)*DeviationRatioBPS
}

func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6371000.0
	dLat := (lat2 - lat1) * (math.Pi / 180.0)
	dLon := (lon2 - lon1) * (math.Pi / 180.0)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*(math.Pi/180.0))*math.Cos(lat2*(math.Pi/180.0))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusM * c
}
