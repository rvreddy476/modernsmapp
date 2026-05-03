package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrVehicleNotFound is returned when the vehicle id doesn't match a row.
var ErrVehicleNotFound = errors.New("vehicle: not found")

// CreateVehicleInput is the input for CreateVehicle.
type CreateVehicleInput struct {
	PartnerID          uuid.UUID
	VehicleType        string
	RegistrationNumber string
	Brand              *string
	Model              *string
	Color              *string
	ManufactureYear    *int
	SeatCount          *int
	FuelType           *string
	IsEV               bool
}

// CreateVehicle inserts a new vehicle in `pending` verification status. The
// unique index on registration_number prevents one number being shared across
// partners (anti-fraud per Mopedu spec §13).
func (s *Store) CreateVehicle(ctx context.Context, in CreateVehicleInput) (*Vehicle, error) {
	const q = `
        INSERT INTO rider_vehicles (partner_id, vehicle_type, registration_number, brand, model, color, manufacture_year, seat_count, fuel_type, is_ev, status, is_active)
        VALUES ($1, $2::rider_vehicle_type, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', TRUE)
        RETURNING id, partner_id, vehicle_type, registration_number, brand, model, color, manufacture_year, seat_count, fuel_type, is_ev, status, is_active, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.PartnerID, in.VehicleType, in.RegistrationNumber, in.Brand, in.Model, in.Color, in.ManufactureYear, in.SeatCount, in.FuelType, in.IsEV)
	return scanVehicle(row)
}

// GetVehicle returns the vehicle by id.
func (s *Store) GetVehicle(ctx context.Context, id uuid.UUID) (*Vehicle, error) {
	const q = `
        SELECT id, partner_id, vehicle_type, registration_number, brand, model, color, manufacture_year, seat_count, fuel_type, is_ev, status, is_active, created_at, updated_at
        FROM rider_vehicles
        WHERE id = $1 AND deleted_at IS NULL`
	row := s.db.QueryRow(ctx, q, id)
	v, err := scanVehicle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVehicleNotFound
		}
		return nil, err
	}
	return v, nil
}

// ListVehiclesByPartner returns the partner's vehicles.
func (s *Store) ListVehiclesByPartner(ctx context.Context, partnerID uuid.UUID) ([]Vehicle, error) {
	const q = `
        SELECT id, partner_id, vehicle_type, registration_number, brand, model, color, manufacture_year, seat_count, fuel_type, is_ev, status, is_active, created_at, updated_at
        FROM rider_vehicles
        WHERE partner_id = $1 AND deleted_at IS NULL
        ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, q, partnerID)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	defer rows.Close()
	var out []Vehicle
	for rows.Next() {
		v, err := scanVehicle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func scanVehicle(row pgx.Row) (*Vehicle, error) {
	var v Vehicle
	if err := row.Scan(&v.ID, &v.PartnerID, &v.VehicleType, &v.RegistrationNumber, &v.Brand, &v.Model, &v.Color, &v.ManufactureYear, &v.SeatCount, &v.FuelType, &v.IsEV, &v.Status, &v.IsActive, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}
