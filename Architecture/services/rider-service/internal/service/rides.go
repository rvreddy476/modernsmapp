package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/geo"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// CreateRideOperation is the idempotency-table operation label.
const CreateRideOperation = "ride_create"

// allowedRidePaymentMethods: cash settles with the captain; upi and card are
// wired by the payments lane. "wallet" is NOT a ride method any more: the
// wallet stays for partner subscriptions only, and the ride path never
// calls the wallet client.
var allowedRidePaymentMethods = map[string]bool{"cash": true, "upi": true, "card": true}

// CreateRideRequest is the input shape for POST /v1/rider/rides.
type CreateRideRequest struct {
	QuoteID        *uuid.UUID `json:"quote_id,omitempty"`
	PickupAddress  string     `json:"pickup_address"`
	PickupLat      float64    `json:"pickup_lat"`
	PickupLng      float64    `json:"pickup_lng"`
	DropAddress    string     `json:"drop_address"`
	DropLat        float64    `json:"drop_lat"`
	DropLng        float64    `json:"drop_lng"`
	VehicleType    string     `json:"vehicle_type"`
	CityID         *uuid.UUID `json:"city_id,omitempty"`
	PaymentMethod  string     `json:"payment_method"`
	IdempotencyKey string     `json:"idempotency_key"`
	ScheduledFor   *time.Time `json:"scheduled_for,omitempty"`
}

// CreateRide creates a ride row with quote binding, request fingerprinting, and active ride uniqueness protection.
func (s *Service) CreateRide(ctx context.Context, customerID uuid.UUID, req CreateRideRequest) (*store.Ride, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer id required")
	}
	if !allowedVehicleTypes[req.VehicleType] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car")
	}
	if !validLatLng(req.PickupLat, req.PickupLng) || !validLatLng(req.DropLat, req.DropLng) {
		return nil, fmt.Errorf("invalid: pickup and drop coordinates must be valid")
	}
	if strings.TrimSpace(req.PickupAddress) == "" || strings.TrimSpace(req.DropAddress) == "" {
		return nil, fmt.Errorf("invalid: pickup_address and drop_address required")
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	if req.QuoteID == nil || *req.QuoteID == uuid.Nil {
		return nil, fmt.Errorf("invalid: quote_id is required to create a booking")
	}

	// 1. Validate Quote Snapshot
	quote, err := s.store.GetQuoteSnapshot(ctx, *req.QuoteID)
	if err != nil {
		if errors.Is(err, store.ErrQuoteNotFound) {
			return nil, fmt.Errorf("invalid: quote not found or expired")
		}
		return nil, err
	}
	if quote.ExpiresAt.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("invalid: quote expired; please request a fresh estimate")
	}
	if quote.CustomerUserID != nil && *quote.CustomerUserID != customerID {
		return nil, fmt.Errorf("invalid: quote was generated for a different user")
	}

	// 2. Validate route coordinates match quote within 50m tolerance
	pickupDeltaKM := geo.HaversineKM(req.PickupLat, req.PickupLng, quote.PickupLat, quote.PickupLng)
	dropDeltaKM := geo.HaversineKM(req.DropLat, req.DropLng, quote.DropLat, quote.DropLng)
	if pickupDeltaKM > 0.05 || dropDeltaKM > 0.05 {
		return nil, fmt.Errorf("invalid: booking coordinates deviate from quote snapshot (>50m)")
	}

	// 3. Find matching vehicle option in quote
	var matchedOpt *store.QuoteOption
	for _, opt := range quote.Options {
		if opt.VehicleType == req.VehicleType {
			matchedOpt = &opt
			break
		}
	}
	if matchedOpt == nil {
		return nil, fmt.Errorf("invalid: vehicle type %s is not available in quote", req.VehicleType)
	}

	d := float64(matchedOpt.DistanceMeters) / 1000.0
	t := float64(matchedOpt.DurationSeconds) / 60.0
	f := float64(matchedOpt.TotalPaise) / 100.0
	distKM, durMin, fareINR := &d, &t, &f
	fareBreakdownBytes, _ := json.Marshal(matchedOpt.Breakdown)
	quoteIDPtr := &quote.ID
	if req.CityID != nil && quote.CityID != nil && *req.CityID != *quote.CityID {
		return nil, fmt.Errorf("invalid: requested city_id does not match quote snapshot city_id")
	}
	cityIDPtr := quote.CityID
	if cityIDPtr == nil {
		cityIDPtr = req.CityID
	}

	method := req.PaymentMethod
	if method == "" {
		method = "cash"
	}
	if !allowedRidePaymentMethods[method] {
		return nil, fmt.Errorf("invalid: payment_method must be one of cash, upi, card")
	}

	// The quote locked the coupon and the outstanding fees; booking reserves
	// them in the same transaction as the ride row.
	var couponRes *store.CouponReservation
	if matchedOpt.Breakdown.CouponID != "" {
		if !s.cfg.couponsEnabled() {
			return nil, &CouponError{Code: CouponCodeDisabled, Message: "coupons are switched off"}
		}
		cid, perr := uuid.Parse(matchedOpt.Breakdown.CouponID)
		if perr != nil {
			return nil, fmt.Errorf("invalid: quote carries a malformed coupon")
		}
		couponRes = &store.CouponReservation{CouponID: cid, QuoteID: quoteIDPtr, DiscountPaise: matchedOpt.Breakdown.DiscountPaise}
	}
	var outstandingIDs []uuid.UUID
	for _, raw := range matchedOpt.Breakdown.OutstandingIDs {
		id, perr := uuid.Parse(raw)
		if perr != nil {
			return nil, fmt.Errorf("invalid: quote carries a malformed outstanding id")
		}
		outstandingIDs = append(outstandingIDs, id)
	}

	// 4. Compute cryptographic SHA-256 request fingerprint
	cityStr := ""
	if cityIDPtr != nil {
		cityStr = cityIDPtr.String()
	}
	schedStr := ""
	if req.ScheduledFor != nil {
		schedStr = req.ScheduledFor.UTC().Format(time.RFC3339)
	}
	fingerprintRaw := fmt.Sprintf("cust:%s|quote:%s|city:%s|vtype:%s|p:%.5f,%.5f|p_addr:%s|d:%.5f,%.5f|d_addr:%s|meth:%s|sched:%s",
		customerID, quote.ID, cityStr, req.VehicleType, req.PickupLat, req.PickupLng, req.PickupAddress,
		req.DropLat, req.DropLng, req.DropAddress, method, schedStr)
	reqFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprintRaw)))

	rideID := uuid.New()
	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":      rideID.String(),
		"customer_id":  customerID.String(),
		"vehicle_type": req.VehicleType,
		"city_id":      cityIDPtr,
	})

	ride, isReplay, err := s.store.CreateRideAtomic(ctx, store.CreateRideAtomicInput{
		RideID: &rideID,
		RideInput: store.CreateRideInput{
			CustomerUserID:       customerID,
			CityID:               cityIDPtr,
			QuoteID:              quoteIDPtr,
			VehicleType:          req.VehicleType,
			PickupAddress:        req.PickupAddress,
			PickupLat:            req.PickupLat,
			PickupLng:            req.PickupLng,
			DropAddress:          req.DropAddress,
			DropLat:              req.DropLat,
			DropLng:              req.DropLng,
			EstimatedDistanceKM:  distKM,
			EstimatedDurationMin: durMin,
			EstimatedFare:        fareINR,
			FareBreakdown:        fareBreakdownBytes,
			PaymentMethod:        &method,
			ScheduledFor:         req.ScheduledFor,
		},
		IdempotencyKey:  req.IdempotencyKey,
		RequestHash:     reqFingerprint,
		OutboxEventType: "rider.ride.requested",
		OutboxPayload:   outboxPayload,
		Coupon:          couponRes,
		OutstandingIDs:  outstandingIDs,
	})
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyMismatch) {
			return nil, fmt.Errorf("conflict: idempotency key reused with different request parameters")
		}
		if errors.Is(err, store.ErrOutstandingChanged) {
			return nil, fmt.Errorf("invalid: your pending fees changed since the quote; please request a fresh estimate")
		}
		return nil, mapCouponReserveError(err)
	}

	if !isReplay {
		cityID := ""
		if ride.CityID != nil {
			cityID = ride.CityID.String()
		}
		if ride.Status == "scheduled" {
			s.emit(ctx, "rider.ride."+ride.ID.String(), "rider.ride.scheduled", ride)
		} else {
			if perr := s.producer.PublishRideRequested(ctx, ride.ID, customerID, ride.VehicleType, cityID); perr != nil {
				slog.Warn("rider: publish ride.requested failed", "ride_id", ride.ID, "error", perr)
			}
			s.emit(ctx, "rider.ride."+ride.ID.String(), "rider.ride.requested", ride)
			s.publishRealtime(ctx, "rider.admin.live_rides", "rider.ride.requested", ride)
		}
	}
	return ride, nil
}

// GetActiveRideForCustomer returns the customer's active ride, with the OTP
// opened while a captain is assigned (see revealOTPForCustomer).
func (s *Service) GetActiveRideForCustomer(ctx context.Context, customerID uuid.UUID) (*store.Ride, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer id required")
	}
	r, err := s.store.GetActiveRideForCustomer(ctx, customerID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, nil // No active ride
		}
		return nil, err
	}
	s.revealOTPForCustomer(ctx, r)
	return r, nil
}

// otpVisibleStatuses are the ride states in which the customer may read the
// ride OTP: from the moment a captain is assigned until the OTP is used.
// Before assignment there is no OTP; from in_progress on it is spent.
var otpVisibleStatuses = map[string]bool{"partner_assigned": true, "partner_arriving": true, "arrived": true}

// revealOTPForCustomer opens the sealed OTP into r.OTPCode for the customer's
// own ride views (the HTTP layer copies it into the "otp" field), and clears
// it in every other state. The bcrypt hash is never exposed.
func (s *Service) revealOTPForCustomer(ctx context.Context, r *store.Ride) {
	if r == nil {
		return
	}
	r.OTPCode = nil
	if !otpVisibleStatuses[r.Status] || len(r.OTPEncrypted) == 0 || s.otpCrypto == nil {
		return
	}
	decrypted, err := s.otpCrypto.OpenOTP(ctx, r.OTPEncrypted)
	if err != nil {
		slog.Warn("rider: open sealed otp failed", "ride_id", r.ID, "error", err)
		return
	}
	r.OTPCode = &decrypted
}

// GetRide returns a ride by id.
func (s *Service) GetRide(ctx context.Context, customerID, rideID uuid.UUID) (*store.Ride, error) {
	r, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if r.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}
	s.revealOTPForCustomer(ctx, r)
	return r, nil
}

// ListMyRides returns recent rides for the customer.
func (s *Service) ListMyRides(ctx context.Context, customerID uuid.UUID, limit int) ([]store.Ride, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer id required")
	}
	return s.store.ListRidesByCustomer(ctx, customerID, limit)
}

// GetRideReceipt returns the immutable trip receipt for rider or partner.
func (s *Service) GetRideReceipt(ctx context.Context, userID, rideID uuid.UUID) (*store.RideReceipt, error) {
	if userID == uuid.Nil || rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id and ride id required")
	}
	receipt, err := s.store.GetRideReceipt(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride receipt")
		}
		return nil, err
	}
	// Check access
	if receipt.CustomerUserID != userID {
		partner, err := s.store.GetPartnerByUserID(ctx, userID)
		if err != nil || partner == nil || receipt.PartnerID == nil || *receipt.PartnerID != partner.ID {
			return nil, fmt.Errorf("forbidden: not authorized to view receipt")
		}
	}
	return receipt, nil
}
