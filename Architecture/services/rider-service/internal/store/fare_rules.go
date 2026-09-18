package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"

	"github.com/atpost/rider-service/internal/pricing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrFareRuleNotFound is returned when no rule exists for the city + vehicle.
var ErrFareRuleNotFound = errors.New("fare_rule: not found")

const fareRuleColumns = `
        id, city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare,
        platform_fee, night_multiplier, peak_multiplier, cancellation_fee, is_active, starts_at,
        base_fare_paise, per_km_fare_paise, per_minute_fare_paise, minimum_fare_paise,
        platform_fee_paise, cancellation_fee_paise,
        waiting_free_minutes, waiting_per_minute_paise, cancel_free_seconds`

// GetFareRule returns the active fare rule for a (city, vehicle_type) pair.
func (s *Store) GetFareRule(ctx context.Context, cityID uuid.UUID, vehicleType string) (*FareRule, error) {
	const q = `
        SELECT ` + fareRuleColumns + `
        FROM rider_fare_rules
        WHERE city_id = $1 AND vehicle_type = $2::rider_vehicle_type AND is_active = TRUE
        ORDER BY starts_at DESC
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, cityID, vehicleType)
	r, err := scanFareRule(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFareRuleNotFound
		}
		return nil, err
	}
	return r, nil
}

// ListFareRulesByCity returns every active rule in the city, ordered by
// vehicle_type. Used by /v1/rider/cities responses + admin UI in S3.
func (s *Store) ListFareRulesByCity(ctx context.Context, cityID uuid.UUID) ([]FareRule, error) {
	const q = `
        SELECT ` + fareRuleColumns + `
        FROM rider_fare_rules
        WHERE city_id = $1 AND is_active = TRUE
        ORDER BY vehicle_type ASC`
	rows, err := s.db.Query(ctx, q, cityID)
	if err != nil {
		return nil, fmt.Errorf("list fare rules: %w", err)
	}
	defer rows.Close()
	var out []FareRule
	for rows.Next() {
		r, err := scanFareRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// floatFallbackOnce logs the first fare rule read through the float fallback.
var floatFallbackOnce sync.Once

// paiseOrFloat returns the paise column, or ROUND(float*100) when the paise
// column is 0 and the float is not (a row written by the legacy float admin
// API and never backfilled). The fallback is logged once per process.
func paiseOrFloat(paise int64, float float64, ruleID uuid.UUID, column string) int64 {
	if paise != 0 || float == 0 {
		return paise
	}
	floatFallbackOnce.Do(func() {
		slog.Warn("rider: fare rule paise column is 0 while the float is set; pricing from ROUND(float*100) (logged once)",
			"rule_id", ruleID, "column", column)
	})
	return int64(math.Round(float * 100))
}

func scanFareRule(row pgx.Row) (*FareRule, error) {
	var r FareRule
	if err := row.Scan(
		&r.ID, &r.CityID, &r.VehicleType, &r.BaseFare, &r.PerKMFare, &r.PerMinuteFare, &r.MinimumFare,
		&r.PlatformFee, &r.NightMultiplier, &r.PeakMultiplier, &r.CancellationFee, &r.IsActive, &r.StartsAt,
		&r.BasePaise, &r.PerKMPaise, &r.PerMinutePaise, &r.MinimumPaise,
		&r.PlatformFeePaise, &r.CancellationFeePaise,
		&r.WaitingFreeMinutes, &r.WaitingPerMinutePaise, &r.CancelFreeSeconds,
	); err != nil {
		return nil, err
	}
	r.BasePaise = paiseOrFloat(r.BasePaise, r.BaseFare, r.ID, "base_fare_paise")
	r.PerKMPaise = paiseOrFloat(r.PerKMPaise, r.PerKMFare, r.ID, "per_km_fare_paise")
	r.PerMinutePaise = paiseOrFloat(r.PerMinutePaise, r.PerMinuteFare, r.ID, "per_minute_fare_paise")
	r.MinimumPaise = paiseOrFloat(r.MinimumPaise, r.MinimumFare, r.ID, "minimum_fare_paise")
	r.PlatformFeePaise = paiseOrFloat(r.PlatformFeePaise, r.PlatformFee, r.ID, "platform_fee_paise")
	r.CancellationFeePaise = paiseOrFloat(r.CancellationFeePaise, r.CancellationFee, r.ID, "cancellation_fee_paise")
	// The legacy float view is derived from the paise, never the other way.
	r.BaseFare = float64(r.BasePaise) / 100
	r.PerKMFare = float64(r.PerKMPaise) / 100
	r.PerMinuteFare = float64(r.PerMinutePaise) / 100
	r.MinimumFare = float64(r.MinimumPaise) / 100
	r.PlatformFee = float64(r.PlatformFeePaise) / 100
	r.CancellationFee = float64(r.CancellationFeePaise) / 100
	return &r, nil
}

// PricingRule is the engine's view of the rule.
func (r *FareRule) PricingRule() pricing.FareRule {
	return pricing.FareRule{
		BasePaise:             r.BasePaise,
		PerKMPaise:            r.PerKMPaise,
		PerMinutePaise:        r.PerMinutePaise,
		MinimumPaise:          r.MinimumPaise,
		PlatformFeePaise:      r.PlatformFeePaise,
		CancellationFeePaise:  r.CancellationFeePaise,
		WaitingFreeMinutes:    r.WaitingFreeMinutes,
		WaitingPerMinutePaise: r.WaitingPerMinutePaise,
		CancelFreeSeconds:     r.CancelFreeSeconds,
	}
}

// ListFareWindows returns the active windows that can apply to a vehicle
// type in a city (its own plus the all-vehicle ones). Time selection is the
// engine's job (pricing.SelectWindow).
func (s *Store) ListFareWindows(ctx context.Context, cityID uuid.UUID, vehicleType string) ([]FareWindow, error) {
	const q = `
        SELECT id, city_id, vehicle_type, name, days_of_week, start_minute, end_minute, multiplier_bps,
               priority, is_active, effective_from, effective_to, created_at, updated_at
        FROM rider_fare_windows
        WHERE city_id = $1 AND is_active = TRUE
          AND (vehicle_type IS NULL OR vehicle_type = $2::rider_vehicle_type)
        ORDER BY priority DESC, name ASC`
	rows, err := s.db.Query(ctx, q, cityID, vehicleType)
	if err != nil {
		return nil, fmt.Errorf("list fare windows: %w", err)
	}
	defer rows.Close()
	var out []FareWindow
	for rows.Next() {
		var w FareWindow
		if err := rows.Scan(&w.ID, &w.CityID, &w.VehicleType, &w.Name, &w.DaysOfWeek, &w.StartMinute, &w.EndMinute,
			&w.MultiplierBPS, &w.Priority, &w.IsActive, &w.EffectiveFrom, &w.EffectiveTo, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan fare window: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// PricingWindows converts rows for pricing.SelectWindow.
func PricingWindows(ws []FareWindow) []pricing.Window {
	out := make([]pricing.Window, 0, len(ws))
	for _, w := range ws {
		vt := ""
		if w.VehicleType != nil {
			vt = *w.VehicleType
		}
		out = append(out, pricing.Window{
			ID: w.ID.String(), Name: w.Name, VehicleType: vt, DaysOfWeek: w.DaysOfWeek,
			StartMinute: w.StartMinute, EndMinute: w.EndMinute, MultiplierBPS: w.MultiplierBPS,
			Priority: w.Priority, IsActive: w.IsActive, EffectiveFrom: w.EffectiveFrom, EffectiveTo: w.EffectiveTo,
		})
	}
	return out
}

// DemandCounts returns the inputs of the demand ratio for a (city, vehicle
// type): rides in requested / searching_partner created in the last five
// minutes, and online partners with an approved active vehicle of the type.
func (s *Store) DemandCounts(ctx context.Context, cityID uuid.UUID, vehicleType string) (requested, online int, err error) {
	const q = `
        SELECT
          (SELECT COUNT(*) FROM rider_rides r
             WHERE r.city_id = $1 AND r.vehicle_type = $2::rider_vehicle_type
               AND r.status IN ('requested','searching_partner')
               AND r.created_at > NOW() - INTERVAL '5 minutes')::int,
          (SELECT COUNT(DISTINCT p.id) FROM rider_partners p
             JOIN rider_partner_locations l ON l.partner_id = p.id AND l.is_online = TRUE
             JOIN rider_vehicles v ON v.partner_id = p.id AND v.vehicle_type = $2::rider_vehicle_type
                  AND v.status = 'approved' AND v.is_active = TRUE
             WHERE p.city_id = $1)::int`
	if err := s.db.QueryRow(ctx, q, cityID, vehicleType).Scan(&requested, &online); err != nil {
		return 0, 0, fmt.Errorf("demand counts: %w", err)
	}
	return requested, online, nil
}
