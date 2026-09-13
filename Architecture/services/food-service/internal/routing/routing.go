// Package routing answers one question for food-service: how far, and how
// long, is a two-wheeler ride from A to B.
//
// The chain main.go builds is Cache(Fallback(GoogleRoutes, Haversine)):
//
//   - GoogleRoutes calls the Routes API computeRoutes (TWO_WHEELER,
//     TRAFFIC_AWARE) with the server key in a header, under a hard timeout;
//   - Fallback answers from Haversine whenever Google is not configured or
//     fails in any way, logs each failure class once and counts every failure,
//     and never fails the caller;
//   - Cache keeps Google answers in Redis for five minutes, keyed on both
//     endpoints rounded to about 50 m plus a five-minute time bucket.
//
// Every Route says where it came from (Source), so an ETA built from it can
// tell the customer whether traffic was considered.
package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// LatLng is a WGS84 coordinate in degrees.
type LatLng struct {
	Lat float64
	Lng float64
}

// Valid reports whether the point is a finite coordinate on the globe.
func (p LatLng) Valid() bool {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lng) || math.IsInf(p.Lat, 0) || math.IsInf(p.Lng, 0) {
		return false
	}
	return p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// Route sources.
const (
	SourceGoogle    = "google"
	SourceHaversine = "haversine"
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
