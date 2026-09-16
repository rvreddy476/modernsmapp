package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// writeAdminAudit appends one food.admin_audit_logs row inside the caller's
// transaction, so the change and its audit commit or roll back together.
// actor is the acting admin: the X-User-Id of a legacy admin request, or the
// signed act claim of an admin-service token (never a header on that path).
func writeAdminAudit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action, entityType string, entityID *uuid.UUID, newValue map[string]any) error {
	if actor == uuid.Nil {
		return fmt.Errorf("admin audit %s: actor is required", action)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, $2, $3, $4, $5)
	`, actor, action, entityType, entityID, newValue); err != nil {
		return fmt.Errorf("admin audit %s: %w", action, err)
	}
	return nil
}

// AdminStats are the counts the Feast admin dashboard shows (admin console
// Wave 2). The first seven reuse the predicates of the existing dashboard
// (AdminDashboard); "today" here starts at midnight India time rather than
// the database session's CURRENT_DATE. Money is integer paise.
type AdminStats struct {
	// Volume today.
	OrdersToday          int   `json:"orders_today"`
	GMVTodayPaise        int64 `json:"gmv_today_paise"`
	CancelledOrdersToday int   `json:"cancelled_orders_today"`

	// Supply.
	RestaurantsActive             int `json:"restaurants_active"`
	RestaurantsPendingReview      int `json:"restaurants_pending_review"`
	DeliveryPartnersPendingReview int `json:"delivery_partners_pending_review"`
	DeliveryPartnersOnline        int `json:"delivery_partners_online"`

	// Work queues.
	RestaurantDocumentsPending int `json:"restaurant_documents_pending"`
	DeliveryDocumentsPending   int `json:"delivery_documents_pending"`
	RefundRequestsPending      int `json:"refund_requests_pending"`
	RefundsInFlight            int `json:"refunds_in_flight"`
	TicketsOpen                int `json:"tickets_open"`

	// Settlements awaiting payment (PENDING or PROCESSING).
	RestaurantSettlementsUnpaid      int   `json:"restaurant_settlements_unpaid"`
	RestaurantSettlementsUnpaidPaise int64 `json:"restaurant_settlements_unpaid_paise"`
	DeliverySettlementsUnpaid        int   `json:"delivery_settlements_unpaid"`
	DeliverySettlementsUnpaidPaise   int64 `json:"delivery_settlements_unpaid_paise"`

	DayStartsAt time.Time `json:"day_starts_at"`
	GeneratedAt time.Time `json:"generated_at"`
}

// AdminStats reads the dashboard counts in one round trip.
func (s *Store) AdminStats(ctx context.Context) (*AdminStats, error) {
	out := &AdminStats{}
	err := s.db.QueryRow(ctx, `
		WITH bounds AS (
			SELECT (date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') AS day_start,
				now() AS generated_at
		)
		SELECT
			(SELECT COUNT(*) FROM food.orders, bounds WHERE placed_at >= bounds.day_start)::int,
			(SELECT COALESCE(SUM(COALESCE(final_amount_paise, ROUND(final_amount * 100)::bigint)), 0)
				FROM food.orders, bounds WHERE placed_at >= bounds.day_start)::bigint,
			(SELECT COUNT(*) FROM food.orders, bounds WHERE placed_at >= bounds.day_start AND status::text LIKE 'CANCELLED%')::int,
			(SELECT COUNT(*) FROM food.restaurants WHERE status = 'ACTIVE')::int,
			(SELECT COUNT(*) FROM food.restaurants WHERE status = 'PENDING_REVIEW')::int,
			(SELECT COUNT(*) FROM food.delivery_partners WHERE status = 'PENDING_REVIEW')::int,
			(SELECT COUNT(*) FROM food.delivery_partners WHERE is_online = TRUE)::int,
			(SELECT COUNT(*) FROM food.restaurant_documents WHERE status = 'PENDING')::int,
			(SELECT COUNT(*) FROM food.delivery_partner_documents WHERE status = 'PENDING')::int,
			(SELECT COUNT(*) FROM food.refund_requests WHERE status = 'requested')::int,
			(SELECT COUNT(*) FROM food.refunds WHERE status IN ('PENDING', 'SUBMITTED'))::int,
			(SELECT COUNT(*) FROM food.support_tickets WHERE status IN ('open', 'in_progress'))::int,
			(SELECT COUNT(*) FROM food.restaurant_settlements WHERE status IN ('PENDING', 'PROCESSING'))::int,
			(SELECT COALESCE(SUM(ROUND(payout_amount * 100)), 0) FROM food.restaurant_settlements WHERE status IN ('PENDING', 'PROCESSING'))::bigint,
			(SELECT COUNT(*) FROM food.delivery_partner_settlements WHERE status IN ('PENDING', 'PROCESSING'))::int,
			(SELECT COALESCE(SUM(ROUND(payout_amount * 100)), 0) FROM food.delivery_partner_settlements WHERE status IN ('PENDING', 'PROCESSING'))::bigint,
			bounds.day_start, bounds.generated_at
		FROM bounds`).Scan(
		&out.OrdersToday, &out.GMVTodayPaise, &out.CancelledOrdersToday,
		&out.RestaurantsActive, &out.RestaurantsPendingReview, &out.DeliveryPartnersPendingReview, &out.DeliveryPartnersOnline,
		&out.RestaurantDocumentsPending, &out.DeliveryDocumentsPending, &out.RefundRequestsPending, &out.RefundsInFlight,
		&out.TicketsOpen,
		&out.RestaurantSettlementsUnpaid, &out.RestaurantSettlementsUnpaidPaise,
		&out.DeliverySettlementsUnpaid, &out.DeliverySettlementsUnpaidPaise,
		&out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("admin stats: %w", err)
	}
	return out, nil
}
