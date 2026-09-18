package routing

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"
)

// Haversine defaults reproduce the DeterministicCalculator's estimate: the
// great-circle distance stretched by 1.25 for the road, ridden at 22 km/h,
// the Indian-city two-wheeler averages service.Config documents.
const (
	DefaultSpeedKmh      = 22.0
	DefaultWindingFactor = 1.25
)

// Haversine estimates a ride from the great-circle distance, stretched by
// WindingFactor to approximate the road, ridden at SpeedKmh. It never calls
// out and never fails on valid coordinates.
type Haversine struct {
	// SpeedKmh is the average speed along the (wound) route.
	SpeedKmh float64
	// WindingFactor is road distance over straight-line distance, >= 1.
	WindingFactor float64
}

// Route implements Router.
func (h Haversine) Route(_ context.Context, from, to LatLng) (Route, error) {
	if !from.Valid() || !to.Valid() {
		return Route{}, &Error{Class: FailureRequest, msg: "coordinates out of range"}
	}
	speed := h.SpeedKmh
	if speed <= 0 {
		speed = DefaultSpeedKmh
	}
	winding := h.WindingFactor
	if winding < 1 {
		winding = DefaultWindingFactor
	}
	km := haversineKM(from.Lat, from.Lng, to.Lat, to.Lng) * winding
	return Route{
		DistanceMeters: int(math.Round(km * 1000)),
		Duration:       time.Duration(km / speed * float64(time.Hour)),
		Source:         SourceHaversine,
	}, nil
}

// Config is the routing environment.
//
//   - GOOGLE_MAPS_SERVER_KEY: optional. Set, quotes come from the Routes API
//     (TWO_WHEELER, TRAFFIC_AWARE) with haversine as the fallback; empty,
//     haversine only. A server key: restrict it to the Routes API and the
//     service's egress IPs, never ship it to a client.
//   - MOPEDU_ROUTING_TIMEOUT_MS: optional, default 2000. One computeRoutes
//     call end to end; a positive integer no larger than 10000.
type Config struct {
	GoogleKey string
	Timeout   time.Duration
}

// maxTimeout keeps a typo from parking a quote for a minute.
const maxTimeout = 10 * time.Second

// ConfigFromEnv reads the routing environment. An invalid timeout is an error
// so a typo stops startup rather than silently disabling the bound.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{GoogleKey: getenv("GOOGLE_MAPS_SERVER_KEY"), Timeout: DefaultTimeout}
	if raw := getenv("MOPEDU_ROUTING_TIMEOUT_MS"); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms <= 0 || time.Duration(ms)*time.Millisecond > maxTimeout {
			return cfg, fmt.Errorf("MOPEDU_ROUTING_TIMEOUT_MS must be a whole number of milliseconds between 1 and %d", maxTimeout.Milliseconds())
		}
		cfg.Timeout = time.Duration(ms) * time.Millisecond
	}
	return cfg, nil
}
