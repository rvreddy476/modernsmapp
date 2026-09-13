package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AutoOfflineStaleDeliveryPartners turns off every online partner who has
// sent no location ping for `silence`. The partner's last sign of life is the
// newest of: their latest location ping, their latest go-online toggle, and
// the row's creation.
//
// A partner with no job goes OFFLINE (status and is_online, exactly what the
// availability toggle writes). A partner still carrying an order keeps status
// ACTIVE so they can finish it (the step buttons require ACTIVE), but
// is_online goes false so dispatch offers them nothing new. Each change is
// recorded in delivery_partner_availability with changed_by NULL.
func (s *Store) AutoOfflineStaleDeliveryPartners(ctx context.Context, silence time.Duration) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		WITH stale AS (
			SELECT dp.id,
				EXISTS (
					SELECT 1
					FROM food.delivery_assignments da
					JOIN food.orders o ON o.id = da.order_id
					WHERE da.delivery_partner_id = dp.id
					  AND da.status NOT IN ('CREATED', 'DELIVERED', 'FAILED', 'CANCELLED', 'REJECTED')
					  AND o.status IN ('DELIVERY_ASSIGNED', 'PICKED_UP', 'OUT_FOR_DELIVERY')
				) AS on_job
			FROM food.delivery_partners dp
			WHERE dp.is_online = TRUE
			  AND GREATEST(
					dp.created_at,
					COALESCE((SELECT MAX(l.recorded_at) FROM food.delivery_partner_locations l
					          WHERE l.delivery_partner_id = dp.id), '-infinity'::timestamptz),
					COALESCE((SELECT MAX(a.created_at) FROM food.delivery_partner_availability a
					          WHERE a.delivery_partner_id = dp.id AND a.is_online), '-infinity'::timestamptz)
				) < NOW() - make_interval(secs => $1::float8)
			FOR UPDATE OF dp SKIP LOCKED
		), updated AS (
			UPDATE food.delivery_partners dp
			SET is_online = FALSE,
				status = CASE WHEN dp.status = 'ACTIVE' AND NOT stale.on_job
					THEN 'OFFLINE'::food.delivery_partner_status ELSE dp.status END
			FROM stale
			WHERE dp.id = stale.id
			RETURNING dp.id
		), logged AS (
			INSERT INTO food.delivery_partner_availability (delivery_partner_id, is_online, reason)
			SELECT id, FALSE, 'auto-offline: no location ping' FROM updated
		)
		SELECT id FROM updated
	`, silence.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// LocationPurgeResult counts what one purge pass removed.
type LocationPurgeResult struct {
	Locations     int64
	TrackingPings int64
}

// PurgeDeliveryLocationHistory deletes rider location history older than
// `olderThan`: the raw pings in delivery_partner_locations and the per-ping
// "location update" rows in delivery_tracking_events. Status-change tracking
// rows are kept. Deletes run in batches so one pass never holds a long lock.
func (s *Store) PurgeDeliveryLocationHistory(ctx context.Context, olderThan time.Duration, batch int) (LocationPurgeResult, error) {
	if batch <= 0 {
		batch = 5000
	}
	var out LocationPurgeResult
	purge := func(sql string, total *int64) error {
		for {
			tag, err := s.db.Exec(ctx, sql, olderThan.Seconds(), batch)
			if err != nil {
				return err
			}
			*total += tag.RowsAffected()
			if tag.RowsAffected() < int64(batch) {
				return nil
			}
		}
	}
	if err := purge(`
		DELETE FROM food.delivery_partner_locations
		WHERE id IN (
			SELECT id FROM food.delivery_partner_locations
			WHERE recorded_at < NOW() - make_interval(secs => $1::float8)
			LIMIT $2
		)
	`, &out.Locations); err != nil {
		return out, err
	}
	if err := purge(`
		DELETE FROM food.delivery_tracking_events
		WHERE id IN (
			SELECT id FROM food.delivery_tracking_events
			WHERE note = 'location update' AND created_at < NOW() - make_interval(secs => $1::float8)
			LIMIT $2
		)
	`, &out.TrackingPings); err != nil {
		return out, err
	}
	return out, nil
}
