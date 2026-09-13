package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotBatchOffer tells the service an offer is a single-order offer, so it
// should take the single-order accept path.
var ErrNotBatchOffer = errors.New("offer is not a batch offer")

// GetBatchForOrderForPartner returns the batch for an order only to the
// delivery partner assigned to that order. Anyone else gets pgx.ErrNoRows,
// the same answer as an order that is not batched.
func (s *Store) GetBatchForOrderForPartner(ctx context.Context, userID, orderID uuid.UUID) (*DeliveryBatch, error) {
	var b DeliveryBatch
	if err := s.db.QueryRow(ctx, `
		SELECT b.id, b.restaurant_id, b.status, b.created_at::text,
			b.assigned_at::text, b.completed_at::text
		FROM food.delivery_batches b
		JOIN food.delivery_assignments da ON da.batch_id = b.id
		JOIN food.delivery_partners dp ON dp.id = da.delivery_partner_id
		WHERE da.order_id = $1 AND dp.user_id = $2
	`, orderID, userID).Scan(&b.ID, &b.RestaurantID, &b.Status, &b.CreatedAt, &b.AssignedAt, &b.CompletedAt); err != nil {
		return nil, err
	}
	members, err := s.batchMembers(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	b.Members = members
	return &b, nil
}

func (s *Store) batchMembers(ctx context.Context, batchID uuid.UUID) ([]BatchMember, error) {
	rows, err := s.db.Query(ctx, `
		SELECT order_id, COALESCE(batch_sequence, 0)
		FROM food.delivery_assignments
		WHERE batch_id = $1 ORDER BY batch_sequence ASC
	`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BatchMember
	for rows.Next() {
		var m BatchMember
		if err := rows.Scan(&m.OrderID, &m.Sequence); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
