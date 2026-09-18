package service

// Fare windows (peak / night multipliers) admin CRUD and the live demand
// state per vehicle type. The pricing lane seeded the rows
// (rider_fare_windows, migration 003); these are the console's routes over
// them. Validation mirrors the table CHECKs.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// FareWindowRequest is the create body.
type FareWindowRequest struct {
	CityID        uuid.UUID  `json:"city_id"`
	VehicleType   *string    `json:"vehicle_type"`
	Name          string     `json:"name"`
	DaysOfWeek    int        `json:"days_of_week"`
	StartMinute   int        `json:"start_minute"`
	EndMinute     int        `json:"end_minute"`
	MultiplierBPS int64      `json:"multiplier_bps"`
	Priority      int        `json:"priority"`
	IsActive      *bool      `json:"is_active"`
	EffectiveFrom *time.Time `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
}

// FareWindowPatchRequest is the PATCH body; absent fields are unchanged.
// vehicle_type "" clears it (all vehicle types); effective_to "" clears it.
type FareWindowPatchRequest struct {
	VehicleType   *string    `json:"vehicle_type"`
	Name          *string    `json:"name"`
	DaysOfWeek    *int       `json:"days_of_week"`
	StartMinute   *int       `json:"start_minute"`
	EndMinute     *int       `json:"end_minute"`
	MultiplierBPS *int64     `json:"multiplier_bps"`
	Priority      *int       `json:"priority"`
	IsActive      *bool      `json:"is_active"`
	EffectiveFrom *time.Time `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
	// ClearEffectiveTo removes the end of the window's validity.
	ClearEffectiveTo bool `json:"clear_effective_to"`
}

func validateWindowFields(days, start, end int, multiplier int64) error {
	if days < 1 || days > 127 {
		return fmt.Errorf("invalid: days_of_week must be a bitmask between 1 and 127 (Mon=1 ... Sun=64)")
	}
	if start < 0 || start > 1439 {
		return fmt.Errorf("invalid: start_minute must be between 0 and 1439")
	}
	if end < 0 || end > 1440 {
		return fmt.Errorf("invalid: end_minute must be between 0 and 1440")
	}
	if multiplier < 10000 || multiplier > 30000 {
		return fmt.Errorf("invalid: multiplier_bps must be between 10000 (x1.0) and 30000 (x3.0)")
	}
	return nil
}

func validateWindowVehicleType(vt *string) (*string, error) {
	if vt == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*vt)
	if v == "" {
		return nil, nil
	}
	if !allowedVehicleTypes[v] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car, or null for all")
	}
	return &v, nil
}

// ListFareWindowsAdmin lists windows, optionally for one city.
func (s *Service) ListFareWindowsAdmin(ctx context.Context, cityID *uuid.UUID) ([]store.FareWindow, error) {
	return s.store.ListFareWindowsAdmin(ctx, cityID)
}

// CreateFareWindow creates a window (audited).
func (s *Service) CreateFareWindow(ctx context.Context, adminID uuid.UUID, req FareWindowRequest) (*store.FareWindow, error) {
	if req.CityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	if _, err := s.store.GetCity(ctx, req.CityID); err != nil {
		if errors.Is(err, store.ErrCityNotFound) {
			return nil, fmt.Errorf("not_found: city")
		}
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 80 {
		return nil, fmt.Errorf("invalid: name required (1-80 characters)")
	}
	vt, err := validateWindowVehicleType(req.VehicleType)
	if err != nil {
		return nil, err
	}
	if req.MultiplierBPS == 0 {
		req.MultiplierBPS = 10000
	}
	if err := validateWindowFields(req.DaysOfWeek, req.StartMinute, req.EndMinute, req.MultiplierBPS); err != nil {
		return nil, err
	}
	if req.EffectiveFrom != nil && req.EffectiveTo != nil && !req.EffectiveTo.After(*req.EffectiveFrom) {
		return nil, fmt.Errorf("invalid: effective_to must be after effective_from")
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	w, err := s.store.CreateFareWindow(ctx, store.FareWindowInput{
		CityID: req.CityID, VehicleType: vt, Name: name, DaysOfWeek: req.DaysOfWeek, StartMinute: req.StartMinute,
		EndMinute: req.EndMinute, MultiplierBPS: req.MultiplierBPS, Priority: req.Priority, IsActive: active,
		EffectiveFrom: req.EffectiveFrom, EffectiveTo: req.EffectiveTo,
	})
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "fare_window.create", "fare_window", w.ID, name)
	return w, nil
}

// UpdateFareWindow applies a partial update (audited).
func (s *Service) UpdateFareWindow(ctx context.Context, adminID, id uuid.UUID, req FareWindowPatchRequest) (*store.FareWindow, error) {
	cur, err := s.store.GetFareWindow(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrFareWindowNotFound) {
			return nil, fmt.Errorf("not_found: fare window")
		}
		return nil, err
	}
	patch := store.FareWindowPatch{
		DaysOfWeek: req.DaysOfWeek, StartMinute: req.StartMinute, EndMinute: req.EndMinute, MultiplierBPS: req.MultiplierBPS,
		Priority: req.Priority, IsActive: req.IsActive, EffectiveFrom: req.EffectiveFrom, EffectiveTo: req.EffectiveTo,
		ClearEffectiveTo: req.ClearEffectiveTo,
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 80 {
			return nil, fmt.Errorf("invalid: name required (1-80 characters)")
		}
		patch.Name = &name
	}
	if req.VehicleType != nil {
		vt, err := validateWindowVehicleType(req.VehicleType)
		if err != nil {
			return nil, err
		}
		if vt == nil {
			patch.ClearVehicleType = true
		} else {
			patch.VehicleType = vt
		}
	}
	// Validate the row as it will be after the patch.
	days, start, end, mult := cur.DaysOfWeek, cur.StartMinute, cur.EndMinute, cur.MultiplierBPS
	if req.DaysOfWeek != nil {
		days = *req.DaysOfWeek
	}
	if req.StartMinute != nil {
		start = *req.StartMinute
	}
	if req.EndMinute != nil {
		end = *req.EndMinute
	}
	if req.MultiplierBPS != nil {
		mult = *req.MultiplierBPS
	}
	if err := validateWindowFields(days, start, end, mult); err != nil {
		return nil, err
	}
	from, to := cur.EffectiveFrom, cur.EffectiveTo
	if req.EffectiveFrom != nil {
		from = *req.EffectiveFrom
	}
	if req.ClearEffectiveTo {
		to = nil
	} else if req.EffectiveTo != nil {
		to = req.EffectiveTo
	}
	if to != nil && !to.After(from) {
		return nil, fmt.Errorf("invalid: effective_to must be after effective_from")
	}
	w, err := s.store.UpdateFareWindow(ctx, id, patch)
	if err != nil {
		if errors.Is(err, store.ErrFareWindowNotFound) {
			return nil, fmt.Errorf("not_found: fare window")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "fare_window.update", "fare_window", id, "")
	return w, nil
}

// DeactivateFareWindow switches a window off (audited).
func (s *Service) DeactivateFareWindow(ctx context.Context, adminID, id uuid.UUID, reason string) (*store.FareWindow, error) {
	off := false
	w, err := s.store.UpdateFareWindow(ctx, id, store.FareWindowPatch{IsActive: &off})
	if err != nil {
		if errors.Is(err, store.ErrFareWindowNotFound) {
			return nil, fmt.Errorf("not_found: fare window")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "fare_window.deactivate", "fare_window", id, reason)
	return w, nil
}

// SurgeVehicleState is the live demand state of one vehicle type in a city.
type SurgeVehicleState struct {
	VehicleType string `json:"vehicle_type"`
	Requested   int    `json:"requested"`
	Online      int    `json:"online"`
	DemandBPS   int64  `json:"demand_bps"`
	CapBPS      int64  `json:"cap_bps"`
}

// SurgeState reports the current demand surge inputs and step per vehicle
// type the city prices (its fare rules).
func (s *Service) SurgeState(ctx context.Context, cityID uuid.UUID) ([]SurgeVehicleState, error) {
	if cityID == uuid.Nil {
		return nil, fmt.Errorf("invalid: city_id required")
	}
	rules, err := s.store.ListFareRulesByCity(ctx, cityID)
	if err != nil {
		return nil, err
	}
	out := []SurgeVehicleState{}
	for _, r := range rules {
		requested, online, err := s.store.DemandCounts(ctx, cityID, r.VehicleType)
		if err != nil {
			return nil, err
		}
		out = append(out, SurgeVehicleState{
			VehicleType: r.VehicleType, Requested: requested, Online: online,
			DemandBPS: pricing.DemandBPS(requested, online, s.cfg.SurgeCapBPS), CapBPS: s.cfg.SurgeCapBPS,
		})
	}
	return out, nil
}
