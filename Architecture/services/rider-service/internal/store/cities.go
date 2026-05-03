package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCityNotFound is returned when GetCity finds no row.
var ErrCityNotFound = errors.New("city: not found")

// ListActiveCities returns every active city, ordered by name.
func (s *Store) ListActiveCities(ctx context.Context) ([]City, error) {
	const q = `
        SELECT id, name, state, country, currency_code, is_active, created_at, updated_at
        FROM rider_cities
        WHERE is_active = TRUE
        ORDER BY name ASC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	defer rows.Close()
	var out []City
	for rows.Next() {
		var c City
		if err := rows.Scan(&c.ID, &c.Name, &c.State, &c.Country, &c.CurrencyCode, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan city: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCity returns the city by id, or ErrCityNotFound.
func (s *Store) GetCity(ctx context.Context, id uuid.UUID) (*City, error) {
	const q = `
        SELECT id, name, state, country, currency_code, is_active, created_at, updated_at
        FROM rider_cities
        WHERE id = $1`
	var c City
	row := s.db.QueryRow(ctx, q, id)
	if err := row.Scan(&c.ID, &c.Name, &c.State, &c.Country, &c.CurrencyCode, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCityNotFound
		}
		return nil, fmt.Errorf("get city: %w", err)
	}
	return &c, nil
}

// CreateCity inserts a new city. Mostly used in tests + admin paths.
func (s *Store) CreateCity(ctx context.Context, name, state, country, currency string) (*City, error) {
	if country == "" {
		country = "India"
	}
	if currency == "" {
		currency = "INR"
	}
	const q = `
        INSERT INTO rider_cities (name, state, country, currency_code, is_active)
        VALUES ($1, $2, $3, $4, TRUE)
        ON CONFLICT (name, state, country) DO UPDATE SET updated_at = NOW()
        RETURNING id, name, state, country, currency_code, is_active, created_at, updated_at`
	var c City
	row := s.db.QueryRow(ctx, q, name, state, country, currency)
	if err := row.Scan(&c.ID, &c.Name, &c.State, &c.Country, &c.CurrencyCode, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create city: %w", err)
	}
	return &c, nil
}
