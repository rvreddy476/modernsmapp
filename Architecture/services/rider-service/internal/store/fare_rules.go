package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrFareRuleNotFound is returned when no rule exists for the city + vehicle.
var ErrFareRuleNotFound = errors.New("fare_rule: not found")

// GetFareRule returns the active fare rule for a (city, vehicle_type) pair.
func (s *Store) GetFareRule(ctx context.Context, cityID uuid.UUID, vehicleType string) (*FareRule, error) {
	const q = `
        SELECT id, city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare,
               platform_fee, night_multiplier, peak_multiplier, cancellation_fee, is_active, starts_at
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
        SELECT id, city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare,
               platform_fee, night_multiplier, peak_multiplier, cancellation_fee, is_active, starts_at
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

func scanFareRule(row pgx.Row) (*FareRule, error) {
	var r FareRule
	if err := row.Scan(&r.ID, &r.CityID, &r.VehicleType, &r.BaseFare, &r.PerKMFare, &r.PerMinuteFare, &r.MinimumFare, &r.PlatformFee, &r.NightMultiplier, &r.PeakMultiplier, &r.CancellationFee, &r.IsActive, &r.StartsAt); err != nil {
		return nil, err
	}
	return &r, nil
}
