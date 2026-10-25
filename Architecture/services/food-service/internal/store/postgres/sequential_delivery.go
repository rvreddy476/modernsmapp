package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// MaxPendingDeliveryOffers is shared by candidate discovery and the locked
// writer. Offers may be compared, but acceptance still permits one active job.
const MaxPendingDeliveryOffers = 3

// CreateSequentialDeliveryOffer returns only newly created offers. Job locks
// serialize replicas; the partner lock enforces the concurrent offer cap.
// Nil means this candidate was tried, is busy, or the job already has an offer.
func (s *Store) CreateSequentialDeliveryOffer(ctx context.Context, orderID, partnerID uuid.UUID, batchID *uuid.UUID, distanceKM float64) (*DeliveryOffer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if batchID != nil {
		var status string
		if err = tx.QueryRow(ctx, `SELECT status FROM food.delivery_batches WHERE id=$1 FOR UPDATE`, *batchID).Scan(&status); err != nil {
			return nil, err
		}
		if status != "pending" {
			return nil, nil
		}
		rows, err := tx.Query(ctx, `SELECT o.id,o.status::text FROM food.orders o JOIN food.delivery_assignments a ON a.order_id=o.id WHERE a.batch_id=$1 ORDER BY o.id FOR UPDATE OF o`, *batchID)
		if err != nil {
			return nil, err
		}
		valid, anchor, count := true, false, 0
		for rows.Next() {
			var id uuid.UUID
			var st string
			if err = rows.Scan(&id, &st); err != nil {
				rows.Close()
				return nil, err
			}
			count++
			valid = valid && st == "DELIVERY_ASSIGNING"
			anchor = anchor || id == orderID
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
		if !valid || !anchor || count == 0 {
			return nil, nil
		}
	} else {
		var status string
		if err = tx.QueryRow(ctx, `SELECT status::text FROM food.orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&status); err != nil {
			return nil, err
		}
		if status != "DELIVERY_ASSIGNING" {
			return nil, nil
		}
		var batched bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.delivery_assignments WHERE order_id=$1 AND batch_id IS NOT NULL)`, orderID).Scan(&batched); err != nil {
			return nil, err
		}
		if batched {
			return nil, nil
		}
	}
	var live bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.delivery_offers WHERE (order_id=$1 OR batch_id=$2) AND status='pending' AND expires_at>clock_timestamp())`, orderID, batchID).Scan(&live); err != nil {
		return nil, err
	}
	if live {
		return nil, nil
	}
	var userID uuid.UUID
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT user_id,status='ACTIVE' AND is_online FROM food.delivery_partners WHERE id=$1 FOR UPDATE`, partnerID).Scan(&userID, &eligible); err != nil {
		return nil, err
	}
	if !eligible {
		return nil, nil
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM food.delivery_offers WHERE delivery_partner_id=$1 AND status='pending' AND expires_at>clock_timestamp()) >= $2 OR
  EXISTS(SELECT 1 FROM food.delivery_assignments WHERE delivery_partner_id=$1 AND status IN ('ASSIGNED','ACCEPTED','ARRIVED_AT_RESTAURANT','PICKED_UP','ARRIVED_AT_CUSTOMER'))`, partnerID, MaxPendingDeliveryOffers).Scan(&live); err != nil {
		return nil, err
	}
	if live {
		return nil, nil
	}
	var o DeliveryOffer
	var expires, created time.Time
	err = tx.QueryRow(ctx, `INSERT INTO food.delivery_offers(order_id,delivery_partner_id,batch_id,expires_at,distance_km)
  VALUES($1,$2,$3,clock_timestamp()+interval '20 seconds',$4) ON CONFLICT(order_id,delivery_partner_id) DO NOTHING
  RETURNING id,order_id,delivery_partner_id,status::text,distance_km::float8,expires_at,responded_at::text,reject_reason,created_at`, orderID, partnerID, batchID, distanceKM).Scan(&o.ID, &o.OrderID, &o.DeliveryPartnerID, &o.Status, &o.DistanceKM, &expires, &o.RespondedAt, &o.RejectReason, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if partnerwork.CandidateUnavailable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.ExpiresAt = expires.UTC().Format(time.RFC3339Nano)
	o.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(map[string]any{"offer": o, "batch_id": batchID, "is_batch": batchID != nil, "delivery_partner_user_id": userID})
	if err != nil {
		return nil, err
	}
	if err = foodOutbox.Enqueue(ctx, tx, "food.delivery.offered", "food.delivery_partner."+userID.String()+".assignments", body); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &o, nil
}

// Retries retain the original batch and stop sequence.
type PendingDeliveryBatch struct {
	BatchID uuid.UUID
	Anchor  ReadyOrderForBatching
}

func (s *Store) ListPendingDeliveryBatches(ctx context.Context, limit int) ([]PendingDeliveryBatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.Query(ctx, `SELECT b.id,a.order_id,b.restaurant_id,o.placed_at,r.latitude::float8,r.longitude::float8
  FROM food.delivery_batches b JOIN food.restaurants r ON r.id=b.restaurant_id
  JOIN LATERAL(SELECT order_id FROM food.delivery_assignments WHERE batch_id=b.id ORDER BY batch_sequence,order_id LIMIT 1)a ON TRUE
  JOIN food.orders o ON o.id=a.order_id
  WHERE b.status='pending' AND o.status='DELIVERY_ASSIGNING'
  AND NOT EXISTS(SELECT 1 FROM food.delivery_offers f WHERE f.batch_id=b.id AND f.status='pending' AND f.expires_at>clock_timestamp())
  ORDER BY b.created_at,b.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PendingDeliveryBatch
	for rows.Next() {
		var b PendingDeliveryBatch
		if err = rows.Scan(&b.BatchID, &b.Anchor.OrderID, &b.Anchor.RestaurantID, &b.Anchor.PlacedAt, &b.Anchor.RestaurantLat, &b.Anchor.RestaurantLng); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func lockAvailableDeliveryPartner(ctx context.Context, tx pgx.Tx, partnerID uuid.UUID) error {
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT status='ACTIVE' AND is_online FROM food.delivery_partners WHERE id=$1 FOR UPDATE`, partnerID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrDeliveryPartnerNotActive
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.delivery_assignments WHERE delivery_partner_id=$1 AND status IN ('ASSIGNED','ACCEPTED','ARRIVED_AT_RESTAURANT','PICKED_UP','ARRIVED_AT_CUSTOMER'))`, partnerID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return fmt.Errorf("partner already has active delivery work")
	}
	return nil
}
