package store

import (
	"math"
	"strings"
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

// GeohashNeighbours returns the 8 neighbouring geohash cells of `gh` at
// the same precision plus `gh` itself (9 cells total). The candidate
// query uses this to cover the radius without a haversine post-filter
// on a too-narrow geohash prefix — a viewer on a cell boundary sees
// candidates across the boundary.
//
// Returns nil for an empty / invalid input so callers can branch.
//
// The geohash cell-neighbour algorithm relies on a 32-character base
// alphabet and pre-computed "borders" + "neighbours" lookup tables —
// stdlib-only via the official RFC reference tables below. Faster than
// re-encoding lat/lon at every offset.
func GeohashNeighbours(gh string) []string {
	if gh == "" {
		return nil
	}
	out := make([]string, 0, 9)
	out = append(out, gh)
	for _, dir := range []string{"n", "s", "e", "w"} {
		if n := geohashAdjacent(gh, dir); n != "" {
			out = append(out, n)
		}
	}
	// Corners via composition.
	if ne := geohashAdjacent(geohashAdjacent(gh, "n"), "e"); ne != "" {
		out = append(out, ne)
	}
	if nw := geohashAdjacent(geohashAdjacent(gh, "n"), "w"); nw != "" {
		out = append(out, nw)
	}
	if se := geohashAdjacent(geohashAdjacent(gh, "s"), "e"); se != "" {
		out = append(out, se)
	}
	if sw := geohashAdjacent(geohashAdjacent(gh, "s"), "w"); sw != "" {
		out = append(out, sw)
	}
	return out
}

// geohashAdjacent implements the standard geohash cell-adjacency lookup.
// Tables from the geohash.org reference implementation.
func geohashAdjacent(gh, dir string) string {
	if gh == "" {
		return ""
	}
	neighbours := map[string][2]string{
		"n": {"p0r21436x8zb9dcf5h7kjnmqesgutwvy", "bc01fg45238967deuvhjyznpkmstqrwx"},
		"s": {"14365h7k9dcfesgujnmqp0r2twvyx8zb", "238967debc01fg45kmstqrwxuvhjyznp"},
		"e": {"bc01fg45238967deuvhjyznpkmstqrwx", "p0r21436x8zb9dcf5h7kjnmqesgutwvy"},
		"w": {"238967debc01fg45kmstqrwxuvhjyznp", "14365h7k9dcfesgujnmqp0r2twvyx8zb"},
	}
	borders := map[string][2]string{
		"n": {"prxz", "bcfguvyz"},
		"s": {"028b", "0145hjnp"},
		"e": {"bcfguvyz", "prxz"},
		"w": {"0145hjnp", "028b"},
	}
	last := gh[len(gh)-1]
	parent := gh[:len(gh)-1]
	odd := len(gh)%2 == 0 // base case: first char is "even" (lon bit first)
	idx := 0
	if odd {
		idx = 1
	}
	if strings.ContainsRune(borders[dir][idx], rune(last)) {
		if parent == "" {
			return ""
		}
		parent = geohashAdjacent(parent, dir)
		if parent == "" {
			return ""
		}
	}
	nextChar := strings.IndexRune(neighbours[dir][idx], rune(last))
	if nextChar < 0 || nextChar >= len(geohashBase32) {
		return ""
	}
	return parent + string(geohashBase32[nextChar])
}

// GeohashPrefixForRadiusKm returns the geohash prefix length that
// covers a given radius (km) with reasonable bounding-box overhead.
// Used by the discovery query to LIKE-match the candidate's geohash
// against the viewer's prefix — index-friendly + index-bounded.
//
// Cell-size cheat sheet (approximate, mid-latitude):
//
//	prec 1 -> ~5,000 km
//	prec 2 -> ~1,250 km
//	prec 3 -> ~150 km
//	prec 4 -> ~40 km
//	prec 5 -> ~5 km
//	prec 6 -> ~1.2 km
//	prec 7 -> ~150 m
//
// For a 25 km radius we want prec 4 (~40 km) so the 9-cell neighbour
// pattern covers the full disc with a little overflow.
func GeohashPrefixForRadiusKm(km int) int {
	switch {
	case km <= 0:
		return 0
	case km <= 1:
		return 6
	case km <= 5:
		return 5
	case km <= 50:
		return 4
	case km <= 200:
		return 3
	case km <= 1500:
		return 2
	default:
		return 1
	}
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

// --- Lane D7: snapped locations and distance buckets ------------------------

// LocationGridCellsPerDegree snaps every stored dating location to a 0.01
// degree grid (~1.1 km of latitude). Same pattern as food-service's
// RoundDropCoordinate, which uses 0.005 degrees for a rider's drop area.
const LocationGridCellsPerDegree = 100

// LocationGeohashPrecision is the stored location_geohash length. It is
// derived from the snapped point, so it carries nothing the snapped point
// does not.
const LocationGeohashPrecision = 7

// SnapCoordinate is v on the 0.01 degree grid, rounding half up (towards
// +Inf): 0.125 -> 0.13, -0.125 -> -0.12. The float64() conversion forbids a
// fused multiply-add, so this is the same IEEE arithmetic as
// dating_snap_coordinate in database/setup.sql. Idempotent.
func SnapCoordinate(v float64) float64 {
	return math.Floor(float64(v*LocationGridCellsPerDegree)+0.5) / LocationGridCellsPerDegree
}

// Distance bucket codes. Every response that conveys a distance carries one
// of these with its label, never a number.
const (
	DistanceBucketUnder5 = "lt_5_km"
	DistanceBucket5To10  = "km_5_10"
	DistanceBucket10To25 = "km_10_25"
	DistanceBucketOver25 = "gt_25_km"
)

// DistanceBand is a distance bucket code and its display label.
type DistanceBand struct {
	Code  string
	Label string
}

// DistanceBucketFor maps a km distance onto the buckets. Lower edges are
// inclusive: 4.9 -> lt_5_km, 5.0 -> km_5_10, 10.0 -> km_10_25,
// 25.0 -> gt_25_km. NaN or a negative value (unreachable from haversine)
// falls in lt_5_km rather than an empty label.
func DistanceBucketFor(km float64) DistanceBand {
	switch {
	case !(km >= 5):
		return DistanceBand{Code: DistanceBucketUnder5, Label: "< 5 km"}
	case km < 10:
		return DistanceBand{Code: DistanceBucket5To10, Label: "5–10 km"}
	case km < 25:
		return DistanceBand{Code: DistanceBucket10To25, Label: "10–25 km"}
	default:
		return DistanceBand{Code: DistanceBucketOver25, Label: "25+ km"}
	}
}

// SnappedDistanceKm is the haversine distance between the two points after
// snapping each to the grid. Stored points are already snapped; snapping again
// is a no-op that keeps any caller from measuring a raw point.
func SnappedDistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	return DistanceKm(SnapCoordinate(lat1), SnapCoordinate(lon1), SnapCoordinate(lat2), SnapCoordinate(lon2))
}

// DistanceBandBetween is the bucket between two optional points; ok is false
// when either side has no location.
func DistanceBandBetween(lat1, lon1, lat2, lon2 *float64) (DistanceBand, bool) {
	if lat1 == nil || lon1 == nil || lat2 == nil || lon2 == nil {
		return DistanceBand{}, false
	}
	return DistanceBucketFor(SnappedDistanceKm(*lat1, *lon1, *lat2, *lon2)), true
}

// DistanceBucketRepresentativeKm is the single km value ranking uses for a
// bucket, so a card's score changes only when its bucket does.
func DistanceBucketRepresentativeKm(code string) float64 {
	switch code {
	case DistanceBucketUnder5:
		return 2.5
	case DistanceBucket5To10:
		return 7.5
	case DistanceBucket10To25:
		return 17.5
	default:
		return 37.5
	}
}

// discoveryRadiusStepsKm are the radii the deck's distance filter applies.
// The first three are the bucket edges.
var discoveryRadiusStepsKm = []int{5, 10, 25, 50, 100, 250, 500}

// EffectiveDiscoveryRadiusKm rounds a preferred radius up to the next step, so
// stepping the distance preference 1 km at a time and watching who enters the
// deck cannot measure a candidate more finely than a step. 0 (no radius) stays
// 0; a value past the last step is returned unchanged (preferences cap at 500).
func EffectiveDiscoveryRadiusKm(km int) int {
	if km <= 0 {
		return 0
	}
	for _, step := range discoveryRadiusStepsKm {
		if km <= step {
			return step
		}
	}
	return km
}
