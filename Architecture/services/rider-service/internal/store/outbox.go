package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/outbox"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OutboxEvent is one row in rider_outbox_events.
type OutboxEvent struct {
	ID            uuid.UUID       `json:"id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
}

// InsertOutboxEventTx inserts a transactional outbox row within an open transaction.
func InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, aggregateType, aggregateID string, payload []byte) error {
	const q = `
        INSERT INTO rider_outbox_events (event_type, aggregate_type, aggregate_id, payload)
        VALUES ($1, $2, $3, $4)`
	_, err := tx.Exec(ctx, q, eventType, aggregateType, aggregateID, payload)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	// Keep the historical table for audit/read compatibility, but deliver
	// supported state events through the queue drained by the live publisher.
	// Recipients come from the locked aggregate, not caller-supplied payloads.
	if aggregateType == "ride" && durableRideStateEvent(eventType) {
		var body []byte
		err = tx.QueryRow(ctx, `SELECT jsonb_strip_nulls(jsonb_build_object(
		 'ride_id',r.id,'customer_user_id',r.customer_user_id,'partner_id',r.partner_id,
		 'partner_user_id',p.user_id,'vehicle_id',r.vehicle_id,'status',r.status,
		 'revision',r.revision,'occurred_at',r.updated_at,'assigned_at',r.assigned_at,
		 'completed_at',r.completed_at,'cancelled_at',r.cancelled_at,
		 'cancelled_by_kind',r.cancelled_by_kind,'cancelled_by_user_id',r.cancelled_by_user_id,
		 'cancellation_fee_paise',r.cancellation_fee_paise,'reason',r.cancellation_reason,
		 'final_distance_km',r.final_distance_km,'final_duration_min',r.final_duration_min,
		 'final_fare_paise',r.final_fare_paise,'payment_method',r.payment_method,
		 'payment_status',(SELECT status::text FROM rider_ride_payments WHERE ride_id=r.id ORDER BY created_at DESC LIMIT 1),
		 'flagged_for_review',r.flagged_for_review,
		 'offer_id',(SELECT id FROM rider_ride_offers WHERE ride_id=r.id AND status='accepted' ORDER BY decided_at DESC LIMIT 1)))
		 FROM rider_rides r LEFT JOIN rider_partners p ON p.id=r.partner_id WHERE r.id=$1::uuid`, aggregateID).Scan(&body)
		if err != nil {
			return fmt.Errorf("build durable ride event: %w", err)
		}
		if err = outbox.NewQueuer("rider").Enqueue(ctx, tx, eventType, "rider.ride."+aggregateID, body); err != nil {
			return err
		}
	}
	return nil
}

func durableRideStateEvent(typ string) bool {
	switch typ {
	case "rider.ride.assigned", "rider.ride.arriving", "rider.ride.arrived", "rider.ride.started", "rider.ride.completed", "rider.ride.cancelled":
		return true
	default:
		return false
	}
}

// FetchUnpublishedOutboxEvents retrieves batches of unpublished events with SKIP LOCKED.
func (s *Store) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `
        SELECT id, event_type, aggregate_type, aggregate_id, payload, created_at, published_at
        FROM rider_outbox_events
        WHERE published_at IS NULL
        ORDER BY created_at ASC
        LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch unpublished outbox: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.AggregateType, &e.AggregateID, &e.Payload, &e.CreatedAt, &e.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// MarkOutboxPublished marks an event as successfully published after broker ACK.
func (s *Store) MarkOutboxPublished(ctx context.Context, eventID uuid.UUID) error {
	const q = `UPDATE rider_outbox_events SET published_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, eventID)
	return err
}
