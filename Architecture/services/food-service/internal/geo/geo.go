// Package geo holds the small amount of spherical maths food-service needs
// for dispatch radius, serviceability and ETA. HaversineKM is a copy of
// rider-service/internal/geo.HaversineKM (services do not import each other).
package geo

import "math"

const earthRadiusKM = 6371.0

// HaversineKM is the great-circle distance in km.
func HaversineKM(lat1, lng1, lat2, lng2 float64) float64 {
	dLat := degToRad(lat2 - lat1)
	dLng := degToRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degToRad(lat1))*math.Cos(degToRad(lat2))*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKM * c
}

// BoundingBox returns a lat/lng box that contains every point within
// radiusKM of (lat, lng). It is a cheap SQL prefilter only; callers still
// filter by HaversineKM.
func BoundingBox(lat, lng, radiusKM float64) (minLat, maxLat, minLng, maxLng float64) {
	dLat := radToDeg(radiusKM / earthRadiusKM)
	minLat, maxLat = lat-dLat, lat+dLat
	cos := math.Cos(degToRad(lat))
	if cos < 1e-6 || maxLat >= 90 || minLat <= -90 {
		return math.Max(minLat, -90), math.Min(maxLat, 90), -180, 180
	}
	dLng := radToDeg(radiusKM / (earthRadiusKM * cos))
	return minLat, maxLat, lng - dLng, lng + dLng
}

func degToRad(d float64) float64 { return d * math.Pi / 180.0 }
func radToDeg(r float64) float64 { return r * 180.0 / math.Pi }
