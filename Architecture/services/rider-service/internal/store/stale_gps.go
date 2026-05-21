package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ForceOfflineStaleGPS finds online partners whose last location ping
// is older than `staleAfter` and flips them offline. Returns the
// affected partner ids so the caller can publish partner.offline
// events.
func (s *Store) ForceOfflineStaleGPS(ctx context.Context, staleAfter time.Duration) ([]uuid.UUID, error) {
	const q = `
		WITH stale AS (
			SELECT partner_id FROM rider_partner_locations
			WHERE is_online = TRUE AND updated_at < NOW() - $1::interval
			ORDER BY updated_at ASC
			LIMIT 100
			FOR UPDATE SKIP LOCKED
		),
		updated AS (
			UPDATE rider_partner_locations
			SET is_online = FALSE, updated_at = NOW()
			WHERE partner_id IN (SELECT partner_id FROM stale)
			RETURNING partner_id
		),
		bumped AS (
			UPDATE rider_partners
			SET is_online = FALSE
			WHERE id IN (SELECT partner_id FROM updated)
			RETURNING id
		)
		SELECT id FROM bumped
	`
	rows, err := s.db.Query(ctx, q, staleAfter.String())
	if err != nil {
		return nil, fmt.Errorf("force offline stale: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
