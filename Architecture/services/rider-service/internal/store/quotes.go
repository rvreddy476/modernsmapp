package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrQuoteNotFound is returned when a quote snapshot is not found or expired.
var ErrQuoteNotFound = errors.New("quote: not found")

// QuoteBreakdownPaise provides itemized paise amounts.
type QuoteBreakdownPaise struct {
	BasePaise        int64 `json:"base_paise"`
	DistancePaise    int64 `json:"distance_paise"`
	TimePaise        int64 `json:"time_paise"`
	PlatformFeePaise int64 `json:"platform_fee_paise"`
	TaxPaise         int64 `json:"tax_paise"`
	TollPaise        int64 `json:"toll_paise"`
	SurgeBasisPoints int64 `json:"surge_basis_points"`
}

// QuoteOption is a single vehicle option in a quote snapshot.
type QuoteOption struct {
	VehicleType      string              `json:"vehicle_type"`
	Available        bool                `json:"available"`
	PickupETASeconds int                 `json:"pickup_eta_seconds"`
	DistanceMeters   int                 `json:"distance_meters"`
	DurationSeconds  int                 `json:"duration_seconds"`
	Currency         string              `json:"currency"`
	TotalPaise       int64               `json:"total_paise"`
	Breakdown        QuoteBreakdownPaise `json:"breakdown"`
}

// QuoteSnapshot is one row in rider_quote_snapshots.
type QuoteSnapshot struct {
	ID                uuid.UUID     `json:"id"`
	CustomerUserID    *uuid.UUID    `json:"customer_user_id,omitempty"`
	CityID            *uuid.UUID    `json:"city_id,omitempty"`
	PickupLat         float64       `json:"pickup_lat"`
	PickupLng         float64       `json:"pickup_lng"`
	PickupLabel       string        `json:"pickup_label,omitempty"`
	PickupPlaceID     string        `json:"pickup_place_id,omitempty"`
	DropLat           float64       `json:"drop_lat"`
	DropLng           float64       `json:"drop_lng"`
	DropLabel         string        `json:"drop_label,omitempty"`
	DropPlaceID       string        `json:"drop_place_id,omitempty"`
	RouteVersion      string        `json:"route_version"`
	FarePolicyVersion int           `json:"fare_policy_version"`
	DistanceMeters    int           `json:"distance_meters"`
	DurationSeconds   int           `json:"duration_seconds"`
	Options           []QuoteOption `json:"options"`
	RequestHash       string        `json:"request_hash"`
	ExpiresAt         time.Time     `json:"expires_at"`
	CreatedAt         time.Time     `json:"created_at"`
}

// CreateQuoteInput is the input struct for creating a quote snapshot.
type CreateQuoteInput struct {
	CustomerUserID    *uuid.UUID
	CityID            *uuid.UUID
	PickupLat         float64
	PickupLng         float64
	PickupLabel       string
	PickupPlaceID     string
	DropLat           float64
	DropLng           float64
	DropLabel         string
	DropPlaceID       string
	RouteVersion      string
	FarePolicyVersion int
	DistanceMeters    int
	DurationSeconds   int
	Options           []QuoteOption
	RequestHash       string
	ExpiresAt         time.Time
}

// CreateQuoteSnapshot inserts a new quote snapshot into PostgreSQL.
func (s *Store) CreateQuoteSnapshot(ctx context.Context, in CreateQuoteInput) (*QuoteSnapshot, error) {
	optionsJSON, err := json.Marshal(in.Options)
	if err != nil {
		return nil, fmt.Errorf("marshal quote options: %w", err)
	}

	const q = `
        INSERT INTO rider_quote_snapshots (
            customer_user_id, city_id,
            pickup_lat, pickup_lng, pickup_label, pickup_place_id,
            drop_lat, drop_lng, drop_label, drop_place_id,
            route_version, fare_policy_version, distance_meters, duration_seconds,
            options, request_hash, expires_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
        )
        RETURNING id, customer_user_id, city_id,
                  pickup_lat, pickup_lng, pickup_label, pickup_place_id,
                  drop_lat, drop_lng, drop_label, drop_place_id,
                  route_version, fare_policy_version, distance_meters, duration_seconds,
                  options, request_hash, expires_at, created_at`

	var qs QuoteSnapshot
	var optBytes []byte
	row := s.db.QueryRow(ctx, q,
		in.CustomerUserID, in.CityID,
		in.PickupLat, in.PickupLng, in.PickupLabel, in.PickupPlaceID,
		in.DropLat, in.DropLng, in.DropLabel, in.DropPlaceID,
		in.RouteVersion, in.FarePolicyVersion, in.DistanceMeters, in.DurationSeconds,
		optionsJSON, in.RequestHash, in.ExpiresAt,
	)
	if err := row.Scan(
		&qs.ID, &qs.CustomerUserID, &qs.CityID,
		&qs.PickupLat, &qs.PickupLng, &qs.PickupLabel, &qs.PickupPlaceID,
		&qs.DropLat, &qs.DropLng, &qs.DropLabel, &qs.DropPlaceID,
		&qs.RouteVersion, &qs.FarePolicyVersion, &qs.DistanceMeters, &qs.DurationSeconds,
		&optBytes, &qs.RequestHash, &qs.ExpiresAt, &qs.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("create quote snapshot: %w", err)
	}

	if len(optBytes) > 0 {
		_ = json.Unmarshal(optBytes, &qs.Options)
	}
	return &qs, nil
}

// GetQuoteSnapshot fetches a quote snapshot by ID.
func (s *Store) GetQuoteSnapshot(ctx context.Context, id uuid.UUID) (*QuoteSnapshot, error) {
	const q = `
        SELECT id, customer_user_id, city_id,
               pickup_lat, pickup_lng, pickup_label, pickup_place_id,
               drop_lat, drop_lng, drop_label, drop_place_id,
               route_version, fare_policy_version, distance_meters, duration_seconds,
               options, request_hash, expires_at, created_at
        FROM rider_quote_snapshots
        WHERE id = $1`

	var qs QuoteSnapshot
	var optBytes []byte
	row := s.db.QueryRow(ctx, q, id)
	if err := row.Scan(
		&qs.ID, &qs.CustomerUserID, &qs.CityID,
		&qs.PickupLat, &qs.PickupLng, &qs.PickupLabel, &qs.PickupPlaceID,
		&qs.DropLat, &qs.DropLng, &qs.DropLabel, &qs.DropPlaceID,
		&qs.RouteVersion, &qs.FarePolicyVersion, &qs.DistanceMeters, &qs.DurationSeconds,
		&optBytes, &qs.RequestHash, &qs.ExpiresAt, &qs.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrQuoteNotFound
		}
		return nil, err
	}

	if len(optBytes) > 0 {
		_ = json.Unmarshal(optBytes, &qs.Options)
	}
	return &qs, nil
}
