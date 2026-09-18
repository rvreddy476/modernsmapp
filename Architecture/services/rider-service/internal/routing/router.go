// Package routing answers one question for rider-service: how far, and how
// long, is a two-wheeler / auto ride from pickup to drop.
//
// The chain main.go builds is Cache(Fallback(GoogleRoutes, Haversine)), the
// same shape as food-service/internal/routing:
//
//   - GoogleRoutes calls the Routes API computeRoutes (TWO_WHEELER,
//     TRAFFIC_AWARE) with the server key in a header, under a hard timeout;
//   - Fallback answers from Haversine whenever Google is not configured or
//     fails in any way, logs each failure class once and counts every
//     failure, and never fails the caller;
//   - Cache keeps Google answers in Redis for five minutes, keyed on both
//     endpoints rounded to about 50 m plus a five-minute time bucket.
//
// The service consumes the Calculator interface (CalculateRoute returning a
// RouteResult); CalculatorFromRouter adapts the chain, and
// DeterministicCalculator stays for tests and offline development.
package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrInvalidCoordinates is returned when input coordinates are invalid.
	ErrInvalidCoordinates = errors.New("routing: invalid coordinates")
)

// RouteResult captures the authoritative distance and duration from the routing provider.
type RouteResult struct {
	DistanceMeters  int     `json:"distance_meters"`
	DurationSeconds int     `json:"duration_seconds"`
	DistanceKM      float64 `json:"distance_km"`
	DurationMin     float64 `json:"duration_min"`
	ProviderVersion string  `json:"provider_version"`
}

// Calculator defines the routing abstraction the service consumes.
type Calculator interface {
	CalculateRoute(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) (*RouteResult, error)
	ProviderVersion() string
}

// LatLng is a WGS84 coordinate in degrees.
type LatLng struct {
	Lat float64
	Lng float64
}

// Valid reports whether the point is a finite coordinate on the globe.
// (0, 0) is refused as the null-island sentinel, as validLatLng always has.
func (p LatLng) Valid() bool {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lng) || math.IsInf(p.Lat, 0) || math.IsInf(p.Lng, 0) {
		return false
	}
	if p.Lat == 0 && p.Lng == 0 {
		return false
	}
	return p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// Route sources.
const (
	SourceGoogle    = "google"
	SourceHaversine = "haversine"
)

// Provider versions recorded on a quote (route_version).
const (
	VersionGoogle        = "google-routes-v2"
	VersionHaversine     = "haversine-v1"
	VersionDeterministic = "deterministic-v1"
)

// Route is one leg's distance and ride time.
type Route struct {
	DistanceMeters int
	Duration       time.Duration
	Source         string
}

// Router computes a two-wheeler route between two points.
type Router interface {
	Route(ctx context.Context, from, to LatLng) (Route, error)
}

// Failure classes a Router error is counted and logged under.
const (
	FailureTimeout    = "timeout"     // the routing timeout fired
	FailureCanceled   = "canceled"    // the caller's context ended first
	FailureTransport  = "transport"   // connection refused, DNS, TLS, reset
	FailureHTTPStatus = "http_status" // a non-2xx answer
	FailureDecode     = "decode"      // a 2xx body that is not the documented shape
	FailureEmptyRoute = "empty_route" // a 2xx body with no usable route
	FailureRequest    = "request"     // the request could not be built (bad coordinates)
	FailureUnknown    = "unknown"
)

// Error is a classified routing failure. Its message never carries the API
// key: GoogleRoutes redacts it before an Error is built.
type Error struct {
	Class      string
	StatusCode int
	msg        string
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("routing %s (HTTP %d): %s", e.Class, e.StatusCode, e.msg)
	}
	return fmt.Sprintf("routing %s: %s", e.Class, e.msg)
}

// ClassOf returns the failure class of err, FailureUnknown when it carries none.
func ClassOf(err error) string {
	var re *Error
	if errors.As(err, &re) && re.Class != "" {
		return re.Class
	}
	return FailureUnknown
}

// --- Calculator adapters --------------------------------------------------

// routerCalculator adapts a Router chain to the Calculator the service uses.
type routerCalculator struct {
	r Router
}

// CalculatorFromRouter wraps a Router (usually Cache(Fallback(Google,
// Haversine))) as a Calculator. The RouteResult's ProviderVersion says which
// source answered.
func CalculatorFromRouter(r Router) Calculator { return &routerCalculator{r: r} }

func (c *routerCalculator) ProviderVersion() string { return VersionGoogle + "|" + VersionHaversine }

func (c *routerCalculator) CalculateRoute(ctx context.Context, pLat, pLng, dLat, dLng float64) (*RouteResult, error) {
	from, to := LatLng{pLat, pLng}, LatLng{dLat, dLng}
	if !from.Valid() || !to.Valid() {
		return nil, ErrInvalidCoordinates
	}
	r, err := c.r.Route(ctx, from, to)
	if err != nil {
		var re *Error
		if errors.As(err, &re) && re.Class == FailureRequest {
			return nil, ErrInvalidCoordinates
		}
		return nil, err
	}
	version := VersionHaversine
	if r.Source == SourceGoogle {
		version = VersionGoogle
	}
	secs := int(math.Round(r.Duration.Seconds()))
	return &RouteResult{
		DistanceMeters:  r.DistanceMeters,
		DurationSeconds: secs,
		DistanceKM:      float64(r.DistanceMeters) / 1000.0,
		DurationMin:     float64(secs) / 60.0,
		ProviderVersion: version,
	}, nil
}

// DeterministicCalculator is the test/dev router computing deterministic distances.
type DeterministicCalculator struct {
	WindingFactor float64
	AverageSpeed  float64
	Version       string
}

// NewDeterministicCalculator returns a deterministic calculator for tests and local development.
func NewDeterministicCalculator(winding, speed float64) *DeterministicCalculator {
	if winding <= 0 {
		winding = 1.25
	}
	if speed <= 0 {
		speed = 22.0
	}
	return &DeterministicCalculator{
		WindingFactor: winding,
		AverageSpeed:  speed,
		Version:       VersionDeterministic,
	}
}

func (d *DeterministicCalculator) ProviderVersion() string {
	if d.Version == "" {
		return VersionDeterministic
	}
	return d.Version
}

// CalculateRoute computes distance and duration using Haversine with winding and speed.
func (d *DeterministicCalculator) CalculateRoute(_ context.Context, pLat, pLng, dLat, dLng float64) (*RouteResult, error) {
	if !validLatLng(pLat, pLng) || !validLatLng(dLat, dLng) {
		return nil, ErrInvalidCoordinates
	}

	straightKM := haversineKM(pLat, pLng, dLat, dLng)
	distKM := straightKM * d.WindingFactor
	durMin := (distKM / d.AverageSpeed) * 60.0

	return &RouteResult{
		DistanceMeters:  int(math.Round(distKM * 1000.0)),
		DurationSeconds: int(math.Round(durMin * 60.0)),
		DistanceKM:      distKM,
		DurationMin:     durMin,
		ProviderVersion: d.ProviderVersion(),
	}, nil
}

func validLatLng(lat, lng float64) bool {
	return LatLng{lat, lng}.Valid()
}

func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371.0
	dLat := (lat2 - lat1) * (math.Pi / 180.0)
	dLon := (lon2 - lon1) * (math.Pi / 180.0)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*(math.Pi/180.0))*math.Cos(lat2*(math.Pi/180.0))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKM * c
}
