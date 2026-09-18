// Cities, zones, and fare-rule CRUD for the admin surface (Sprint 3 §17).
package service

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// CreateCityRequest is the input shape for CreateCity.
type CreateCityRequest struct {
	Name         string
	State        string
	Country      string
	CurrencyCode string
}

// CreateCity creates a new city. Admin only — wired through the audit MW.
func (s *Service) CreateCity(ctx context.Context, adminID uuid.UUID, req CreateCityRequest) (*store.City, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("invalid: name required")
	}
	c, err := s.store.CreateCity(ctx, req.Name, req.State, req.Country, req.CurrencyCode)
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "city.create", "city", c.ID, "")
	return c, nil
}

// UpdateCityRequest is the input shape for UpdateCity.
type UpdateCityRequest struct {
	Name         *string
	State        *string
	Country      *string
	CurrencyCode *string
	IsActive     *bool
}

// UpdateCity applies a partial update. Admin only.
func (s *Service) UpdateCity(ctx context.Context, adminID, cityID uuid.UUID, req UpdateCityRequest) (*store.City, error) {
	c, err := s.store.UpdateCity(ctx, cityID, store.UpdateCityInput{
		Name:         req.Name,
		State:        req.State,
		Country:      req.Country,
		CurrencyCode: req.CurrencyCode,
		IsActive:     req.IsActive,
	})
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "city.update", "city", cityID, "")
	return c, nil
}

// CreateZoneRequest is the input for CreateZone.
type CreateZoneRequest struct {
	CityID      uuid.UUID
	Name        string
	BoundaryWKT string // POLYGON((lng lat,...)) — caller-validated.
}

// CreateZone creates a new PostGIS zone polygon for a city.
func (s *Service) CreateZone(ctx context.Context, adminID uuid.UUID, req CreateZoneRequest) (*store.Zone, error) {
	if req.CityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("invalid: name required")
	}
	if !looksLikeWKTPolygon(req.BoundaryWKT) {
		return nil, fmt.Errorf("invalid: boundary must be a POLYGON WKT string")
	}
	z, err := s.store.CreateZone(ctx, store.CreateZoneInput{
		CityID:      req.CityID,
		Name:        strings.TrimSpace(req.Name),
		BoundaryWKT: req.BoundaryWKT,
	})
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "zone.create", "zone", z.ID, "")
	return z, nil
}

// UpdateZoneRequest is the input for UpdateZone.
type UpdateZoneRequest struct {
	Name        *string
	BoundaryWKT *string
	IsActive    *bool
}

// UpdateZone applies a partial update.
func (s *Service) UpdateZone(ctx context.Context, adminID, zoneID uuid.UUID, req UpdateZoneRequest) (*store.Zone, error) {
	if req.BoundaryWKT != nil && !looksLikeWKTPolygon(*req.BoundaryWKT) {
		return nil, fmt.Errorf("invalid: boundary must be a POLYGON WKT string")
	}
	z, err := s.store.UpdateZone(ctx, zoneID, store.UpdateZoneInput{
		Name:        req.Name,
		BoundaryWKT: req.BoundaryWKT,
		IsActive:    req.IsActive,
	})
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "zone.update", "zone", zoneID, "")
	return z, nil
}

// CreateFareRuleRequest is the input for CreateFareRule. Money may arrive
// either as the legacy INR floats or as paise; a paise field wins when both
// are given, otherwise the float is converted with ROUND(x*100).
type CreateFareRuleRequest struct {
	CityID          uuid.UUID
	VehicleType     string
	BaseFare        float64
	PerKMFare       float64
	PerMinuteFare   float64
	MinimumFare     float64
	PlatformFee     float64
	NightMultiplier float64
	PeakMultiplier  float64
	CancellationFee float64

	BasePaise             *int64
	PerKMPaise            *int64
	PerMinutePaise        *int64
	MinimumPaise          *int64
	PlatformFeePaise      *int64
	CancellationFeePaise  *int64
	WaitingFreeMinutes    *int
	WaitingPerMinutePaise *int64
	CancelFreeSeconds     *int
}

// inrToPaise is ROUND(inr*100) for the legacy float admin bodies.
func inrToPaise(inr float64) int64 { return int64(math.Round(inr * 100)) }

func paiseOr(p *int64, inr float64) int64 {
	if p != nil {
		return *p
	}
	return inrToPaise(inr)
}

// CreateFareRule inserts a new fare-rule row. The most recent active row
// wins per (city, vehicle_type), so this also functions as an "update" by
// supersession — keeping the audit trail intact.
//
// night_multiplier / peak_multiplier are still accepted and stored for the
// legacy admin UI but they no longer price anything: peak and night pricing
// is rider_fare_windows (migration 003).
func (s *Service) CreateFareRule(ctx context.Context, adminID uuid.UUID, req CreateFareRuleRequest) (*store.FareRule, error) {
	if req.CityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	if !allowedVehicleTypes[req.VehicleType] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car")
	}
	in := store.CreateFareRuleInput{
		CityID:               req.CityID,
		VehicleType:          req.VehicleType,
		BasePaise:            paiseOr(req.BasePaise, req.BaseFare),
		PerKMPaise:           paiseOr(req.PerKMPaise, req.PerKMFare),
		PerMinutePaise:       paiseOr(req.PerMinutePaise, req.PerMinuteFare),
		MinimumPaise:         paiseOr(req.MinimumPaise, req.MinimumFare),
		PlatformFeePaise:     paiseOr(req.PlatformFeePaise, req.PlatformFee),
		CancellationFeePaise: paiseOr(req.CancellationFeePaise, req.CancellationFee),
		WaitingFreeMinutes:   3, WaitingPerMinutePaise: 100, CancelFreeSeconds: 120,
		NightMultiplier: req.NightMultiplier,
		PeakMultiplier:  req.PeakMultiplier,
	}
	if req.VehicleType == "auto" {
		in.WaitingPerMinutePaise = 150
	}
	if req.WaitingFreeMinutes != nil {
		in.WaitingFreeMinutes = *req.WaitingFreeMinutes
	}
	if req.WaitingPerMinutePaise != nil {
		in.WaitingPerMinutePaise = *req.WaitingPerMinutePaise
	}
	if req.CancelFreeSeconds != nil {
		in.CancelFreeSeconds = *req.CancelFreeSeconds
	}
	if in.BasePaise < 0 || in.PerKMPaise < 0 || in.PerMinutePaise < 0 || in.MinimumPaise < 0 || in.PlatformFeePaise < 0 ||
		in.CancellationFeePaise < 0 || in.WaitingFreeMinutes < 0 || in.WaitingPerMinutePaise < 0 || in.CancelFreeSeconds < 0 {
		return nil, fmt.Errorf("invalid: fare components must be non-negative")
	}
	if in.NightMultiplier <= 0 {
		in.NightMultiplier = 1.0
	}
	if in.PeakMultiplier <= 0 {
		in.PeakMultiplier = 1.0
	}
	r, err := s.store.CreateFareRule(ctx, in)
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "fare_rule.create", "fare_rule", r.ID, "")
	return r, nil
}

// UpdateFareRuleRequest is the input for UpdateFareRule (floats are legacy;
// a paise field wins).
type UpdateFareRuleRequest struct {
	BaseFare        *float64
	PerKMFare       *float64
	PerMinuteFare   *float64
	MinimumFare     *float64
	PlatformFee     *float64
	NightMultiplier *float64
	PeakMultiplier  *float64
	CancellationFee *float64
	IsActive        *bool

	BasePaise             *int64
	PerKMPaise            *int64
	PerMinutePaise        *int64
	MinimumPaise          *int64
	PlatformFeePaise      *int64
	CancellationFeePaise  *int64
	WaitingFreeMinutes    *int
	WaitingPerMinutePaise *int64
	CancelFreeSeconds     *int
}

func paisePtr(p *int64, inr *float64) *int64 {
	if p != nil {
		return p
	}
	if inr == nil {
		return nil
	}
	v := inrToPaise(*inr)
	return &v
}

// UpdateFareRule applies a partial update.
func (s *Service) UpdateFareRule(ctx context.Context, adminID, ruleID uuid.UUID, req UpdateFareRuleRequest) (*store.FareRule, error) {
	in := store.UpdateFareRuleInput{
		BasePaise:             paisePtr(req.BasePaise, req.BaseFare),
		PerKMPaise:            paisePtr(req.PerKMPaise, req.PerKMFare),
		PerMinutePaise:        paisePtr(req.PerMinutePaise, req.PerMinuteFare),
		MinimumPaise:          paisePtr(req.MinimumPaise, req.MinimumFare),
		PlatformFeePaise:      paisePtr(req.PlatformFeePaise, req.PlatformFee),
		CancellationFeePaise:  paisePtr(req.CancellationFeePaise, req.CancellationFee),
		WaitingFreeMinutes:    req.WaitingFreeMinutes,
		WaitingPerMinutePaise: req.WaitingPerMinutePaise,
		CancelFreeSeconds:     req.CancelFreeSeconds,
		NightMultiplier:       req.NightMultiplier,
		PeakMultiplier:        req.PeakMultiplier,
		IsActive:              req.IsActive,
	}
	for _, p := range []*int64{in.BasePaise, in.PerKMPaise, in.PerMinutePaise, in.MinimumPaise, in.PlatformFeePaise, in.CancellationFeePaise, in.WaitingPerMinutePaise} {
		if p != nil && *p < 0 {
			return nil, fmt.Errorf("invalid: fare components must be non-negative")
		}
	}
	r, err := s.store.UpdateFareRule(ctx, ruleID, in)
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "fare_rule.update", "fare_rule", ruleID, "")
	return r, nil
}

// looksLikeWKTPolygon does a cheap shape check so blatantly bad input is
// rejected before reaching PostGIS. A real validator (open-ring, lng/lat
// order) lives in a future iteration.
func looksLikeWKTPolygon(wkt string) bool {
	w := strings.TrimSpace(strings.ToUpper(wkt))
	return strings.HasPrefix(w, "POLYGON((") && strings.HasSuffix(w, "))")
}
