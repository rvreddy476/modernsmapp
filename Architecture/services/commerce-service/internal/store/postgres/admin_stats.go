package postgres

import (
	"context"
	"fmt"
	"time"
)

// AdminStats are the counts the MStore admin dashboard shows (admin console
// Wave 2). Each queue figure uses the same predicate as the queue it
// summarises, so a number on the dashboard matches the list behind it.
// Money is integer paise; nothing here is a float.
type AdminStats struct {
	// Work queues.
	SellersPending          int `json:"sellers_pending"`           // ListSellerQueue
	ProductsPending         int `json:"products_pending"`          // ListProductQueue
	KYCAwaitingVerification int `json:"kyc_awaiting_verification"` // in-flight or live sellers not yet 'verified'
	DeadLetterJobs          int `json:"dead_letter_jobs"`          // ListDeadLetterJobs
	ComplianceGapsOpen      int `json:"compliance_gaps_open"`      // ListOpenComplianceGaps

	// Money owed to sellers.
	PendingPayoutSellers     int   `json:"pending_payout_sellers"`      // ListPendingPayoutsBySeller rows
	PendingPayoutAmountPaise int64 `json:"pending_payout_amount_paise"` // SUM(net_amount) of pending remittances
	CODRemittancesPending    int   `json:"cod_remittances_pending"`     // rows awaiting settle

	// Volume. An order counts once it is past payment: not created,
	// payment_pending or cancelled. GMV is final_amount_minor of those orders.
	OrdersToday   int   `json:"orders_today"`
	Orders7d      int   `json:"orders_7d"`
	GMVTodayPaise int64 `json:"gmv_today_paise"`
	GMV7dPaise    int64 `json:"gmv_7d_paise"`

	// DayStartsAt is the start of "today": midnight in India (Asia/Kolkata).
	DayStartsAt time.Time `json:"day_starts_at"`
	GeneratedAt time.Time `json:"generated_at"`
}

// SellerQueueStatuses are the onboarding states ListSellerQueue shows.
var SellerQueueStatuses = []string{"submitted", "under_review", "changes_required"}

// ProductQueueStatuses are the approval states ListProductQueue shows.
var ProductQueueStatuses = []string{"submitted", "under_review"}

// KYCAwaitingStatuses: sellers still in the flow or approved whose documents
// have not been verified by a verifying adapter ('format_ok' is format-only).
var (
	KYCSellerStatuses       = []string{"submitted", "under_review", "changes_required", "approved"}
	KYCVerificationStatuses = []string{"pending", "format_ok"}
)

// OrderNotPlacedStatuses are excluded from order counts and GMV.
var OrderNotPlacedStatuses = []string{"created", "payment_pending", "cancelled"}

// AdminStats reads the dashboard counts in one round trip.
func (s *Store) AdminStats(ctx context.Context) (*AdminStats, error) {
	out := &AdminStats{}
	err := s.db.QueryRow(ctx, `
        WITH bounds AS (
            SELECT (date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') AS day_start,
                   now() - interval '7 days' AS week_start,
                   now() AS generated_at
        ),
        placed AS (
            SELECT o.created_at, COALESCE(o.final_amount_minor, 0) AS amount
              FROM orders o, bounds
             WHERE o.created_at >= bounds.week_start
               AND o.status <> ALL($5)
        ),
        pending_cod AS (
            SELECT seller_id, net_amount FROM cod_remittances WHERE status = 'pending'
        )
        SELECT
            (SELECT COUNT(*) FROM sellers WHERE status = ANY($1))::int,
            (SELECT COUNT(*) FROM products WHERE approval_status = ANY($2))::int,
            (SELECT COUNT(*) FROM sellers WHERE status = ANY($3) AND verification_status = ANY($4))::int,
            (SELECT COUNT(*) FROM fulfillment_jobs WHERE status = 'dead')::int,
            (SELECT COUNT(*) FROM product_compliance_gaps WHERE resolved_at IS NULL)::int,
            (SELECT COUNT(DISTINCT seller_id) FROM pending_cod)::int,
            (SELECT COALESCE(ROUND(SUM(net_amount) * 100), 0) FROM pending_cod)::bigint,
            (SELECT COUNT(*) FROM pending_cod)::int,
            (SELECT COUNT(*) FROM placed WHERE created_at >= bounds.day_start)::int,
            (SELECT COUNT(*) FROM placed)::int,
            (SELECT COALESCE(SUM(amount), 0) FROM placed WHERE created_at >= bounds.day_start)::bigint,
            (SELECT COALESCE(SUM(amount), 0) FROM placed)::bigint,
            bounds.day_start, bounds.generated_at
        FROM bounds`,
		SellerQueueStatuses, ProductQueueStatuses, KYCSellerStatuses, KYCVerificationStatuses,
		OrderNotPlacedStatuses).Scan(
		&out.SellersPending, &out.ProductsPending, &out.KYCAwaitingVerification,
		&out.DeadLetterJobs, &out.ComplianceGapsOpen,
		&out.PendingPayoutSellers, &out.PendingPayoutAmountPaise, &out.CODRemittancesPending,
		&out.OrdersToday, &out.Orders7d, &out.GMVTodayPaise, &out.GMV7dPaise,
		&out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("admin stats: %w", err)
	}
	return out, nil
}
