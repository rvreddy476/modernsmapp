// Cities, zones, and fare-rule CRUD for the admin surface (Sprint 3 §17).
package service

import (
	"context"
	"fmt"
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

// CreateFareRuleRequest is the input for CreateFareRule.
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
}

// CreateFareRule inserts a new fare-rule row. The most recent active row
// wins per (city, vehicle_type), so this also functions as an "update" by
// supersession — keeping the audit trail intact.
func (s *Service) CreateFareRule(ctx context.Context, adminID uuid.UUID, req CreateFareRuleRequest) (*store.FareRule, error) {
	if req.CityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	if !allowedVehicleTypes[req.VehicleType] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car")
	}
	if req.BaseFare < 0 || req.PerKMFare < 0 || req.PerMinuteFare < 0 || req.MinimumFare < 0 {
		return nil, fmt.Errorf("invalid: fare components must be non-negative")
	}
	if req.NightMultiplier <= 0 {
		req.NightMultiplier = 1.0
	}
	if req.PeakMultiplier <= 0 {
		req.PeakMultiplier = 1.0
	}
	r, err := s.store.CreateFareRule(ctx, store.CreateFareRuleInput{
		CityID:          req.CityID,
		VehicleType:     req.VehicleType,
		BaseFare:        req.BaseFare,
		PerKMFare:       req.PerKMFare,
		PerMinuteFare:   req.PerMinuteFare,
		MinimumFare:     req.MinimumFare,
		PlatformFee:     req.PlatformFee,
		NightMultiplier: req.NightMultiplier,
		PeakMultiplier:  req.PeakMultiplier,
		CancellationFee: req.CancellationFee,
	})
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "fare_rule.create", "fare_rule", r.ID, "")
	return r, nil
}

// UpdateFareRuleRequest is the input for UpdateFareRule.
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
}

// UpdateFareRule applies a partial update.
func (s *Service) UpdateFareRule(ctx context.Context, adminID, ruleID uuid.UUID, req UpdateFareRuleRequest) (*store.FareRule, error) {
	r, err := s.store.UpdateFareRule(ctx, ruleID, store.UpdateFareRuleInput{
		BaseFare:        req.BaseFare,
		PerKMFare:       req.PerKMFare,
		PerMinuteFare:   req.PerMinuteFare,
		MinimumFare:     req.MinimumFare,
		PlatformFee:     req.PlatformFee,
		NightMultiplier: req.NightMultiplier,
		PeakMultiplier:  req.PeakMultiplier,
		CancellationFee: req.CancellationFee,
		IsActive:        req.IsActive,
	})
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
