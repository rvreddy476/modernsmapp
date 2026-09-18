package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrFareWindowNotFound is returned when no window matches.
var ErrFareWindowNotFound = errors.New("fare_window: not found")

const fareWindowColumns = `id, city_id, vehicle_type, name, days_of_week, start_minute, end_minute, multiplier_bps,
               priority, is_active, effective_from, effective_to, created_at, updated_at`

func scanFareWindow(row pgx.Row) (*FareWindow, error) {
	var w FareWindow
	if err := row.Scan(&w.ID, &w.CityID, &w.VehicleType, &w.Name, &w.DaysOfWeek, &w.StartMinute, &w.EndMinute,
		&w.MultiplierBPS, &w.Priority, &w.IsActive, &w.EffectiveFrom, &w.EffectiveTo, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	return &w, nil
}

// ListFareWindowsAdmin lists every window (active or not), optionally for
// one city, by city then priority.
func (s *Store) ListFareWindowsAdmin(ctx context.Context, cityID *uuid.UUID) ([]FareWindow, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+fareWindowColumns+`
        FROM rider_fare_windows
        WHERE ($1::uuid IS NULL OR city_id = $1)
        ORDER BY city_id, priority DESC, name ASC, created_at ASC`, cityID)
	if err != nil {
		return nil, fmt.Errorf("list fare windows: %w", err)
	}
	defer rows.Close()
	out := []FareWindow{}
	for rows.Next() {
		w, err := scanFareWindow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// FareWindowInput is the create payload (validated by the service; the
// table CHECKs are the last line).
type FareWindowInput struct {
	CityID        uuid.UUID
	VehicleType   *string
	Name          string
	DaysOfWeek    int
	StartMinute   int
	EndMinute     int
	MultiplierBPS int64
	Priority      int
	IsActive      bool
	EffectiveFrom *time.Time
	EffectiveTo   *time.Time
}

// CreateFareWindow inserts one window.
func (s *Store) CreateFareWindow(ctx context.Context, in FareWindowInput) (*FareWindow, error) {
	w, err := scanFareWindow(s.db.QueryRow(ctx, `
        INSERT INTO rider_fare_windows (city_id, vehicle_type, name, days_of_week, start_minute, end_minute, multiplier_bps,
                                        priority, is_active, effective_from, effective_to)
        VALUES ($1, $2::rider_vehicle_type, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, NOW()), $11)
        RETURNING `+fareWindowColumns,
		in.CityID, in.VehicleType, in.Name, in.DaysOfWeek, in.StartMinute, in.EndMinute, in.MultiplierBPS,
		in.Priority, in.IsActive, in.EffectiveFrom, in.EffectiveTo))
	if err != nil {
		return nil, fmt.Errorf("create fare window: %w", err)
	}
	return w, nil
}

// FareWindowPatch is the partial update; nil leaves a column as is.
// ClearVehicleType / ClearEffectiveTo null the nullable columns.
type FareWindowPatch struct {
	VehicleType      *string
	ClearVehicleType bool
	Name             *string
	DaysOfWeek       *int
	StartMinute      *int
	EndMinute        *int
	MultiplierBPS    *int64
	Priority         *int
	IsActive         *bool
	EffectiveFrom    *time.Time
	EffectiveTo      *time.Time
	ClearEffectiveTo bool
}

// UpdateFareWindow applies a patch.
func (s *Store) UpdateFareWindow(ctx context.Context, id uuid.UUID, p FareWindowPatch) (*FareWindow, error) {
	w, err := scanFareWindow(s.db.QueryRow(ctx, `
        UPDATE rider_fare_windows SET
            vehicle_type   = CASE WHEN $2 THEN NULL ELSE COALESCE($3::rider_vehicle_type, vehicle_type) END,
            name           = COALESCE($4, name),
            days_of_week   = COALESCE($5, days_of_week),
            start_minute   = COALESCE($6, start_minute),
            end_minute     = COALESCE($7, end_minute),
            multiplier_bps = COALESCE($8, multiplier_bps),
            priority       = COALESCE($9, priority),
            is_active      = COALESCE($10, is_active),
            effective_from = COALESCE($11, effective_from),
            effective_to   = CASE WHEN $12 THEN NULL ELSE COALESCE($13, effective_to) END,
            updated_at     = NOW()
        WHERE id = $1
        RETURNING `+fareWindowColumns,
		id, p.ClearVehicleType, p.VehicleType, p.Name, p.DaysOfWeek, p.StartMinute, p.EndMinute, p.MultiplierBPS,
		p.Priority, p.IsActive, p.EffectiveFrom, p.ClearEffectiveTo, p.EffectiveTo))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFareWindowNotFound
		}
		return nil, fmt.Errorf("update fare window: %w", err)
	}
	return w, nil
}

// GetFareWindow returns one window.
func (s *Store) GetFareWindow(ctx context.Context, id uuid.UUID) (*FareWindow, error) {
	w, err := scanFareWindow(s.db.QueryRow(ctx, `SELECT `+fareWindowColumns+` FROM rider_fare_windows WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFareWindowNotFound
		}
		return nil, err
	}
	return w, nil
}
