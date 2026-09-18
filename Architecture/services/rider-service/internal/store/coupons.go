package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Redemption statuses.
const (
	RedemptionReserved = "reserved"
	RedemptionApplied  = "applied"
	RedemptionReleased = "released"
)

// Coupon errors the service maps to typed validation codes.
var (
	ErrCouponNotFound       = errors.New("coupon: not found")
	ErrCouponTotalExhausted = errors.New("coupon: total limit reached")
	ErrCouponUserExhausted  = errors.New("coupon: per-user limit reached")
	ErrCouponInactive       = errors.New("coupon: inactive or outside its dates")
)

const couponColumns = `
        id, code, description, discount_type, discount_value_paise, percent_bps, max_discount_paise,
        min_fare_paise, city_id, vehicle_types, first_ride_only, per_user_limit, total_limit, used_count,
        starts_at, ends_at, is_active, created_by, created_at, updated_at`

func scanCoupon(row pgx.Row) (*Coupon, error) {
	var c Coupon
	if err := row.Scan(&c.ID, &c.Code, &c.Description, &c.DiscountType, &c.DiscountValuePaise, &c.PercentBPS, &c.MaxDiscountPaise,
		&c.MinFarePaise, &c.CityID, &c.VehicleTypes, &c.FirstRideOnly, &c.PerUserLimit, &c.TotalLimit, &c.UsedCount,
		&c.StartsAt, &c.EndsAt, &c.IsActive, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// NormalizeCouponCode is the case-insensitive key: trimmed, upper-cased.
func NormalizeCouponCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// GetCouponByCode finds a coupon regardless of case.
func (s *Store) GetCouponByCode(ctx context.Context, code string) (*Coupon, error) {
	const q = `SELECT ` + couponColumns + ` FROM rider_coupons WHERE UPPER(code) = $1`
	c, err := scanCoupon(s.db.QueryRow(ctx, q, NormalizeCouponCode(code)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon: %w", err)
	}
	return c, nil
}

// GetCoupon returns a coupon by id.
func (s *Store) GetCoupon(ctx context.Context, id uuid.UUID) (*Coupon, error) {
	const q = `SELECT ` + couponColumns + ` FROM rider_coupons WHERE id = $1`
	c, err := scanCoupon(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon: %w", err)
	}
	return c, nil
}

// CountCouponUses counts the customer's live redemptions (reserved or
// applied) of a coupon.
func (s *Store) CountCouponUses(ctx context.Context, couponID, customerID uuid.UUID) (int, error) {
	const q = `
        SELECT COUNT(*)::int FROM rider_coupon_redemptions
        WHERE coupon_id = $1 AND customer_user_id = $2 AND status IN ('reserved','applied')`
	var n int
	if err := s.db.QueryRow(ctx, q, couponID, customerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count coupon uses: %w", err)
	}
	return n, nil
}

// CountCompletedRides counts the customer's completed rides (first-ride-only
// coupons).
func (s *Store) CountCompletedRides(ctx context.Context, customerID uuid.UUID) (int, error) {
	const q = `SELECT COUNT(*)::int FROM rider_rides WHERE customer_user_id = $1 AND status = 'completed'`
	var n int
	if err := s.db.QueryRow(ctx, q, customerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count completed rides: %w", err)
	}
	return n, nil
}

// CouponReservation is what ride creation locks in.
type CouponReservation struct {
	CouponID      uuid.UUID
	QuoteID       *uuid.UUID
	DiscountPaise int64
}

// ReserveCouponTx reserves one redemption for the ride inside the booking
// transaction. The coupon row is locked FOR UPDATE so the per-user and total
// limits are enforced against a consistent count; used_count moves with the
// reservation and back on release.
func ReserveCouponTx(ctx context.Context, tx pgx.Tx, in CouponReservation, customerID, rideID uuid.UUID, now time.Time) error {
	const lockQ = `SELECT ` + couponColumns + ` FROM rider_coupons WHERE id = $1 FOR UPDATE`
	c, err := scanCoupon(tx.QueryRow(ctx, lockQ, in.CouponID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCouponNotFound
		}
		return fmt.Errorf("lock coupon: %w", err)
	}
	if !c.IsActive || now.Before(c.StartsAt) || (c.EndsAt != nil && !now.Before(*c.EndsAt)) {
		return ErrCouponInactive
	}
	if c.TotalLimit > 0 && c.UsedCount >= c.TotalLimit {
		return ErrCouponTotalExhausted
	}
	if c.PerUserLimit > 0 {
		const usesQ = `
            SELECT COUNT(*)::int FROM rider_coupon_redemptions
            WHERE coupon_id = $1 AND customer_user_id = $2 AND status IN ('reserved','applied')`
		var uses int
		if err := tx.QueryRow(ctx, usesQ, c.ID, customerID).Scan(&uses); err != nil {
			return fmt.Errorf("count uses: %w", err)
		}
		if uses >= c.PerUserLimit {
			return ErrCouponUserExhausted
		}
	}
	const insQ = `
        INSERT INTO rider_coupon_redemptions (coupon_id, customer_user_id, ride_id, quote_id, discount_paise, status)
        VALUES ($1, $2, $3, $4, $5, 'reserved')`
	if _, err := tx.Exec(ctx, insQ, c.ID, customerID, rideID, in.QuoteID, in.DiscountPaise); err != nil {
		return fmt.Errorf("insert redemption: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rider_coupons SET used_count = used_count + 1, updated_at = NOW() WHERE id = $1`, c.ID); err != nil {
		return fmt.Errorf("bump used_count: %w", err)
	}
	return nil
}

// ApplyCouponByRideTx marks the ride's reserved redemption applied.
func ApplyCouponByRideTx(ctx context.Context, tx pgx.Tx, rideID uuid.UUID) error {
	const q = `
        UPDATE rider_coupon_redemptions SET status = 'applied', updated_at = NOW()
        WHERE ride_id = $1 AND status = 'reserved'`
	if _, err := tx.Exec(ctx, q, rideID); err != nil {
		return fmt.Errorf("apply redemption: %w", err)
	}
	return nil
}

// ReleaseCouponByRideTx releases the ride's reserved redemption and gives the
// use back to the coupon.
func ReleaseCouponByRideTx(ctx context.Context, tx pgx.Tx, rideID uuid.UUID) error {
	const q = `
        UPDATE rider_coupon_redemptions SET status = 'released', updated_at = NOW()
        WHERE ride_id = $1 AND status = 'reserved'
        RETURNING coupon_id`
	var couponID uuid.UUID
	err := tx.QueryRow(ctx, q, rideID).Scan(&couponID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release redemption: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rider_coupons SET used_count = GREATEST(used_count - 1, 0), updated_at = NOW() WHERE id = $1`, couponID); err != nil {
		return fmt.Errorf("unbump used_count: %w", err)
	}
	return nil
}

// GetRedemptionByRide returns the ride's redemption, if any.
func (s *Store) GetRedemptionByRide(ctx context.Context, rideID uuid.UUID) (*CouponRedemption, error) {
	const q = `
        SELECT id, coupon_id, customer_user_id, ride_id, quote_id, discount_paise, status, created_at, updated_at
        FROM rider_coupon_redemptions WHERE ride_id = $1`
	var r CouponRedemption
	if err := s.db.QueryRow(ctx, q, rideID).Scan(&r.ID, &r.CouponID, &r.CustomerUserID, &r.RideID, &r.QuoteID, &r.DiscountPaise, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, err
	}
	return &r, nil
}

// --- Admin CRUD -------------------------------------------------------------

// CreateCouponInput is the admin create body.
type CreateCouponInput struct {
	Code               string
	Description        string
	DiscountType       string
	DiscountValuePaise int64
	PercentBPS         int
	MaxDiscountPaise   int64
	MinFarePaise       int64
	CityID             *uuid.UUID
	VehicleTypes       []string
	FirstRideOnly      bool
	PerUserLimit       int
	TotalLimit         int
	StartsAt           *time.Time
	EndsAt             *time.Time
	IsActive           *bool
	CreatedBy          uuid.UUID
}

// ErrCouponCodeTaken is returned when the code exists (case-insensitively).
var ErrCouponCodeTaken = errors.New("coupon: code already exists")

// CreateCoupon inserts a coupon.
func (s *Store) CreateCoupon(ctx context.Context, in CreateCouponInput) (*Coupon, error) {
	const q = `
        INSERT INTO rider_coupons (
            code, description, discount_type, discount_value_paise, percent_bps, max_discount_paise, min_fare_paise,
            city_id, vehicle_types, first_ride_only, per_user_limit, total_limit,
            starts_at, ends_at, is_active, created_by
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
            COALESCE($13, NOW()), $14, COALESCE($15, TRUE), $16
        )
        ON CONFLICT DO NOTHING
        RETURNING ` + couponColumns
	var vts []string
	if len(in.VehicleTypes) > 0 {
		vts = in.VehicleTypes
	}
	c, err := scanCoupon(s.db.QueryRow(ctx, q,
		NormalizeCouponCode(in.Code), in.Description, in.DiscountType, in.DiscountValuePaise, in.PercentBPS, in.MaxDiscountPaise, in.MinFarePaise,
		in.CityID, vts, in.FirstRideOnly, in.PerUserLimit, in.TotalLimit,
		in.StartsAt, in.EndsAt, in.IsActive, in.CreatedBy,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponCodeTaken
		}
		return nil, fmt.Errorf("create coupon: %w", err)
	}
	return c, nil
}

// UpdateCouponInput is the admin patch body; nil leaves a field alone.
type UpdateCouponInput struct {
	Description        *string
	DiscountValuePaise *int64
	PercentBPS         *int
	MaxDiscountPaise   *int64
	MinFarePaise       *int64
	FirstRideOnly      *bool
	PerUserLimit       *int
	TotalLimit         *int
	StartsAt           *time.Time
	EndsAt             *time.Time
	IsActive           *bool
}

// UpdateCoupon applies a partial update.
func (s *Store) UpdateCoupon(ctx context.Context, id uuid.UUID, in UpdateCouponInput) (*Coupon, error) {
	const q = `
        UPDATE rider_coupons SET
            description          = COALESCE($2, description),
            discount_value_paise = COALESCE($3, discount_value_paise),
            percent_bps          = COALESCE($4, percent_bps),
            max_discount_paise   = COALESCE($5, max_discount_paise),
            min_fare_paise       = COALESCE($6, min_fare_paise),
            first_ride_only      = COALESCE($7, first_ride_only),
            per_user_limit       = COALESCE($8, per_user_limit),
            total_limit          = COALESCE($9, total_limit),
            starts_at            = COALESCE($10, starts_at),
            ends_at              = COALESCE($11, ends_at),
            is_active            = COALESCE($12, is_active),
            updated_at           = NOW()
        WHERE id = $1
        RETURNING ` + couponColumns
	c, err := scanCoupon(s.db.QueryRow(ctx, q, id, in.Description, in.DiscountValuePaise, in.PercentBPS, in.MaxDiscountPaise,
		in.MinFarePaise, in.FirstRideOnly, in.PerUserLimit, in.TotalLimit, in.StartsAt, in.EndsAt, in.IsActive))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("update coupon: %w", err)
	}
	return c, nil
}

// ListCoupons is the admin list, newest first; activeOnly filters is_active.
func (s *Store) ListCoupons(ctx context.Context, activeOnly bool, limit, offset int) ([]Coupon, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        SELECT ` + couponColumns + `
        FROM rider_coupons
        WHERE (NOT $1 OR is_active = TRUE)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, activeOnly, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list coupons: %w", err)
	}
	defer rows.Close()
	out := []Coupon{}
	for rows.Next() {
		c, err := scanCoupon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListCouponRedemptions lists redemptions, optionally for one coupon.
func (s *Store) ListCouponRedemptions(ctx context.Context, couponID *uuid.UUID, limit, offset int) ([]CouponRedemption, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        SELECT id, coupon_id, customer_user_id, ride_id, quote_id, discount_paise, status, created_at, updated_at
        FROM rider_coupon_redemptions
        WHERE ($1::uuid IS NULL OR coupon_id = $1)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, couponID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list redemptions: %w", err)
	}
	defer rows.Close()
	out := []CouponRedemption{}
	for rows.Next() {
		var r CouponRedemption
		if err := rows.Scan(&r.ID, &r.CouponID, &r.CustomerUserID, &r.RideID, &r.QuoteID, &r.DiscountPaise, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
