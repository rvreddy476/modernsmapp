// Coupons and customer outstanding fees: validation with typed errors, the
// public validate endpoint, and the admin surface.
package service

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

// Coupon validation codes. Handlers answer 422 with the code.
const (
	CouponCodeDisabled      = "COUPONS_DISABLED"
	CouponCodeInvalid       = "COUPON_INVALID"
	CouponCodeExpired       = "COUPON_EXPIRED"
	CouponCodeMinFare       = "COUPON_MIN_FARE"
	CouponCodeFirstRideOnly = "COUPON_FIRST_RIDE_ONLY"
	CouponCodeUserLimit     = "COUPON_USER_LIMIT_REACHED"
	CouponCodeExhausted     = "COUPON_EXHAUSTED"
	CouponCodeWrongCity     = "COUPON_WRONG_CITY"
	CouponCodeWrongVehicle  = "COUPON_WRONG_VEHICLE"
)

// CouponError is a typed coupon validation failure.
type CouponError struct {
	Code    string
	Message string
}

func (e *CouponError) Error() string { return "coupon: " + e.Code + ": " + e.Message }

// AsCouponError unwraps a CouponError.
func AsCouponError(err error) (*CouponError, bool) {
	var ce *CouponError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// couponCoversVehicle reports whether the coupon applies to the vehicle type.
func couponCoversVehicle(c *store.Coupon, vehicleType string) bool {
	if c == nil {
		return false
	}
	if len(c.VehicleTypes) == 0 {
		return true
	}
	for _, vt := range c.VehicleTypes {
		if strings.EqualFold(vt, vehicleType) {
			return true
		}
	}
	return false
}

// ValidateCoupon checks everything about a coupon that does not depend on
// the fare: the flag, existence, activity and dates, city, vehicle type
// (when one is requested), first-ride-only, the per-user limit and the total
// limit. The min-fare rule is applied per option by EstimateFare. The
// returned pricing.Coupon is what the engine prices with.
func (s *Service) ValidateCoupon(ctx context.Context, code string, customerID *uuid.UUID, cityID *uuid.UUID, vehicleType string) (*store.Coupon, *pricing.Coupon, error) {
	if !s.cfg.couponsEnabled() {
		return nil, nil, &CouponError{Code: CouponCodeDisabled, Message: "coupons are switched off until the GST treatment of discounts is confirmed"}
	}
	code = store.NormalizeCouponCode(code)
	if code == "" {
		return nil, nil, &CouponError{Code: CouponCodeInvalid, Message: "coupon code required"}
	}
	c, err := s.store.GetCouponByCode(ctx, code)
	if err != nil {
		if errors.Is(err, store.ErrCouponNotFound) {
			return nil, nil, &CouponError{Code: CouponCodeInvalid, Message: "unknown coupon code"}
		}
		return nil, nil, err
	}
	now := s.now()
	if !c.IsActive {
		return nil, nil, &CouponError{Code: CouponCodeInvalid, Message: "coupon is not active"}
	}
	if now.Before(c.StartsAt) {
		return nil, nil, &CouponError{Code: CouponCodeInvalid, Message: "coupon is not valid yet"}
	}
	if c.EndsAt != nil && !now.Before(*c.EndsAt) {
		return nil, nil, &CouponError{Code: CouponCodeExpired, Message: "coupon has expired"}
	}
	if c.CityID != nil && cityID != nil && *c.CityID != *cityID {
		return nil, nil, &CouponError{Code: CouponCodeWrongCity, Message: "coupon is not valid in this city"}
	}
	if vehicleType != "" && !couponCoversVehicle(c, vehicleType) {
		return nil, nil, &CouponError{Code: CouponCodeWrongVehicle, Message: "coupon is not valid for " + vehicleType}
	}
	if c.TotalLimit > 0 && c.UsedCount >= c.TotalLimit {
		return nil, nil, &CouponError{Code: CouponCodeExhausted, Message: "coupon has been fully redeemed"}
	}
	if customerID != nil && *customerID != uuid.Nil {
		if c.FirstRideOnly {
			n, err := s.store.CountCompletedRides(ctx, *customerID)
			if err != nil {
				return nil, nil, err
			}
			if n > 0 {
				return nil, nil, &CouponError{Code: CouponCodeFirstRideOnly, Message: "coupon is for a first ride only"}
			}
		}
		if c.PerUserLimit > 0 {
			uses, err := s.store.CountCouponUses(ctx, c.ID, *customerID)
			if err != nil {
				return nil, nil, err
			}
			if uses >= c.PerUserLimit {
				return nil, nil, &CouponError{Code: CouponCodeUserLimit, Message: "you have already used this coupon"}
			}
		}
	}
	return c, &pricing.Coupon{
		ID:               c.ID.String(),
		Code:             c.Code,
		DiscountType:     c.DiscountType,
		ValuePaise:       c.DiscountValuePaise,
		PercentBPS:       int64(c.PercentBPS),
		MaxDiscountPaise: c.MaxDiscountPaise,
		MinFarePaise:     c.MinFarePaise,
	}, nil
}

// CouponValidation is the public validate response.
type CouponValidation struct {
	Valid              bool     `json:"valid"`
	Code               string   `json:"code"`
	Description        string   `json:"description"`
	DiscountType       string   `json:"discount_type"`
	DiscountValuePaise int64    `json:"discount_value_paise"`
	PercentBPS         int      `json:"percent_bps"`
	MaxDiscountPaise   int64    `json:"max_discount_paise"`
	MinFarePaise       int64    `json:"min_fare_paise"`
	VehicleTypes       []string `json:"vehicle_types,omitempty"`
	FirstRideOnly      bool     `json:"first_ride_only"`
}

// CheckCoupon is GET /v1/rider/coupons/validate: what the UI shows before
// the estimate. A CouponError is returned as-is for the 422 mapping.
func (s *Service) CheckCoupon(ctx context.Context, code string, customerID *uuid.UUID, cityID *uuid.UUID) (*CouponValidation, error) {
	c, _, err := s.ValidateCoupon(ctx, code, customerID, cityID, "")
	if err != nil {
		return nil, err
	}
	return &CouponValidation{
		Valid: true, Code: c.Code, Description: c.Description, DiscountType: c.DiscountType,
		DiscountValuePaise: c.DiscountValuePaise, PercentBPS: c.PercentBPS, MaxDiscountPaise: c.MaxDiscountPaise,
		MinFarePaise: c.MinFarePaise, VehicleTypes: c.VehicleTypes, FirstRideOnly: c.FirstRideOnly,
	}, nil
}

// mapCouponReserveError turns the store's reservation failures (raced
// limits at booking time) into typed errors.
func mapCouponReserveError(err error) error {
	switch {
	case errors.Is(err, store.ErrCouponNotFound), errors.Is(err, store.ErrCouponInactive):
		return &CouponError{Code: CouponCodeInvalid, Message: "coupon is no longer valid"}
	case errors.Is(err, store.ErrCouponTotalExhausted):
		return &CouponError{Code: CouponCodeExhausted, Message: "coupon has been fully redeemed"}
	case errors.Is(err, store.ErrCouponUserExhausted):
		return &CouponError{Code: CouponCodeUserLimit, Message: "you have already used this coupon"}
	}
	return err
}

// --- Admin -----------------------------------------------------------------

// CreateCouponRequest is the admin create body.
type CreateCouponRequest struct {
	Code               string     `json:"code"`
	Description        string     `json:"description"`
	DiscountType       string     `json:"discount_type"`
	DiscountValuePaise int64      `json:"discount_value_paise"`
	PercentBPS         int        `json:"percent_bps"`
	MaxDiscountPaise   int64      `json:"max_discount_paise"`
	MinFarePaise       int64      `json:"min_fare_paise"`
	CityID             *uuid.UUID `json:"city_id,omitempty"`
	VehicleTypes       []string   `json:"vehicle_types,omitempty"`
	FirstRideOnly      bool       `json:"first_ride_only"`
	PerUserLimit       int        `json:"per_user_limit"`
	TotalLimit         int        `json:"total_limit"`
	StartsAt           *time.Time `json:"starts_at,omitempty"`
	EndsAt             *time.Time `json:"ends_at,omitempty"`
	IsActive           *bool      `json:"is_active,omitempty"`
}

// CreateCoupon validates and inserts a coupon (audited by the admin route).
func (s *Service) CreateCoupon(ctx context.Context, adminID uuid.UUID, req CreateCouponRequest) (*store.Coupon, error) {
	code := store.NormalizeCouponCode(req.Code)
	if len(code) < 3 || len(code) > 32 {
		return nil, fmt.Errorf("invalid: code must be 3-32 characters")
	}
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return nil, fmt.Errorf("invalid: code may contain letters, digits, '_' and '-' only")
		}
	}
	switch req.DiscountType {
	case pricing.CouponFlat:
		if req.DiscountValuePaise <= 0 {
			return nil, fmt.Errorf("invalid: discount_value_paise must be positive for a flat coupon")
		}
	case pricing.CouponPercent:
		if req.PercentBPS <= 0 || req.PercentBPS > 10000 {
			return nil, fmt.Errorf("invalid: percent_bps must be 1-10000 for a percent coupon")
		}
	default:
		return nil, fmt.Errorf("invalid: discount_type must be flat or percent")
	}
	if req.MaxDiscountPaise < 0 || req.MinFarePaise < 0 || req.PerUserLimit < 0 || req.TotalLimit < 0 {
		return nil, fmt.Errorf("invalid: limits and amounts must be non-negative")
	}
	for _, vt := range req.VehicleTypes {
		if !allowedVehicleTypes[vt] {
			return nil, fmt.Errorf("invalid: unknown vehicle type %q", vt)
		}
	}
	if req.StartsAt != nil && req.EndsAt != nil && !req.EndsAt.After(*req.StartsAt) {
		return nil, fmt.Errorf("invalid: ends_at must be after starts_at")
	}
	c, err := s.store.CreateCoupon(ctx, store.CreateCouponInput{
		Code: code, Description: strings.TrimSpace(req.Description), DiscountType: req.DiscountType,
		DiscountValuePaise: req.DiscountValuePaise, PercentBPS: req.PercentBPS, MaxDiscountPaise: req.MaxDiscountPaise,
		MinFarePaise: req.MinFarePaise, CityID: req.CityID, VehicleTypes: req.VehicleTypes, FirstRideOnly: req.FirstRideOnly,
		PerUserLimit: req.PerUserLimit, TotalLimit: req.TotalLimit, StartsAt: req.StartsAt, EndsAt: req.EndsAt,
		IsActive: req.IsActive, CreatedBy: adminID,
	})
	if err != nil {
		if errors.Is(err, store.ErrCouponCodeTaken) {
			return nil, fmt.Errorf("conflict: coupon code already exists")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "coupon.create", "coupon", c.ID, "")
	return c, nil
}

// UpdateCouponRequest is the admin patch body.
type UpdateCouponRequest struct {
	Description        *string    `json:"description,omitempty"`
	DiscountValuePaise *int64     `json:"discount_value_paise,omitempty"`
	PercentBPS         *int       `json:"percent_bps,omitempty"`
	MaxDiscountPaise   *int64     `json:"max_discount_paise,omitempty"`
	MinFarePaise       *int64     `json:"min_fare_paise,omitempty"`
	FirstRideOnly      *bool      `json:"first_ride_only,omitempty"`
	PerUserLimit       *int       `json:"per_user_limit,omitempty"`
	TotalLimit         *int       `json:"total_limit,omitempty"`
	StartsAt           *time.Time `json:"starts_at,omitempty"`
	EndsAt             *time.Time `json:"ends_at,omitempty"`
	IsActive           *bool      `json:"is_active,omitempty"`
}

// UpdateCoupon applies a partial update.
func (s *Service) UpdateCoupon(ctx context.Context, adminID, couponID uuid.UUID, req UpdateCouponRequest) (*store.Coupon, error) {
	if req.PercentBPS != nil && (*req.PercentBPS < 0 || *req.PercentBPS > 10000) {
		return nil, fmt.Errorf("invalid: percent_bps must be 0-10000")
	}
	for _, p := range []*int64{req.DiscountValuePaise, req.MaxDiscountPaise, req.MinFarePaise} {
		if p != nil && *p < 0 {
			return nil, fmt.Errorf("invalid: amounts must be non-negative")
		}
	}
	for _, p := range []*int{req.PerUserLimit, req.TotalLimit} {
		if p != nil && *p < 0 {
			return nil, fmt.Errorf("invalid: limits must be non-negative")
		}
	}
	c, err := s.store.UpdateCoupon(ctx, couponID, store.UpdateCouponInput{
		Description: req.Description, DiscountValuePaise: req.DiscountValuePaise, PercentBPS: req.PercentBPS,
		MaxDiscountPaise: req.MaxDiscountPaise, MinFarePaise: req.MinFarePaise, FirstRideOnly: req.FirstRideOnly,
		PerUserLimit: req.PerUserLimit, TotalLimit: req.TotalLimit, StartsAt: req.StartsAt, EndsAt: req.EndsAt, IsActive: req.IsActive,
	})
	if err != nil {
		if errors.Is(err, store.ErrCouponNotFound) {
			return nil, fmt.Errorf("not_found: coupon")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "coupon.update", "coupon", couponID, "")
	return c, nil
}

// DeactivateCoupon flips is_active off.
func (s *Service) DeactivateCoupon(ctx context.Context, adminID, couponID uuid.UUID, reason string) (*store.Coupon, error) {
	off := false
	c, err := s.store.UpdateCoupon(ctx, couponID, store.UpdateCouponInput{IsActive: &off})
	if err != nil {
		if errors.Is(err, store.ErrCouponNotFound) {
			return nil, fmt.Errorf("not_found: coupon")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "coupon.deactivate", "coupon", couponID, reason)
	return c, nil
}

// ListCoupons is the admin list.
func (s *Service) ListCoupons(ctx context.Context, activeOnly bool, limit, offset int) ([]store.Coupon, error) {
	return s.store.ListCoupons(ctx, activeOnly, limit, offset)
}

// ListCouponRedemptions is the admin redemptions list.
func (s *Service) ListCouponRedemptions(ctx context.Context, couponID *uuid.UUID, limit, offset int) ([]store.CouponRedemption, error) {
	return s.store.ListCouponRedemptions(ctx, couponID, limit, offset)
}

// ListOutstanding is the admin outstanding list.
func (s *Service) ListOutstanding(ctx context.Context, customerID *uuid.UUID, status string, limit, offset int) ([]store.CustomerOutstanding, error) {
	return s.store.ListOutstanding(ctx, customerID, status, limit, offset)
}

// WaiveOutstanding waives a pending cancellation fee (audited).
func (s *Service) WaiveOutstanding(ctx context.Context, adminID, id uuid.UUID, reason string) (*store.CustomerOutstanding, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("invalid: reason required")
	}
	o, err := s.store.WaiveOutstanding(ctx, id, adminID, reason)
	if err != nil {
		if errors.Is(err, store.ErrOutstandingNotFound) {
			return nil, fmt.Errorf("not_found: pending outstanding fee")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "outstanding.waive", "customer_outstanding", id, reason)
	return o, nil
}
