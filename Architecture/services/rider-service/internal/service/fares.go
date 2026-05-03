package service

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// FareEstimateRequest is the input for EstimateFare.
type FareEstimateRequest struct {
	PickupLat       float64
	PickupLng       float64
	DropLat         float64
	DropLng         float64
	VehicleType     string
	CityID          uuid.UUID
	SurgeMultiplier float64 // optional; defaults to 1.0
}

// FareEstimateResult mirrors the API response shape.
type FareEstimateResult struct {
	EstimatedDistanceKM   float64 `json:"estimated_distance_km"`
	EstimatedDurationMin  float64 `json:"estimated_duration_min"`
	FareEstimatePaise     int64   `json:"fare_estimate_paise"`
	SurgeMultiplier       float64 `json:"surge_multiplier"`
	VehicleType           string  `json:"vehicle_type"`
	ETAToPickupSeconds    int     `json:"eta_to_pickup_seconds"`
	BaseFareINR           float64 `json:"base_fare_inr"`
	PerKMINR              float64 `json:"per_km_inr"`
	PerMinuteINR          float64 `json:"per_minute_inr"`
	MinimumFareINR        float64 `json:"minimum_fare_inr"`
	FareEstimateINR       float64 `json:"fare_estimate_inr"`
}

// EstimateFare computes a fare estimate for the given route.
//
// SPRINT 1 STAND-IN: real Google Maps / Mappls integration lands in S2. v1
// uses straight-line haversine distance × cfg.WindingFactor as a road-distance
// stand-in, and a configured average speed to derive duration. The result is
// good enough for the customer-facing "approximate ₹85" string but is NOT a
// commitment — the final fare is computed from the actual ride telemetry.
func (s *Service) EstimateFare(ctx context.Context, req FareEstimateRequest) (*FareEstimateResult, error) {
	if !allowedVehicleTypes[req.VehicleType] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car")
	}
	if req.CityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	if !validLatLng(req.PickupLat, req.PickupLng) || !validLatLng(req.DropLat, req.DropLng) {
		return nil, fmt.Errorf("invalid: pickup and drop coordinates must be valid")
	}
	rule, err := s.store.GetFareRule(ctx, req.CityID, req.VehicleType)
	if err != nil {
		if errors.Is(err, store.ErrFareRuleNotFound) {
			return nil, fmt.Errorf("not_found: no fare rule for this city + vehicle type")
		}
		return nil, err
	}
	straightKM := haversineKM(req.PickupLat, req.PickupLng, req.DropLat, req.DropLng)
	distanceKM := straightKM * s.cfg.WindingFactor
	speed := s.cfg.AverageSpeedKMPH
	if speed <= 0 {
		speed = 22.0
	}
	durationMin := (distanceKM / speed) * 60.0
	surge := req.SurgeMultiplier
	if surge <= 0 {
		surge = 1.0
	}
	rawINR := rule.BaseFare + (rule.PerKMFare * distanceKM) + (rule.PerMinuteFare * durationMin)
	if rawINR < rule.MinimumFare {
		rawINR = rule.MinimumFare
	}
	rawINR = rawINR * surge
	// ETA stand-in: assume partner is 5 minutes from the pickup at base. S2
	// replaces this with the real partner-locations geo query.
	etaSeconds := 300
	return &FareEstimateResult{
		EstimatedDistanceKM:  round2(distanceKM),
		EstimatedDurationMin: round2(durationMin),
		FareEstimatePaise:    int64(math.Round(rawINR * 100)),
		SurgeMultiplier:      surge,
		VehicleType:          req.VehicleType,
		ETAToPickupSeconds:   etaSeconds,
		BaseFareINR:          rule.BaseFare,
		PerKMINR:             rule.PerKMFare,
		PerMinuteINR:         rule.PerMinuteFare,
		MinimumFareINR:       rule.MinimumFare,
		FareEstimateINR:      round2(rawINR),
	}, nil
}

// haversineKM computes the great-circle distance in km between two
// (lat, lng) pairs given in decimal degrees.
func haversineKM(lat1, lng1, lat2, lng2 float64) float64 {
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

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func validLatLng(lat, lng float64) bool {
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && !(lat == 0 && lng == 0)
}
