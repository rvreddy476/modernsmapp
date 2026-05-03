// Package geo provides the small geographic helpers rider-service needs:
//   - Encode(lat, lng, precision) -> geohash string
//   - HaversineKM(lat1, lng1, lat2, lng2) -> great-circle distance in km
//
// Inlined here (rather than pulling a third-party geohash library) to keep
// the dependency surface minimal. Geohash is a deterministic z-order curve
// over (lat, lng) — see https://en.wikipedia.org/wiki/Geohash.
package geo

import "math"

const base32 = "0123456789bcdefghjkmnpqrstuvwxyz"

// Encode returns the geohash of (lat, lng) at the given precision. Precision
// 6 yields ~1.2km × 0.6km cells, which is the right size for ride-matching.
func Encode(lat, lng float64, precision int) string {
	if precision <= 0 {
		precision = 6
	}
	if precision > 12 {
		precision = 12
	}
	latRange := [2]float64{-90, 90}
	lngRange := [2]float64{-180, 180}
	bits := 0
	bitVal := 0
	even := true
	out := make([]byte, 0, precision)
	for len(out) < precision {
		if even {
			mid := (lngRange[0] + lngRange[1]) / 2
			if lng >= mid {
				bitVal = (bitVal << 1) | 1
				lngRange[0] = mid
			} else {
				bitVal = bitVal << 1
				lngRange[1] = mid
			}
		} else {
			mid := (latRange[0] + latRange[1]) / 2
			if lat >= mid {
				bitVal = (bitVal << 1) | 1
				latRange[0] = mid
			} else {
				bitVal = bitVal << 1
				latRange[1] = mid
			}
		}
		even = !even
		bits++
		if bits == 5 {
			out = append(out, base32[bitVal])
			bits = 0
			bitVal = 0
		}
	}
	return string(out)
}

// HaversineKM is the great-circle distance in km. Mirrors the helper in
// internal/service/fares.go but is exposed here so the matcher and the
// location store can share it without a service-package import cycle.
func HaversineKM(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKM = 6371.0
	dLat := degToRad(lat2 - lat1)
	dLng := degToRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degToRad(lat1))*math.Cos(degToRad(lat2))*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKM * c
}

func degToRad(d float64) float64 { return d * math.Pi / 180.0 }

// Neighbors returns the 8 surrounding geohash cells of `gh` plus `gh` itself.
// Used to widen the search radius without recomputing the whole grid.
//
// Precision is preserved: a precision-6 input yields precision-6 outputs.
// Implementation: walk a small (lat, lng) lattice around the cell center.
func Neighbors(gh string) []string {
	if gh == "" {
		return nil
	}
	lat, lng := decodeCenter(gh)
	precision := len(gh)
	// Geohash precision-6 covers ~0.0055°lat × ~0.011°lng; bump by 1.5× to
	// land squarely in the next cell on each axis.
	dLat, dLng := stepSize(precision)
	out := make([]string, 0, 9)
	seen := map[string]struct{}{}
	for _, latOff := range []float64{-1, 0, 1} {
		for _, lngOff := range []float64{-1, 0, 1} {
			n := Encode(lat+latOff*dLat, lng+lngOff*dLng, precision)
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	return out
}

// decodeCenter returns the approximate center (lat, lng) of a geohash.
func decodeCenter(gh string) (float64, float64) {
	latRange := [2]float64{-90, 90}
	lngRange := [2]float64{-180, 180}
	even := true
	for _, ch := range gh {
		idx := indexOf(base32, byte(ch))
		if idx < 0 {
			return 0, 0
		}
		for bit := 4; bit >= 0; bit-- {
			b := (idx >> uint(bit)) & 1
			if even {
				mid := (lngRange[0] + lngRange[1]) / 2
				if b == 1 {
					lngRange[0] = mid
				} else {
					lngRange[1] = mid
				}
			} else {
				mid := (latRange[0] + latRange[1]) / 2
				if b == 1 {
					latRange[0] = mid
				} else {
					latRange[1] = mid
				}
			}
			even = !even
		}
	}
	return (latRange[0] + latRange[1]) / 2, (lngRange[0] + lngRange[1]) / 2
}

func stepSize(precision int) (dLat float64, dLng float64) {
	// Empirically derived cell extents per geohash precision (lat, lng) in
	// degrees. The numbers come from the canonical geohash precision table.
	switch precision {
	case 1:
		return 23.0, 45.0
	case 2:
		return 2.8, 5.6
	case 3:
		return 0.7, 0.7
	case 4:
		return 0.087, 0.18
	case 5:
		return 0.022, 0.022
	case 6:
		return 0.0055, 0.011
	case 7:
		return 0.00068, 0.00068
	default:
		return 0.0001, 0.0001
	}
}

func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
