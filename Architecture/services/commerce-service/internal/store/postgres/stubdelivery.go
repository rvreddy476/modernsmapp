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
