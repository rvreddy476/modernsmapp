package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrDeliveryConfigConflict = errors.New("delivery configuration changed")
var ErrDeliveryConfigInvalid = errors.New("invalid delivery coverage or payout configuration")

type DeliveryArea struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Latitude     float64   `json:"latitude"`
	Longitude    float64   `json:"longitude"`
	RadiusMeters int       `json:"radius_meters"`
	PayoutPaise  *int64    `json:"payout_paise"`
	Enabled      bool      `json:"enabled"`
	Revision     int64     `json:"revision"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type DeliveryAreaSeller struct {
	AreaID   uuid.UUID `json:"area_id"`
	SellerID uuid.UUID `json:"seller_id"`
	Enabled  bool      `json:"enabled"`
	Revision int64     `json:"revision"`
}

func (a DeliveryArea) valid() bool {
	return len(strings.TrimSpace(a.Name)) >= 2 && len(a.Name) <= 100 &&
		!math.IsNaN(a.Latitude) && !math.IsNaN(a.Longitude) && a.Latitude >= -90 && a.Latitude <= 90 &&
		a.Longitude >= -180 && a.Longitude <= 180 && a.RadiusMeters >= 100 && a.RadiusMeters <= 100000 &&
		(a.PayoutPaise == nil || *a.PayoutPaise > 0) && (!a.Enabled || a.PayoutPaise != nil)
}

const deliveryAreaCols = `id,name,latitude,longitude,radius_meters,payout_paise,enabled,revision,updated_at`

func scanDeliveryArea(row pgx.Row) (*DeliveryArea, error) {
	a := &DeliveryArea{}
	err := row.Scan(&a.ID, &a.Name, &a.Latitude, &a.Longitude, &a.RadiusMeters, &a.PayoutPaise, &a.Enabled, &a.Revision, &a.UpdatedAt)
	return a, err
}

// SaveDeliveryArea is revision-checked and audited with the change. This table
// configures eligibility only; the dispatcher has a separate activation gate.
func (s *Store) SaveDeliveryArea(ctx context.Context, actor uuid.UUID, a DeliveryArea) (*DeliveryArea, error) {
	a.Name = strings.TrimSpace(a.Name)
	if actor == uuid.Nil || !a.valid() || a.Revision < 0 {
		return nil, ErrDeliveryConfigInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var before any
	if a.ID == uuid.Nil {
		if a.Revision != 0 {
			return nil, ErrDeliveryConfigConflict
		}
		a.ID = uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_areas(id,name,latitude,longitude,radius_meters,payout_paise,enabled) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.Name, a.Latitude, a.Longitude, a.RadiusMeters, a.PayoutPaise, a.Enabled)
	} else {
		old, e := scanDeliveryArea(tx.QueryRow(ctx, `SELECT `+deliveryAreaCols+` FROM commerce_delivery_areas WHERE id=$1 FOR UPDATE`, a.ID))
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, ErrDeliveryConfigConflict
		}
		if e != nil {
			return nil, e
		}
		if a.Revision != old.Revision {
			return nil, ErrDeliveryConfigConflict
		}
		before = old
		_, err = tx.Exec(ctx, `UPDATE commerce_delivery_areas SET name=$2,latitude=$3,longitude=$4,radius_meters=$5,payout_paise=$6,enabled=$7,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, a.ID, a.Name, a.Latitude, a.Longitude, a.RadiusMeters, a.PayoutPaise, a.Enabled)
	}
	if err != nil {
		return nil, err
	}
	next, err := scanDeliveryArea(tx.QueryRow(ctx, `SELECT `+deliveryAreaCols+` FROM commerce_delivery_areas WHERE id=$1`, a.ID))
	if err != nil {
		return nil, err
	}
	if err = deliveryConfigAudit(ctx, tx, actor, a.ID, nil, before, next); err != nil {
		return nil, err
	}
	return next, tx.Commit(ctx)
}

func deliveryConfigAudit(ctx context.Context, tx pgx.Tx, actor, area uuid.UUID, seller *uuid.UUID, before, after any) error {
	old, err := json.Marshal(before)
	if err != nil {
		return err
	}
	next, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_config_audit(area_id,seller_id,actor_id,before_state,after_state) VALUES($1,$2,$3,$4,$5)`, area, seller, actor, old, next)
	return err
}

func (s *Store) DeliveryAreas(ctx context.Context, enabledOnly bool) ([]DeliveryArea, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryAreaCols+` FROM commerce_delivery_areas WHERE (NOT $1 OR enabled) ORDER BY name,id LIMIT 500`, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DeliveryArea{}
	for rows.Next() {
		a, err := scanDeliveryArea(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *a)
	}
	return result, rows.Err()
}

func (s *Store) DeliveryAreaSellers(ctx context.Context, area uuid.UUID) ([]DeliveryAreaSeller, error) {
	rows, err := s.db.Query(ctx, `SELECT area_id,seller_id,enabled,revision FROM commerce_delivery_area_sellers WHERE area_id=$1 ORDER BY seller_id`, area)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DeliveryAreaSeller{}
	for rows.Next() {
		var a DeliveryAreaSeller
		if err = rows.Scan(&a.AreaID, &a.SellerID, &a.Enabled, &a.Revision); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Store) SaveDeliveryAreaSeller(ctx context.Context, actor uuid.UUID, a DeliveryAreaSeller) error {
	if actor == uuid.Nil || a.AreaID == uuid.Nil || a.SellerID == uuid.Nil || a.Revision < 0 {
		return ErrDeliveryConfigInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	// Serialize insert/update with area edits. A seller is never auto-enabled.
	var area uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM commerce_delivery_areas WHERE id=$1 FOR UPDATE`, a.AreaID).Scan(&area); errors.Is(err, pgx.ErrNoRows) {
		return ErrDeliveryConfigConflict
	}
	if err != nil {
		return err
	}
	var seller uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM sellers WHERE id=$1 FOR KEY SHARE`, a.SellerID).Scan(&seller); errors.Is(err, pgx.ErrNoRows) {
		return ErrDeliveryConfigInvalid
	}
	if err != nil {
		return err
	}
	old := DeliveryAreaSeller{AreaID: a.AreaID, SellerID: a.SellerID}
	err = tx.QueryRow(ctx, `SELECT enabled,revision FROM commerce_delivery_area_sellers WHERE area_id=$1 AND seller_id=$2`, a.AreaID, a.SellerID).Scan(&old.Enabled, &old.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if a.Revision != old.Revision {
		return ErrDeliveryConfigConflict
	}
	a.Revision++
	_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_area_sellers(area_id,seller_id,enabled,revision) VALUES($1,$2,$3,$4) ON CONFLICT(area_id,seller_id) DO UPDATE SET enabled=EXCLUDED.enabled,revision=EXCLUDED.revision,updated_at=clock_timestamp()`, a.AreaID, a.SellerID, a.Enabled, a.Revision)
	if err != nil {
		return err
	}
	if err = deliveryConfigAudit(ctx, tx, actor, a.AreaID, &a.SellerID, old, a); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Contains uses metres, including antimeridian/polar locations, and rejects
// invalid coordinates. Both pickup AND customer drop must pass before dispatch.
func (a DeliveryArea) Contains(lat, lng float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return false
	}
	rad := math.Pi / 180
	dLat, dLng := (lat-a.Latitude)*rad, (lng-a.Longitude)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Latitude*rad)*math.Cos(lat*rad)*math.Pow(math.Sin(dLng/2), 2)
	distance := 6371000 * 2 * math.Asin(math.Sqrt(math.Min(1, math.Max(0, h))))
	return distance <= float64(a.RadiusMeters)
}
