package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Customer addresses (A3). The street lines arrive sealed (propii scope
// doorstep.customer_address); this layer never sees them in clear.

// AddressRow is a stored address.
type AddressRow struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Label       string
	LinesSealed []byte
	Locality    string
	CityCode    string
	Pincode     string
	Lat, Lng    float64
	ZoneID      uuid.UUID
	IsDefault   bool
	CreatedAt   time.Time
}

// AddressWrite is an address to store (zone and city already resolved from
// the point by the service).
type AddressWrite struct {
	Label       string
	LinesSealed []byte
	KeyVersion  uint32
	Locality    string
	CityCode    string
	Pincode     string
	Lat, Lng    float64
	ZoneID      uuid.UUID
	IsDefault   bool
}

const addressCols = `id, user_id, label, lines_sealed, locality, city_code, pincode,
	ST_Y(location::geometry), ST_X(location::geometry), zone_id, is_default, created_at`

func scanAddress(r pgx.Row) (*AddressRow, error) {
	var a AddressRow
	var zone *uuid.UUID
	if err := r.Scan(&a.ID, &a.UserID, &a.Label, &a.LinesSealed, &a.Locality, &a.CityCode, &a.Pincode,
		&a.Lat, &a.Lng, &zone, &a.IsDefault, &a.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	if zone != nil {
		a.ZoneID = *zone
	}
	a.CreatedAt = a.CreatedAt.UTC()
	return &a, nil
}

// Addresses lists a user's live addresses, default first, newest next.
func (s *Store) Addresses(ctx context.Context, user uuid.UUID) ([]AddressRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+addressCols+` FROM doorstep.customer_addresses
		WHERE user_id = $1 AND deleted_at IS NULL ORDER BY is_default DESC, created_at DESC, id`, user)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (AddressRow, error) {
		a, err := scanAddress(r)
		if err != nil {
			return AddressRow{}, err
		}
		return *a, nil
	})
}

// Address returns one of the user's live addresses (ErrNotFound otherwise).
func (s *Store) Address(ctx context.Context, user, id uuid.UUID) (*AddressRow, error) {
	return scanAddress(s.db.QueryRow(ctx, `SELECT `+addressCols+` FROM doorstep.customer_addresses
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, user))
}

// InsertAddress stores a new address. The first address is the default;
// a new default clears the old one in the same transaction.
func (s *Store) InsertAddress(ctx context.Context, id, user uuid.UUID, w AddressWrite, at time.Time) (*AddressRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var others int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.customer_addresses WHERE user_id = $1 AND deleted_at IS NULL`, user).Scan(&others); err != nil {
		return nil, err
	}
	isDefault := w.IsDefault || others == 0
	if isDefault {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.customer_addresses SET is_default = FALSE, updated_at = $2
			WHERE user_id = $1 AND is_default AND deleted_at IS NULL`, user, at); err != nil {
			return nil, err
		}
	}
	a, err := scanAddress(tx.QueryRow(ctx, `
		INSERT INTO doorstep.customer_addresses (id, user_id, label, lines_sealed, lines_key_version, locality, city_code, pincode,
		                                         location, zone_id, is_default, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, ST_SetSRID(ST_MakePoint($10, $9), 4326)::geography, $11, $12, $13, $13)
		RETURNING `+addressCols,
		id, user, w.Label, w.LinesSealed, int64(w.KeyVersion), w.Locality, w.CityCode, w.Pincode, w.Lat, w.Lng, w.ZoneID, isDefault, at))
	if err != nil {
		return nil, err
	}
	return a, mapErr(tx.Commit(ctx))
}

// UpdateAddress replaces an address's fields. Clearing the default flag of
// the default address is ignored (a user with addresses always has one).
func (s *Store) UpdateAddress(ctx context.Context, user, id uuid.UUID, w AddressWrite, at time.Time) (*AddressRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var wasDefault bool
	if err := tx.QueryRow(ctx, `SELECT is_default FROM doorstep.customer_addresses
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`, id, user).Scan(&wasDefault); err != nil {
		return nil, mapErr(err)
	}
	isDefault := w.IsDefault || wasDefault
	if isDefault && !wasDefault {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.customer_addresses SET is_default = FALSE, updated_at = $2
			WHERE user_id = $1 AND is_default AND deleted_at IS NULL`, user, at); err != nil {
			return nil, err
		}
	}
	a, err := scanAddress(tx.QueryRow(ctx, `
		UPDATE doorstep.customer_addresses
		   SET label = $3, lines_sealed = $4, lines_key_version = $5, locality = $6, city_code = $7, pincode = $8,
		       location = ST_SetSRID(ST_MakePoint($10, $9), 4326)::geography, zone_id = $11, is_default = $12, updated_at = $13
		 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING `+addressCols,
		id, user, w.Label, w.LinesSealed, int64(w.KeyVersion), w.Locality, w.CityCode, w.Pincode, w.Lat, w.Lng, w.ZoneID, isDefault, at))
	if err != nil {
		return nil, err
	}
	return a, mapErr(tx.Commit(ctx))
}

// DeleteAddress soft-deletes an address (bookings keep their snapshot). A
// deleted default passes the flag to the newest remaining address.
func (s *Store) DeleteAddress(ctx context.Context, user, id uuid.UUID, at time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var wasDefault bool
	err = tx.QueryRow(ctx, `SELECT is_default FROM doorstep.customer_addresses
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`, id, user).Scan(&wasDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.customer_addresses SET deleted_at = $2, is_default = FALSE, updated_at = $2
		WHERE id = $1`, id, at); err != nil {
		return err
	}
	if wasDefault {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.customer_addresses SET is_default = TRUE, updated_at = $2
			WHERE id = (SELECT id FROM doorstep.customer_addresses WHERE user_id = $1 AND deleted_at IS NULL
			            ORDER BY created_at DESC, id LIMIT 1)`, user, at); err != nil {
			return err
		}
	}
	return mapErr(tx.Commit(ctx))
}
