package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var pilotVehicleTypes = map[string]bool{
	"bike": true,
	"auto": true,
}

// FareEstimateRequest is the input for EstimateFare.
type FareEstimateRequest struct {
	CustomerUserID *uuid.UUID
	PickupLat      float64
	PickupLng      float64
	PickupLabel    string
	PickupPlaceID  string
	DropLat        float64
	DropLng        float64
	DropLabel      string
	DropPlaceID    string
	VehicleType    string // optional; if empty estimates all pilot vehicle types (bike, auto)
	CityID         uuid.UUID
	// CouponCode is validated (typed CouponError on failure) and priced into
	// every option the coupon covers, then locked in the quote snapshot.
	CouponCode string
}

// FareEstimateResult mirrors the API response shape. The legacy INR floats
// are derived from the paise and kept for older clients.
type FareEstimateResult struct {
	QuoteID              string              `json:"quote_id"`
	EstimatedDistanceKM  float64             `json:"estimated_distance_km"`
	EstimatedDurationMin float64             `json:"estimated_duration_min"`
	FareEstimatePaise    int64               `json:"fare_estimate_paise"`
	SurgeMultiplier      float64             `json:"surge_multiplier"`
	SurgeBPS             int64               `json:"surge_bps"`
	SurgeReason          string              `json:"surge_reason"`
	WindowName           string              `json:"window_name,omitempty"`
	DiscountPaise        int64               `json:"discount_paise"`
	CouponCode           string              `json:"coupon_code,omitempty"`
	OutstandingPaise     int64               `json:"outstanding_paise"`
	TaxNote              string              `json:"tax_note"`
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
	city := pickupCity

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

	// 4. Coupon (typed errors), outstanding fees, tax and the quote instant.
	now := s.now()
	var coupon *store.Coupon
	var couponRule *pricing.Coupon
	if req.CouponCode != "" {
		coupon, couponRule, err = s.ValidateCoupon(ctx, req.CouponCode, req.CustomerUserID, &req.CityID, req.VehicleType)
		if err != nil {
			return nil, err
		}
	}
	var outstanding []store.CustomerOutstanding
	if req.CustomerUserID != nil {
		outstanding, err = s.store.ListPendingOutstanding(ctx, *req.CustomerUserID)
		if err != nil {
			return nil, fmt.Errorf("list outstanding: %w", err)
		}
	}
	var outstandingPaise int64
	var outstandingIDs []string
	for _, o := range outstanding {
		outstandingPaise += o.AmountPaise
		outstandingIDs = append(outstandingIDs, o.ID.String())
	}
	tax := s.taxFor(city)

	var options []store.QuoteOption
	var primaryOption *store.QuoteOption
	var primaryRule *store.FareRule
	couponCovered, couponUnderMin := 0, 0

	for _, vt := range typesToQuote {
		rule, err := s.store.GetFareRule(ctx, req.CityID, vt)
		if err != nil {
			continue
		}
		if primaryRule == nil {
			primaryRule = rule
		}

		surgeBPS, surgeReason, windowName := s.surgeFor(ctx, city, vt, now)

		var optCoupon *pricing.Coupon
		if couponRule != nil && couponCoversVehicle(coupon, vt) {
			optCoupon = couponRule
		}
		b, err := pricing.Compute(pricing.Input{
			Rule:             rule.PricingRule(),
			DistanceMeters:   routeRes.DistanceMeters,
			DurationSeconds:  routeRes.DurationSeconds,
			SurgeBPS:         surgeBPS,
			SurgeReason:      surgeReason,
			WindowName:       windowName,
			Coupon:           optCoupon,
			OutstandingPaise: outstandingPaise,
			OutstandingIDs:   outstandingIDs,
			Tax:              tax,
			InvoiceDate:      now,
		})
		if err != nil {
			return nil, fmt.Errorf("price %s: %w", vt, err)
		}
		if optCoupon != nil {
			couponCovered++
			if b.DiscountPaise == 0 && !optCoupon.MeetsMinFare(b.RideFarePaise) {
				couponUnderMin++
				b.CouponCode, b.CouponID = "", ""
			}
		}

		opt := store.QuoteOption{
			VehicleType:      vt,
			Available:        true,
			PickupETASeconds: 300,
			DistanceMeters:   routeRes.DistanceMeters,
			DurationSeconds:  routeRes.DurationSeconds,
			Currency:         "INR",
			TotalPaise:       b.TotalPaise,
			SurgeBPS:         b.SurgeBasisPoints,
			SurgeReason:      b.SurgeReason,
			WindowName:       b.WindowName,
			DiscountPaise:    b.DiscountPaise,
			CouponCode:       b.CouponCode,
			Breakdown:        b,
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
	if couponRule != nil && couponCovered > 0 && couponUnderMin == couponCovered {
		return nil, &CouponError{Code: CouponCodeMinFare, Message: fmt.Sprintf("the fare must be at least Rs %d for %s", couponRule.MinFarePaise/100, coupon.Code)}
	}

	// 5. Compute canonical request fingerprint covering all bound authority fields
	custIDStr := ""
	if req.CustomerUserID != nil {
		custIDStr = req.CustomerUserID.String()
	}
	expiresAt := now.Add(5 * time.Minute)

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
		CouponCode   string              `json:"coupon_code"`
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
		CouponCode:   store.NormalizeCouponCode(req.CouponCode),
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
		FarePolicyVersion: pricing.FarePolicyVersion,
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
		SurgeMultiplier:      1.0 + float64(primaryOption.SurgeBPS)/10000.0,
		SurgeBPS:             primaryOption.SurgeBPS,
		SurgeReason:          primaryOption.SurgeReason,
		WindowName:           primaryOption.WindowName,
		DiscountPaise:        primaryOption.DiscountPaise,
		CouponCode:           primaryOption.CouponCode,
		OutstandingPaise:     outstandingPaise,
		TaxNote:              pricing.TaxNote,
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

// taxFor returns the tax computer for a city: the GST computer re-pointed at
// the city's state as place of supply when the state is known, otherwise
// the configured default.
func (s *Service) taxFor(city *store.City) pricing.TaxComputer {
	g, ok := s.tax.(*pricing.GSTComputer)
	if !ok || city == nil || city.State == nil {
		return s.tax
	}
	code, found := pricing.StateCodeForName(*city.State)
	if !found {
		return s.tax
	}
	if local, err := g.WithPlaceOfSupply(code); err == nil {
		return local
	}
	return s.tax
}

// surgeFor combines the city's fare window at the local quote time with the
// cached demand step: max of the two, never the sum.
func (s *Service) surgeFor(ctx context.Context, city *store.City, vehicleType string, now time.Time) (bps int64, reason, windowName string) {
	var window *pricing.Window
	rows, err := s.store.ListFareWindows(ctx, city.ID, vehicleType)
	if err != nil {
		slog.Warn("rider: list fare windows failed; quoting without a window", "city_id", city.ID, "error", err)
	} else {
		window = pricing.SelectWindow(store.PricingWindows(rows), vehicleType, pricing.LocalTime(now, city.Timezone))
	}
	return pricing.EffectiveSurge(window, s.demandBPS(ctx, city.ID, vehicleType))
}

// surgeCacheTTL is how long a demand step is reused per (city, vehicle type).
const surgeCacheTTL = 60 * time.Second

func surgeCacheKey(cityID uuid.UUID, vehicleType string) string {
	return "rider:surge:v1:" + cityID.String() + ":" + vehicleType
}

// demandBPS is the demand surge step for a (city, vehicle type), cached in
// Redis for 60 s when a client is set. Any failure prices as no demand
// surge: a Redis or Postgres hiccup must not inflate a fare.
func (s *Service) demandBPS(ctx context.Context, cityID uuid.UUID, vehicleType string) int64 {
	key := surgeCacheKey(cityID, vehicleType)
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, key).Result(); err == nil {
			if v, perr := strconv.ParseInt(raw, 10, 64); perr == nil {
				return v
			}
		} else if !errors.Is(err, redis.Nil) {
			slog.Debug("rider: surge cache read failed", "error", err)
		}
	}
	requested, online, err := s.store.DemandCounts(ctx, cityID, vehicleType)
	if err != nil {
		slog.Warn("rider: demand counts failed; no demand surge", "city_id", cityID, "vehicle_type", vehicleType, "error", err)
		return 0
	}
	bps := pricing.DemandBPS(requested, online, s.cfg.SurgeCapBPS)
	if s.rdb != nil {
		if err := s.rdb.Set(ctx, key, strconv.FormatInt(bps, 10), surgeCacheTTL).Err(); err != nil {
			slog.Debug("rider: surge cache write failed", "error", err)
		}
	}
	return bps
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
