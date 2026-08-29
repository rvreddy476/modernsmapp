package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

var pilotVehicleTypes = map[string]bool{
	"bike": true,
	"auto": true,
}

// FareEstimateRequest is the input for EstimateFare.
type FareEstimateRequest struct {
	CustomerUserID  *uuid.UUID
	PickupLat       float64
	PickupLng       float64
	PickupLabel     string
	PickupPlaceID   string
	DropLat         float64
	DropLng         float64
	DropLabel       string
	DropPlaceID     string
	VehicleType     string // optional; if empty estimates all pilot vehicle types (bike, auto)
	CityID          uuid.UUID
	SurgeMultiplier float64 // internal system use only
}

// FareEstimateResult mirrors the API response shape.
type FareEstimateResult struct {
	QuoteID              string              `json:"quote_id"`
	EstimatedDistanceKM  float64             `json:"estimated_distance_km"`
	EstimatedDurationMin float64             `json:"estimated_duration_min"`
	FareEstimatePaise    int64               `json:"fare_estimate_paise"`
	SurgeMultiplier      float64             `json:"surge_multiplier"`
	VehicleType          string              `json:"vehicle_type"`
	ETAToPickupSeconds   int                 `json:"eta_to_pickup_seconds"`
	BaseFareINR          float64             `json:"base_fare_inr"`
	PerKMINR             float64             `json:"per_km_inr"`
	PerMinuteINR         float64             `json:"per_minute_inr"`
	MinimumFareINR       float64             `json:"minimum_fare_inr"`
	FareEstimateINR      float64             `json:"fare_estimate_inr"`
	Options              []store.QuoteOption `json:"options"`
	ExpiresAt            time.Time           `json:"expires_at"`
}

// EstimateFare computes authoritative pricing, creates an immutable quote snapshot, and returns options.
func (s *Service) EstimateFare(ctx context.Context, req FareEstimateRequest) (*FareEstimateResult, error) {
	if req.CityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	if !validLatLng(req.PickupLat, req.PickupLng) || !validLatLng(req.DropLat, req.DropLng) {
		return nil, fmt.Errorf("invalid: pickup and drop coordinates must be valid")
	}

	// 1. Dual-point geofence check: prove both pickup and drop reside within active serviceable city zone
	pickupCity, err := s.store.FindServiceableCity(ctx, req.PickupLat, req.PickupLng)
	if err != nil || pickupCity == nil || pickupCity.ID != req.CityID {
		return nil, fmt.Errorf("invalid: pickup location is outside serviceable city area")
	}
	dropCity, err := s.store.FindServiceableCity(ctx, req.DropLat, req.DropLng)
	if err != nil || dropCity == nil || dropCity.ID != req.CityID {
		return nil, fmt.Errorf("invalid: drop location is outside serviceable city area")
	}

	// 2. Route calculation through router provider abstraction
	routeRes, err := s.router.CalculateRoute(ctx, req.PickupLat, req.PickupLng, req.DropLat, req.DropLng)
	if err != nil {
		return nil, fmt.Errorf("calculate route: %w", err)
	}

	// 3. Controlled pilot vehicle types: bike and auto only
	typesToQuote := []string{"bike", "auto"}
	if req.VehicleType != "" {
		if !pilotVehicleTypes[req.VehicleType] {
			return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto for pilot")
		}
		typesToQuote = []string{req.VehicleType}
	}

	var options []store.QuoteOption
	var primaryOption *store.QuoteOption
	var primaryRule *store.FareRule

	for _, vt := range typesToQuote {
		rule, err := s.store.GetFareRule(ctx, req.CityID, vt)
		if err != nil {
			continue
		}
		if primaryRule == nil {
			primaryRule = rule
		}

		// Calculate surge multiplier server-side only
		surge := math.Max(rule.NightMultiplier, rule.PeakMultiplier)
		if surge <= 0 {
			surge = 1.0
		}

		basePaise := int64(math.Round(rule.BaseFare * 100))
		perKMPaise := int64(math.Round(rule.PerKMFare * 100))
		perMinPaise := int64(math.Round(rule.PerMinuteFare * 100))
		platformPaise := int64(math.Round(rule.PlatformFee * 100))
		minPaise := int64(math.Round(rule.MinimumFare * 100))

		distPaise := (int64(routeRes.DistanceMeters) * perKMPaise) / 1000
		timePaise := (int64(routeRes.DurationSeconds) * perMinPaise) / 60
		rawPaise := basePaise + distPaise + timePaise + platformPaise
		if rawPaise < minPaise {
			rawPaise = minPaise
		}

		surgeMult := math.Max(rule.NightMultiplier, rule.PeakMultiplier)
		var surgeBPS int64 = 0
		if surgeMult > 1.0 {
			surgeBPS = int64(math.Round((surgeMult - 1.0) * 10000))
		}
		surgePaise := (rawPaise * surgeBPS) / 10000
		totalPaise := rawPaise + surgePaise
		taxPaise := (totalPaise * 500) / 10000 // 5% GST in BPS

		opt := store.QuoteOption{
			VehicleType:      vt,
			Available:        true,
			PickupETASeconds: 300,
			DistanceMeters:   routeRes.DistanceMeters,
			DurationSeconds:  routeRes.DurationSeconds,
			Currency:         "INR",
			TotalPaise:       totalPaise + taxPaise,
			Breakdown: store.QuoteBreakdownPaise{
				BasePaise:        basePaise,
				DistancePaise:    distPaise,
				TimePaise:        timePaise,
				PlatformFeePaise: platformPaise,
				TaxPaise:         taxPaise,
				TollPaise:        0,
				SurgeBasisPoints: surgeBPS,
			},
		}
		options = append(options, opt)
		if primaryOption == nil || vt == req.VehicleType {
			optCopy := opt
			primaryOption = &optCopy
			primaryRule = rule
		}
	}

	if len(options) == 0 {
		return nil, fmt.Errorf("not_found: no fare rules available for city")
	}

	// 4. Compute canonical request fingerprint covering all bound authority fields
	custIDStr := ""
	if req.CustomerUserID != nil {
		custIDStr = req.CustomerUserID.String()
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)

	canonicalQuote := struct {
		CustomerID   string              `json:"customer_id"`
		CityID       string              `json:"city_id"`
		PickupLat    float64             `json:"pickup_lat"`
		PickupLng    float64             `json:"pickup_lng"`
		PickupLabel  string              `json:"pickup_label"`
		PickupPlace  string              `json:"pickup_place_id"`
		DropLat      float64             `json:"drop_lat"`
		DropLng      float64             `json:"drop_lng"`
		DropLabel    string              `json:"drop_label"`
		DropPlace    string              `json:"drop_place_id"`
		RouteVersion string              `json:"route_version"`
		DistMeters   int                 `json:"dist_meters"`
		DurSeconds   int                 `json:"dur_seconds"`
		Options      []store.QuoteOption `json:"options"`
		ExpiresAt    int64               `json:"expires_at"`
	}{
		CustomerID:   custIDStr,
		CityID:       req.CityID.String(),
		PickupLat:    req.PickupLat,
		PickupLng:    req.PickupLng,
		PickupLabel:  req.PickupLabel,
		PickupPlace:  req.PickupPlaceID,
		DropLat:      req.DropLat,
		DropLng:      req.DropLng,
		DropLabel:    req.DropLabel,
		DropPlace:    req.DropPlaceID,
		RouteVersion: routeRes.ProviderVersion,
		DistMeters:   routeRes.DistanceMeters,
		DurSeconds:   routeRes.DurationSeconds,
		Options:      options,
		ExpiresAt:    expiresAt.Unix(),
	}
	canonBytes, _ := json.Marshal(canonicalQuote)
	h := sha256.Sum256(canonBytes)
	reqHash := hex.EncodeToString(h[:])
	cityIDPtr := &req.CityID

	snapshot, err := s.store.CreateQuoteSnapshot(ctx, store.CreateQuoteInput{
		CustomerUserID:    req.CustomerUserID,
		CityID:            cityIDPtr,
		PickupLat:         req.PickupLat,
		PickupLng:         req.PickupLng,
		PickupLabel:       req.PickupLabel,
		PickupPlaceID:     req.PickupPlaceID,
		DropLat:           req.DropLat,
		DropLng:           req.DropLng,
		DropLabel:         req.DropLabel,
		DropPlaceID:       req.DropPlaceID,
		RouteVersion:      routeRes.ProviderVersion,
		FarePolicyVersion: 1,
		DistanceMeters:    routeRes.DistanceMeters,
		DurationSeconds:   routeRes.DurationSeconds,
		Options:           options,
		RequestHash:       reqHash,
		ExpiresAt:         expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create quote snapshot: %w", err)
	}

	if primaryOption == nil {
		primaryOption = &options[0]
	}

	baseINR := 0.0
	perKMINR := 0.0
	perMinINR := 0.0
	minFareINR := 0.0
	if primaryRule != nil {
		baseINR = primaryRule.BaseFare
		perKMINR = primaryRule.PerKMFare
		perMinINR = primaryRule.PerMinuteFare
		minFareINR = primaryRule.MinimumFare
	}

	return &FareEstimateResult{
		QuoteID:              snapshot.ID.String(),
		EstimatedDistanceKM:  round2(routeRes.DistanceKM),
		EstimatedDurationMin: round2(routeRes.DurationMin),
		FareEstimatePaise:    primaryOption.TotalPaise,
		SurgeMultiplier:      1.0 + float64(primaryOption.Breakdown.SurgeBasisPoints)/10000.0,
		VehicleType:          primaryOption.VehicleType,
		ETAToPickupSeconds:   primaryOption.PickupETASeconds,
		BaseFareINR:          baseINR,
		PerKMINR:             perKMINR,
		PerMinuteINR:         perMinINR,
		MinimumFareINR:       minFareINR,
		FareEstimateINR:      float64(primaryOption.TotalPaise) / 100.0,
		Options:              options,
		ExpiresAt:            expiresAt,
	}, nil
}

// GetQuote fetches a quote snapshot by ID.
func (s *Service) GetQuote(ctx context.Context, quoteID uuid.UUID) (*store.QuoteSnapshot, error) {
	if quoteID == uuid.Nil {
		return nil, fmt.Errorf("invalid: quote_id required")
	}
	return s.store.GetQuoteSnapshot(ctx, quoteID)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func validLatLng(lat, lng float64) bool {
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && !(lat == 0 && lng == 0)
}
