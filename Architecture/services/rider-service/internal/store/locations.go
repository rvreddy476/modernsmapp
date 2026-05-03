package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPartnerLocationNotFound is returned when GetPartnerLocation finds no row.
var ErrPartnerLocationNotFound = errors.New("partner_location: not found")

// UpsertPartnerLocationInput is the input for UpsertPartnerLocation.
type UpsertPartnerLocationInput struct {
	PartnerID    uuid.UUID
	LastLat      float64
	LastLng      float64
	LastGeohash  string
	LastSpeedMPS *float64
	LastHeading  *float64
	IsOnline     bool
}

// UpsertPartnerLocation writes the partner's last-known location row.
//
// On INSERT or UPDATE this is the source-of-truth durable mirror. The hot
// copy in Redis (set by the service layer alongside this call) is what the
// matcher reads on the fast path; this row is the cold-path fallback when
// Redis is down + the queryable history.
func (s *Store) UpsertPartnerLocation(ctx context.Context, in UpsertPartnerLocationInput) error {
	const q = `
        INSERT INTO rider_partner_locations
            (partner_id, last_lat, last_lng, last_geohash, last_speed_mps, last_heading, is_online, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
        ON CONFLICT (partner_id) DO UPDATE SET
            last_lat       = EXCLUDED.last_lat,
            last_lng       = EXCLUDED.last_lng,
            last_geohash   = EXCLUDED.last_geohash,
            last_speed_mps = EXCLUDED.last_speed_mps,
            last_heading   = EXCLUDED.last_heading,
            is_online      = EXCLUDED.is_online,
            updated_at     = NOW()`
	if _, err := s.db.Exec(ctx, q, in.PartnerID, in.LastLat, in.LastLng, in.LastGeohash, in.LastSpeedMPS, in.LastHeading, in.IsOnline); err != nil {
		return fmt.Errorf("upsert partner location: %w", err)
	}
	return nil
}

// SetPartnerOnlineFlag flips the partner's is_online flag without touching
// the location coordinates. Used by GoOnline / GoOffline.
func (s *Store) SetPartnerOnlineFlag(ctx context.Context, partnerID uuid.UUID, online bool) error {
	const q = `
        INSERT INTO rider_partner_locations (partner_id, last_lat, last_lng, last_geohash, is_online, updated_at)
        VALUES ($1, 0, 0, '', $2, NOW())
        ON CONFLICT (partner_id) DO UPDATE SET
            is_online  = EXCLUDED.is_online,
            updated_at = NOW()`
	if _, err := s.db.Exec(ctx, q, partnerID, online); err != nil {
		return fmt.Errorf("set partner online flag: %w", err)
	}
	// Also keep the partners table in sync so existing queries that filter on
	// rider_partners.is_online (e.g. admin lists) see the same truth.
	const q2 = `UPDATE rider_partners SET is_online = $2, last_online_at = CASE WHEN $2 THEN NOW() ELSE last_online_at END, last_offline_at = CASE WHEN NOT $2 THEN NOW() ELSE last_offline_at END, updated_at = NOW() WHERE id = $1`
	if _, err := s.db.Exec(ctx, q2, partnerID, online); err != nil {
		return fmt.Errorf("sync partner online: %w", err)
	}
	return nil
}

// GetPartnerLocation returns the partner's last-known location.
func (s *Store) GetPartnerLocation(ctx context.Context, partnerID uuid.UUID) (*PartnerLocation, error) {
	const q = `
        SELECT partner_id, last_lat, last_lng, last_geohash, last_speed_mps, last_heading, is_online, updated_at
        FROM rider_partner_locations
        WHERE partner_id = $1`
	row := s.db.QueryRow(ctx, q, partnerID)
	var l PartnerLocation
	if err := row.Scan(&l.PartnerID, &l.LastLat, &l.LastLng, &l.LastGeohash, &l.LastSpeedMPS, &l.LastHeading, &l.IsOnline, &l.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPartnerLocationNotFound
		}
		return nil, fmt.Errorf("get partner location: %w", err)
	}
	return &l, nil
}

// FindOnlinePartnersByGeohash returns up to `limit` online partners whose
// last_geohash starts with any of the provided prefixes. This is the
// cold-path fallback used when Redis is unavailable.
func (s *Store) FindOnlinePartnersByGeohash(ctx context.Context, prefixes []string, limit int) ([]PartnerLocation, error) {
	if len(prefixes) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Build (last_geohash LIKE $i || '%') OR ... — pgx does not have a
	// native ANY LIKE operator. Each prefix is parameterized to keep this
	// SQL-injection safe.
	q := `
        SELECT partner_id, last_lat, last_lng, last_geohash, last_speed_mps, last_heading, is_online, updated_at
        FROM rider_partner_locations
        WHERE is_online = TRUE AND (`
	args := make([]any, 0, len(prefixes)+1)
	for i, p := range prefixes {
		if i > 0 {
			q += " OR "
		}
		q += fmt.Sprintf("last_geohash LIKE $%d", i+1)
		args = append(args, p+"%")
	}
	q += fmt.Sprintf(") ORDER BY updated_at DESC LIMIT $%d", len(prefixes)+1)
	args = append(args, limit)
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("find online partners: %w", err)
	}
	defer rows.Close()
	var out []PartnerLocation
	for rows.Next() {
		var l PartnerLocation
		if err := rows.Scan(&l.PartnerID, &l.LastLat, &l.LastLng, &l.LastGeohash, &l.LastSpeedMPS, &l.LastHeading, &l.IsOnline, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
