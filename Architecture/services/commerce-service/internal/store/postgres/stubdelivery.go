package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// OrdersDueForStubDelivery lists orders sitting in `status` (shipped or
// out_for_delivery) for longer than `after`, measured from the history row
// that put them there — or, for an order shipped before the history rows
// existed, from its last update. Feeds the stub courier's delivery timer
// (service.RunStubAutoDelivery); a real courier never reads it.
func (s *Store) OrdersDueForStubDelivery(ctx context.Context, status string, after time.Duration, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT o.id
		  FROM orders o
		 WHERE o.status = $1
		   -- Only orders the stub courier booked. An order a real courier
		   -- booked (or one with no shipment) is moved by that courier's
		   -- webhooks or not at all: on 30 Sep 2026 the first dev sweep
		   -- moved a live Shiprocket order and 61 old test orders.
		   AND EXISTS (SELECT 1 FROM shipments sh WHERE sh.order_id = o.id)
		   AND NOT EXISTS (SELECT 1 FROM shipments sh
		                    WHERE sh.order_id = o.id AND LOWER(sh.courier) <> 'stub')
		   AND COALESCE(
		         (SELECT MAX(h.created_at) FROM order_status_history h
		           WHERE h.order_id = o.id AND h.to_status = $1),
		         o.updated_at) < NOW() - make_interval(secs => $2)
		 ORDER BY o.updated_at ASC
		 LIMIT $3`, status, after.Seconds(), limit)
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
