package routing

import (
	"context"
	"math"
	"time"

	"github.com/atpost/food-service/internal/geo"
)

// Haversine defaults. They reproduce lane 0c's estimate exactly: the
// great-circle distance ridden at 20 km/h. 20 km/h over the straight line is
// about 15 km/h along a road 1.35 times longer, which sits inside the range of
// urban two-wheeler averages in Indian traffic, so the combined number is not
// wrong; WindingFactor exists so it can be split honestly once Google answers
// are available to calibrate against (a factor of 1.35 with 27 km/h gives the
// same ride times with road-like distances).
const (
	DefaultSpeedKmh      = 20.0
	DefaultWindingFactor = 1.0
)

// Haversine estimates a ride from the great-circle distance, stretched by
// WindingFactor to approximate the road, ridden at SpeedKmh. It never calls
// out and never fails on valid coordinates.
type Haversine struct {
	// SpeedKmh is the average two-wheeler speed along the (wound) route.
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
	km := geo.HaversineKM(from.Lat, from.Lng, to.Lat, to.Lng) * winding
	return Route{
		DistanceMeters: int(math.Round(km * 1000)),
		Duration:       time.Duration(km / speed * float64(time.Hour)),
		Source:         SourceHaversine,
	}, nil
}
