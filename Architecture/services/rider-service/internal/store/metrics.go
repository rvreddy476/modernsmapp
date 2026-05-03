// Partner metrics + fraud-score recompute store. Sprint 4 introduces a
// nightly job that walks every active partner, computes acceptance /
// cancellation / completion rates over the trailing 30 days from
// rider_ride_offers + rider_rides, then writes the totals back onto
// rider_partners. Fraud-score recompute uses many of the same source
// tables (offers, rides, complaints, safety incidents) so the helpers
// are shared.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §13 (anti-fraud) + §15 (jobs).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PartnerMetrics holds the four trailing-30d numbers we recompute nightly.
// Rates are 0..1 floats; the caller multiplies by 100 before writing the
// rider_partners columns (which store percentages).
type PartnerMetrics struct {
	OffersReceived  int
	OffersAccepted  int
	RidesAssigned   int
	RidesCompleted  int
	RidesCancelled  int
	AcceptanceRate  float64
	CompletionRate  float64
	CancellationRate float64
}

// ComputePartnerMetrics returns the trailing-window aggregates for the
// partner. days defaults to 30 when <= 0.
func (s *Store) ComputePartnerMetrics(ctx context.Context, partnerID uuid.UUID, days int) (PartnerMetrics, error) {
	if days <= 0 {
		days = 30
	}
	out := PartnerMetrics{}
	const offerQ = `
        SELECT
            COUNT(*)::int                                            AS offers_received,
            COUNT(*) FILTER (WHERE status = 'accepted')::int          AS offers_accepted
        FROM rider_ride_offers
        WHERE partner_id = $1
          AND created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, offerQ, partnerID, days).Scan(&out.OffersReceived, &out.OffersAccepted); err != nil {
		return out, fmt.Errorf("partner metrics offers: %w", err)
	}
	const rideQ = `
        SELECT
            COUNT(*)::int                                                       AS assigned,
            COUNT(*) FILTER (WHERE status = 'completed')::int                   AS completed,
            COUNT(*) FILTER (WHERE status = 'cancelled_by_partner')::int        AS cancelled
        FROM rider_rides
        WHERE partner_id = $1
          AND created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, rideQ, partnerID, days).Scan(&out.RidesAssigned, &out.RidesCompleted, &out.RidesCancelled); err != nil {
		return out, fmt.Errorf("partner metrics rides: %w", err)
	}
	if out.OffersReceived > 0 {
		out.AcceptanceRate = float64(out.OffersAccepted) / float64(out.OffersReceived)
	}
	if out.RidesAssigned > 0 {
		out.CompletionRate = float64(out.RidesCompleted) / float64(out.RidesAssigned)
		out.CancellationRate = float64(out.RidesCancelled) / float64(out.RidesAssigned)
	}
	return out, nil
}

// UpdatePartnerMetrics writes the trailing aggregates back onto
// rider_partners. Stamps metrics_recalc_at = NOW().
func (s *Store) UpdatePartnerMetrics(ctx context.Context, partnerID uuid.UUID, m PartnerMetrics) error {
	const q = `
        UPDATE rider_partners
        SET acceptance_rate    = $2,
            completion_rate    = $3,
            cancellation_rate  = $4,
            metrics_recalc_at  = NOW(),
            updated_at         = NOW()
        WHERE id = $1 AND deleted_at IS NULL`
	_, err := s.db.Exec(ctx, q, partnerID,
		m.AcceptanceRate*100, m.CompletionRate*100, m.CancellationRate*100,
	)
	if err != nil {
		return fmt.Errorf("update partner metrics: %w", err)
	}
	return nil
}

// ListActivePartnerIDs returns the ids of every approved/non-deleted
// partner. Used by the metrics + fraud recompute jobs.
func (s *Store) ListActivePartnerIDs(ctx context.Context) ([]uuid.UUID, error) {
	const q = `
        SELECT id FROM rider_partners
        WHERE deleted_at IS NULL
          AND status IN ('approved','suspended','pending_verification')
        ORDER BY id`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list active partner ids: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// FraudInputs holds every counter the fraud-score formula consumes for one
// partner over the trailing 30 days. The job mixes these into a 0..100
// score per spec §13.
type FraudInputs struct {
	PartnerID            uuid.UUID
	OffersReceived       int
	OffersAccepted       int
	RidesAssigned        int
	RidesCompleted       int
	RidesCancelledByP    int
	AcceptThenCancel     int
	ComplaintsCount      int
	SafetyIncidentsCount int
	ExpiredDocsActive    int
}

// FetchFraudInputs gathers the numbers used by the fraud-score formula.
// "Accept-then-cancel" is the count of rides where the partner accepted
// the offer (rider_ride_offers.status='accepted') and then later cancelled
// the ride (rider_rides.status='cancelled_by_partner').
func (s *Store) FetchFraudInputs(ctx context.Context, partnerID uuid.UUID, days int) (FraudInputs, error) {
	if days <= 0 {
		days = 30
	}
	out := FraudInputs{PartnerID: partnerID}
	const offerQ = `
        SELECT COUNT(*)::int,
               COUNT(*) FILTER (WHERE status = 'accepted')::int
        FROM rider_ride_offers
        WHERE partner_id = $1
          AND created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, offerQ, partnerID, days).Scan(&out.OffersReceived, &out.OffersAccepted); err != nil {
		return out, fmt.Errorf("fraud offers: %w", err)
	}
	const rideQ = `
        SELECT COUNT(*)::int,
               COUNT(*) FILTER (WHERE status = 'completed')::int,
               COUNT(*) FILTER (WHERE status = 'cancelled_by_partner')::int
        FROM rider_rides
        WHERE partner_id = $1
          AND created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, rideQ, partnerID, days).Scan(&out.RidesAssigned, &out.RidesCompleted, &out.RidesCancelledByP); err != nil {
		return out, fmt.Errorf("fraud rides: %w", err)
	}
	// accept-then-cancel: rides where partner accepted (offer) AND ride was
	// later cancelled by partner — that's strict fraud-pattern signal.
	const atcQ = `
        SELECT COUNT(*)::int
        FROM rider_rides r
        JOIN rider_ride_offers o ON o.ride_id = r.id AND o.partner_id = r.partner_id
        WHERE r.partner_id = $1
          AND r.status = 'cancelled_by_partner'
          AND o.status = 'accepted'
          AND r.created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, atcQ, partnerID, days).Scan(&out.AcceptThenCancel); err != nil {
		return out, fmt.Errorf("fraud accept-then-cancel: %w", err)
	}
	const compQ = `
        SELECT COUNT(*)::int
        FROM rider_complaints
        WHERE partner_id = $1
          AND created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, compQ, partnerID, days).Scan(&out.ComplaintsCount); err != nil {
		return out, fmt.Errorf("fraud complaints: %w", err)
	}
	const safQ = `
        SELECT COUNT(*)::int
        FROM rider_safety_incidents
        WHERE partner_id = $1
          AND created_at >= NOW() - ($2::int * INTERVAL '1 day')`
	if err := s.db.QueryRow(ctx, safQ, partnerID, days).Scan(&out.SafetyIncidentsCount); err != nil {
		return out, fmt.Errorf("fraud safety: %w", err)
	}
	// Expired docs while the partner was online — caught at this granularity
	// via "documents whose expiry has passed but the partner is currently
	// online". This is a coarse proxy; the production version would scan a
	// "docs expired AND online_at > expires_at" view.
	const docQ = `
        SELECT COUNT(*)::int
        FROM rider_partner_documents d
        JOIN rider_partners p ON p.id = d.partner_id
        WHERE d.partner_id = $1
          AND d.expires_at IS NOT NULL
          AND d.expires_at < NOW()
          AND p.is_online = TRUE`
	if err := s.db.QueryRow(ctx, docQ, partnerID).Scan(&out.ExpiredDocsActive); err != nil {
		return out, fmt.Errorf("fraud docs: %w", err)
	}
	return out, nil
}

// SetFraudScore writes fraud_score on rider_partners. Caller is responsible
// for capping at 100 before calling.
func (s *Store) SetFraudScore(ctx context.Context, partnerID uuid.UUID, score float64) error {
	const q = `
        UPDATE rider_partners
        SET fraud_score = $2,
            updated_at  = NOW()
        WHERE id = $1 AND deleted_at IS NULL`
	_, err := s.db.Exec(ctx, q, partnerID, score)
	if err != nil {
		return fmt.Errorf("set fraud score: %w", err)
	}
	return nil
}

// SetPartnerSuspended sets status='suspended' + suspended_reason. Used by
// the auto-suspend branch of the nightly fraud-score job (score >= 90).
func (s *Store) SetPartnerSuspended(ctx context.Context, partnerID uuid.UUID, reason string) error {
	const q = `
        UPDATE rider_partners
        SET status           = 'suspended',
            suspended_reason = $2,
            updated_at       = NOW()
        WHERE id = $1 AND deleted_at IS NULL AND status NOT IN ('blocked','suspended')`
	_, err := s.db.Exec(ctx, q, partnerID, reason)
	if err != nil {
		return fmt.Errorf("set partner suspended: %w", err)
	}
	return nil
}

// AdminQueueCounts holds the counters consumed by the daily admin-queue
// summary email/push (notification-service routes the event).
type AdminQueueCounts struct {
	PendingKYCCount         int
	PendingVehicleCount     int
	PendingPaymentCount     int
	OpenComplaintsCount     int
	OpenSafetyIncidentsCount int
}

// FetchAdminQueueCounts is a single-shot aggregate used by the daily
// admin-queue summary job.
func (s *Store) FetchAdminQueueCounts(ctx context.Context) (AdminQueueCounts, error) {
	out := AdminQueueCounts{}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_partner_documents WHERE status = 'pending'`).Scan(&out.PendingKYCCount); err != nil {
		return out, fmt.Errorf("queue pending kyc: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_vehicles WHERE status = 'pending'`).Scan(&out.PendingVehicleCount); err != nil {
		return out, fmt.Errorf("queue pending vehicle: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_subscription_payments WHERE status IN ('pending','submitted')`).Scan(&out.PendingPaymentCount); err != nil {
		return out, fmt.Errorf("queue pending payment: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_complaints WHERE status IN ('open','under_review')`).Scan(&out.OpenComplaintsCount); err != nil {
		return out, fmt.Errorf("queue open complaints: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_safety_incidents WHERE status IN ('open','acknowledged')`).Scan(&out.OpenSafetyIncidentsCount); err != nil {
		return out, fmt.Errorf("queue open safety: %w", err)
	}
	return out, nil
}

// CohortRetentionRow is one row from PartnerCohortRetention.
type CohortRetentionRow struct {
	CohortMonth      time.Time `json:"cohort_month"`
	CohortSize       int       `json:"cohort_size"`
	ActiveAt1Month   int       `json:"active_at_1_month"`
	ActiveAt2Months  int       `json:"active_at_2_months"`
	ActiveAt3Months  int       `json:"active_at_3_months"`
}

// PartnerCohortRetention computes the cohort-month retention curve for
// the partner cohort that signed up in the given UTC month boundary.
// "Active at N months" is defined as: the partner has at least one ride
// (assigned/completed) inside that month.
func (s *Store) PartnerCohortRetention(ctx context.Context, cohortStart time.Time) (*CohortRetentionRow, error) {
	if cohortStart.IsZero() {
		return nil, fmt.Errorf("cohort start required")
	}
	cohortEnd := cohortStart.AddDate(0, 1, 0)
	out := &CohortRetentionRow{CohortMonth: cohortStart}
	const cohortQ = `
        SELECT COUNT(*)::int FROM rider_partners
        WHERE created_at >= $1 AND created_at < $2 AND deleted_at IS NULL`
	if err := s.db.QueryRow(ctx, cohortQ, cohortStart, cohortEnd).Scan(&out.CohortSize); err != nil {
		return nil, fmt.Errorf("cohort size: %w", err)
	}
	if out.CohortSize == 0 {
		return out, nil
	}
	const activeQ = `
        SELECT COUNT(DISTINCT r.partner_id)::int
        FROM rider_rides r
        JOIN rider_partners p ON p.id = r.partner_id
        WHERE p.created_at >= $1 AND p.created_at < $2 AND p.deleted_at IS NULL
          AND r.created_at >= $3 AND r.created_at < $4
          AND r.partner_id IS NOT NULL`
	for i, offset := range []int{1, 2, 3} {
		ws := cohortStart.AddDate(0, offset, 0)
		we := cohortStart.AddDate(0, offset+1, 0)
		var n int
		if err := s.db.QueryRow(ctx, activeQ, cohortStart, cohortEnd, ws, we).Scan(&n); err != nil {
			return nil, fmt.Errorf("cohort active month %d: %w", offset, err)
		}
		switch i {
		case 0:
			out.ActiveAt1Month = n
		case 1:
			out.ActiveAt2Months = n
		case 2:
			out.ActiveAt3Months = n
		}
	}
	return out, nil
}

// CustomerCohortBookingRateRow is the response shape for
// CustomerCohortBookingRate.
type CustomerCohortBookingRateRow struct {
	CohortMonth        time.Time `json:"cohort_month"`
	CohortSize         int       `json:"cohort_size"`
	AvgRidesNext1Month float64   `json:"avg_rides_next_1_month"`
	AvgRidesNext2Month float64   `json:"avg_rides_next_2_month"`
	AvgRidesNext3Month float64   `json:"avg_rides_next_3_month"`
}

// CustomerCohortBookingRate computes the per-customer average rides over
// the next 1/2/3 months for customers whose first ride was in the cohort
// month.
func (s *Store) CustomerCohortBookingRate(ctx context.Context, cohortStart time.Time) (*CustomerCohortBookingRateRow, error) {
	if cohortStart.IsZero() {
		return nil, fmt.Errorf("cohort start required")
	}
	cohortEnd := cohortStart.AddDate(0, 1, 0)
	out := &CustomerCohortBookingRateRow{CohortMonth: cohortStart}
	// Identify the cohort: customers whose first-ever ride lands in the window.
	const cohortQ = `
        SELECT customer_user_id
        FROM rider_rides
        GROUP BY customer_user_id
        HAVING MIN(created_at) >= $1 AND MIN(created_at) < $2`
	rows, err := s.db.Query(ctx, cohortQ, cohortStart, cohortEnd)
	if err != nil {
		return nil, fmt.Errorf("customer cohort: %w", err)
	}
	defer rows.Close()
	var customerIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		customerIDs = append(customerIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.CohortSize = len(customerIDs)
	if out.CohortSize == 0 {
		return out, nil
	}
	// Total rides in months 1/2/3 (counting from cohort end).
	const ridesQ = `
        SELECT COUNT(*)::int
        FROM rider_rides
        WHERE customer_user_id = ANY($1::uuid[])
          AND created_at >= $2 AND created_at < $3`
	for i, offset := range []int{1, 2, 3} {
		ws := cohortStart.AddDate(0, offset, 0)
		we := cohortStart.AddDate(0, offset+1, 0)
		var total int
		if err := s.db.QueryRow(ctx, ridesQ, customerIDs, ws, we).Scan(&total); err != nil {
			return nil, fmt.Errorf("customer rides month %d: %w", offset, err)
		}
		avg := float64(total) / float64(out.CohortSize)
		switch i {
		case 0:
			out.AvgRidesNext1Month = avg
		case 1:
			out.AvgRidesNext2Month = avg
		case 2:
			out.AvgRidesNext3Month = avg
		}
	}
	return out, nil
}
