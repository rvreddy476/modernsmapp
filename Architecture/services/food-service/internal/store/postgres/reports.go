package postgres

import (
	"context"
	"fmt"
	"time"
)

// ReportWindow is the canonical {from, to} input every admin report
// takes. `from` defaults to 24h ago when zero.
type ReportWindow struct {
	From time.Time
	To   time.Time
}

func (w *ReportWindow) defaults() {
	if w.To.IsZero() {
		w.To = time.Now().UTC()
	}
	if w.From.IsZero() {
		w.From = w.To.Add(-24 * time.Hour)
	}
}

// RestaurantSLAReport is the per-restaurant accept-SLA summary.
type RestaurantSLAReport struct {
	RestaurantID     string  `json:"restaurant_id"`
	RestaurantName   string  `json:"restaurant_name"`
	OrdersTotal      int     `json:"orders_total"`
	OrdersBreached   int     `json:"orders_breached"`
	BreachedPct      float64 `json:"breached_pct"`
	AvgAcceptSeconds *int    `json:"avg_accept_seconds,omitempty"`
}

// ReportRestaurantSLA returns each restaurant's accept-SLA performance
// over the window.
func (s *Store) ReportRestaurantSLA(ctx context.Context, w ReportWindow) ([]RestaurantSLAReport, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		SELECT
			r.id,
			r.name,
			COUNT(o.id)::int AS orders_total,
			COUNT(*) FILTER (WHERE o.cancellation_reason LIKE 'sla_breach%')::int AS breached,
			ROUND(100.0 * COUNT(*) FILTER (WHERE o.cancellation_reason LIKE 'sla_breach%') / NULLIF(COUNT(o.id), 0), 2)::float8 AS breached_pct,
			-- avg seconds from CONFIRMED (placed_at proxy) to first non-CONFIRMED
			-- history transition. Light-weight; production replaces with
			-- a window function over order_status_history.
			NULL::int AS avg_accept_seconds
		FROM food.restaurants r
		LEFT JOIN food.orders o
			ON o.restaurant_id = r.id AND o.placed_at BETWEEN $1 AND $2
		GROUP BY r.id, r.name
		HAVING COUNT(o.id) > 0
		ORDER BY breached_pct DESC NULLS LAST
		LIMIT 200
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("restaurant sla report: %w", err)
	}
	defer rows.Close()
	var out []RestaurantSLAReport
	for rows.Next() {
		var r RestaurantSLAReport
		if err := rows.Scan(&r.RestaurantID, &r.RestaurantName, &r.OrdersTotal,
			&r.OrdersBreached, &r.BreachedPct, &r.AvgAcceptSeconds); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeliverySLAReport is a delivery-side performance breakdown.
type DeliverySLAReport struct {
	PartnerID        string   `json:"partner_id"`
	PartnerName      string   `json:"partner_name"`
	DeliveriesTotal  int      `json:"deliveries_total"`
	LateCount        int      `json:"late_count"`
	LatePct          float64  `json:"late_pct"`
	AvgDeliveryMins  *float64 `json:"avg_delivery_minutes,omitempty"`
}

// ReportDeliverySLA computes late-delivery stats per delivery partner.
// A delivery is "late" when delivered_at - placed_at > expected (30 min
// floor for v1; spec'd per restaurant in a follow-up).
func (s *Store) ReportDeliverySLA(ctx context.Context, w ReportWindow) ([]DeliverySLAReport, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		WITH base AS (
			SELECT
				dp.id AS partner_id,
				dp.full_name,
				da.order_id,
				o.placed_at,
				da.delivered_at,
				EXTRACT(EPOCH FROM (da.delivered_at - o.placed_at)) / 60.0 AS delivery_minutes
			FROM food.delivery_partners dp
			JOIN food.delivery_assignments da ON da.delivery_partner_id = dp.id
			JOIN food.orders o ON o.id = da.order_id
			WHERE da.delivered_at IS NOT NULL
			  AND da.delivered_at BETWEEN $1 AND $2
		)
		SELECT
			partner_id::text,
			full_name,
			COUNT(*)::int AS deliveries_total,
			COUNT(*) FILTER (WHERE delivery_minutes > 30)::int AS late_count,
			ROUND(100.0 * COUNT(*) FILTER (WHERE delivery_minutes > 30) / NULLIF(COUNT(*), 0), 2)::float8 AS late_pct,
			ROUND(AVG(delivery_minutes)::numeric, 2)::float8 AS avg_delivery_minutes
		FROM base
		GROUP BY partner_id, full_name
		ORDER BY late_pct DESC NULLS LAST
		LIMIT 200
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("delivery sla report: %w", err)
	}
	defer rows.Close()
	var out []DeliverySLAReport
	for rows.Next() {
		var r DeliverySLAReport
		if err := rows.Scan(&r.PartnerID, &r.PartnerName, &r.DeliveriesTotal,
			&r.LateCount, &r.LatePct, &r.AvgDeliveryMins); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PaymentReconRow is a per-method/per-status reconciliation summary.
type PaymentReconRow struct {
	PaymentMethod string  `json:"payment_method"`
	PaymentStatus string  `json:"payment_status"`
	Count         int     `json:"count"`
	GrossAmount   float64 `json:"gross_amount"`
}

func (s *Store) ReportPaymentRecon(ctx context.Context, w ReportWindow) ([]PaymentReconRow, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		SELECT
			payment_method::text,
			payment_status::text,
			COUNT(*)::int,
			SUM(final_amount)::float8
		FROM food.orders
		WHERE placed_at BETWEEN $1 AND $2
		GROUP BY payment_method, payment_status
		ORDER BY payment_method, payment_status
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("payment recon: %w", err)
	}
	defer rows.Close()
	var out []PaymentReconRow
	for rows.Next() {
		var r PaymentReconRow
		if err := rows.Scan(&r.PaymentMethod, &r.PaymentStatus, &r.Count, &r.GrossAmount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RefundCancelRow is a categorical refund + cancellation breakdown.
type RefundCancelRow struct {
	Category string  `json:"category"`
	Count    int     `json:"count"`
	Amount   float64 `json:"amount"`
}

func (s *Store) ReportRefundsCancellations(ctx context.Context, w ReportWindow) ([]RefundCancelRow, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		SELECT
			COALESCE(cancellation_reason, status::text) AS category,
			COUNT(*)::int,
			COALESCE(SUM(final_amount), 0)::float8
		FROM food.orders
		WHERE placed_at BETWEEN $1 AND $2
		  AND status IN ('CANCELLED_BY_CUSTOMER','CANCELLED_BY_RESTAURANT','CANCELLED_BY_ADMIN','RESTAURANT_REJECTED','REFUNDED','REFUND_PENDING')
		GROUP BY category
		ORDER BY count DESC
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("refunds report: %w", err)
	}
	defer rows.Close()
	var out []RefundCancelRow
	for rows.Next() {
		var r RefundCancelRow
		if err := rows.Scan(&r.Category, &r.Count, &r.Amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CouponAbuseRow flags customers exceeding the per-user coupon use cap.
type CouponAbuseRow struct {
	CustomerID  string `json:"customer_id"`
	CouponCode  string `json:"coupon_code"`
	UseCount    int    `json:"use_count"`
}

func (s *Store) ReportCouponAbuse(ctx context.Context, w ReportWindow, threshold int) ([]CouponAbuseRow, error) {
	w.defaults()
	if threshold <= 0 {
		threshold = 5
	}
	rows, err := s.db.Query(ctx, `
		SELECT
			user_id::text,
			coupon_code,
			COUNT(*)::int AS uses
		FROM food.orders
		WHERE coupon_code IS NOT NULL
		  AND placed_at BETWEEN $1 AND $2
		GROUP BY user_id, coupon_code
		HAVING COUNT(*) >= $3
		ORDER BY uses DESC
		LIMIT 200
	`, w.From, w.To, threshold)
	if err != nil {
		return nil, fmt.Errorf("coupon abuse: %w", err)
	}
	defer rows.Close()
	var out []CouponAbuseRow
	for rows.Next() {
		var r CouponAbuseRow
		if err := rows.Scan(&r.CustomerID, &r.CouponCode, &r.UseCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ComplianceReportRow flags restaurants with expired or missing docs.
type ComplianceReportRow struct {
	RestaurantID    string  `json:"restaurant_id"`
	RestaurantName  string  `json:"restaurant_name"`
	HasApprovedFSSAI bool   `json:"has_approved_fssai"`
	ExpiredDocs     int     `json:"expired_docs"`
	OldestDocExpiry *string `json:"oldest_doc_expiry,omitempty"`
}

func (s *Store) ReportCompliance(ctx context.Context) ([]ComplianceReportRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			r.id::text, r.name,
			EXISTS(
				SELECT 1 FROM food.restaurant_documents
				WHERE restaurant_id = r.id
				  AND lower(document_type) LIKE '%fssai%'
				  AND status = 'APPROVED'
				  AND (expires_at IS NULL OR expires_at > NOW())
			) AS has_fssai,
			COALESCE((SELECT COUNT(*) FROM food.restaurant_documents
				WHERE restaurant_id = r.id
				  AND status = 'APPROVED'
				  AND expires_at IS NOT NULL
				  AND expires_at <= NOW()), 0)::int AS expired,
			(SELECT MIN(expires_at)::text FROM food.restaurant_documents
				WHERE restaurant_id = r.id AND status = 'APPROVED'
				  AND expires_at IS NOT NULL) AS oldest_expiry
		FROM food.restaurants r
		WHERE r.status IN ('APPROVED','ACTIVE')
		ORDER BY expired DESC, r.name
	`)
	if err != nil {
		return nil, fmt.Errorf("compliance report: %w", err)
	}
	defer rows.Close()
	var out []ComplianceReportRow
	for rows.Next() {
		var r ComplianceReportRow
		if err := rows.Scan(&r.RestaurantID, &r.RestaurantName,
			&r.HasApprovedFSSAI, &r.ExpiredDocs, &r.OldestDocExpiry); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
