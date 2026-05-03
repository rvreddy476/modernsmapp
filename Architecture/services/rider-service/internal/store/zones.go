package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ListZonesByCity returns every active zone for the given city.
func (s *Store) ListZonesByCity(ctx context.Context, cityID uuid.UUID) ([]Zone, error) {
	const q = `
        SELECT id, city_id, name, is_active, created_at, updated_at
        FROM rider_zones
        WHERE city_id = $1 AND is_active = TRUE
        ORDER BY name ASC`
	rows, err := s.db.Query(ctx, q, cityID)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()
	var out []Zone
	for rows.Next() {
		var z Zone
		if err := rows.Scan(&z.ID, &z.CityID, &z.Name, &z.IsActive, &z.CreatedAt, &z.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan zone: %w", err)
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// CountZones returns total zone count. Used in tests.
func (s *Store) CountZones(ctx context.Context) (int, error) {
	const q = `SELECT count(*) FROM rider_zones`
	var n int
	if err := s.db.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count zones: %w", err)
	}
	return n, nil
}
