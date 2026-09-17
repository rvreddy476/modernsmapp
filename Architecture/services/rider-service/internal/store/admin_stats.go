package store

import (
	"context"
	"fmt"
	"time"
)

// AdminStats are the counts the Mopedu admin dashboard shows (admin console
// Wave 2). Predicates match the existing dashboard (AdminDashboardCounts)
// and the open-complaint / open-incident counters; "today" starts at
// midnight India time rather than the database session's date. Money is
// integer paise: Mopedu's revenue is partner subscriptions (customers ride
// free), so revenue today is the subscription payments verified today.
type AdminStats struct {
	// Review queues.
	PartnersPendingReview        int `json:"partners_pending_review"`
	DocumentsPending             int `json:"documents_pending"`
	VehiclesPending              int `json:"vehicles_pending"`
	PaymentsAwaitingVerification int `json:"payments_awaiting_verification"`

	// Rides.
	RidesToday         int `json:"rides_today"`
	RidesLast7Days     int `json:"rides_last_7_days"`
	LiveRidesNow       int `json:"live_rides_now"`
	CancellationsToday int `json:"cancellations_today"`

	// Support and safety.
	OpenComplaints      int `json:"open_complaints"`
	OpenSafetyIncidents int `json:"open_safety_incidents"`

	RevenueTodayPaise int64 `json:"revenue_today_paise"`

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
			(SELECT COUNT(*) FROM rider_partners WHERE status = 'pending_verification' AND deleted_at IS NULL)::int,
			(SELECT COUNT(*) FROM rider_partner_documents WHERE status = 'pending')::int,
			(SELECT COUNT(*) FROM rider_vehicles WHERE status = 'pending' AND deleted_at IS NULL)::int,
			(SELECT COUNT(*) FROM rider_subscription_payments WHERE status IN ('pending','submitted'))::int,
			(SELECT COUNT(*) FROM rider_rides, bounds WHERE created_at >= bounds.day_start)::int,
			(SELECT COUNT(*) FROM rider_rides, bounds WHERE created_at >= bounds.generated_at - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM rider_rides WHERE status IN ('requested','searching_partner','partner_assigned','partner_arriving','arrived','otp_verified','in_progress'))::int,
			(SELECT COUNT(*) FROM rider_rides, bounds WHERE status::text LIKE 'cancelled_%' AND cancelled_at >= bounds.day_start)::int,
			(SELECT COUNT(*) FROM rider_complaints WHERE status IN ('open','under_review'))::int,
			(SELECT COUNT(*) FROM rider_safety_incidents WHERE status IN ('open','acknowledged'))::int,
			(SELECT COALESCE(SUM(ROUND(amount * 100)), 0) FROM rider_subscription_payments, bounds
				WHERE status = 'verified' AND verified_at >= bounds.day_start)::bigint,
			bounds.day_start, bounds.generated_at
		FROM bounds`).Scan(
		&out.PartnersPendingReview, &out.DocumentsPending, &out.VehiclesPending, &out.PaymentsAwaitingVerification,
		&out.RidesToday, &out.RidesLast7Days, &out.LiveRidesNow, &out.CancellationsToday,
		&out.OpenComplaints, &out.OpenSafetyIncidents,
		&out.RevenueTodayPaise,
		&out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("admin stats: %w", err)
	}
	return out, nil
}
