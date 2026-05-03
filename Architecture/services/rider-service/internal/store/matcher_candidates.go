package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/atpost/rider-service/internal/matcher"
	"github.com/google/uuid"
)

// LoadMatcherCandidates joins partners + their primary approved vehicle +
// active subscription + plan + last-known location into matcher candidates.
//
// One row per partner. Caller pre-filters by Redis / Postgres geohash so
// `partnerIDs` is small (≤ 50). The fan-out join is one query rather than N
// to keep the matcher fast even on cold-cache.
//
// DistanceKM is left at zero — the matcher caller fills it from whichever
// source produced the partner ids (Redis returns it pre-computed; the
// Postgres fallback uses geo.HaversineKM).
func (s *Store) LoadMatcherCandidates(ctx context.Context, partnerIDs []uuid.UUID) ([]matcher.PartnerCandidate, error) {
	if len(partnerIDs) == 0 {
		return nil, nil
	}
	// Build a parameterized IN ($1, $2, …, $N) — pgx maps []uuid.UUID well via
	// ANY but we keep the explicit form for portability + readability.
	placeholders := make([]string, len(partnerIDs))
	args := make([]any, len(partnerIDs))
	for i, id := range partnerIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	q := `
        SELECT
            p.id::text AS partner_id,
            p.user_id::text AS user_id,
            COALESCE(v.id::text, '') AS vehicle_id,
            COALESCE(v.vehicle_type::text, '') AS vehicle_type,
            COALESCE(p.city_id::text, '') AS city_id,
            p.status::text AS partner_status,
            p.kyc_status::text AS kyc_status,
            COALESCE(v.status::text, 'pending') AS vehicle_status,
            COALESCE(s.status::text, 'expired') AS subscription_status,
            COALESCE(loc.is_online, FALSE) AS is_online,
            COALESCE(plan.is_unlimited, FALSE) AS is_unlimited_plan,
            COALESCE(s.leads_used, 0) AS leads_used_this_month,
            COALESCE(plan.lead_limit, 0) AS lead_allotment,
            COALESCE(plan.priority_weight, 0) AS plan_priority_weight,
            COALESCE(p.rating, 0) AS rating,
            COALESCE(p.acceptance_rate, 0) AS acceptance_rate,
            COALESCE(p.cancellation_rate, 0) AS cancellation_rate,
            COALESCE(p.fraud_score, 0) AS fraud_score,
            COALESCE(EXTRACT(EPOCH FROM (NOW() - p.last_online_at))::int, 0) AS idle_secs
        FROM rider_partners p
        LEFT JOIN LATERAL (
            SELECT id, vehicle_type, status
            FROM rider_vehicles vv
            WHERE vv.partner_id = p.id AND vv.deleted_at IS NULL AND vv.is_active = TRUE
            ORDER BY (CASE WHEN vv.status = 'approved' THEN 0 ELSE 1 END), vv.created_at DESC
            LIMIT 1
        ) v ON TRUE
        LEFT JOIN LATERAL (
            SELECT id, status, plan_id, leads_used
            FROM rider_partner_subscriptions ss
            WHERE ss.partner_id = p.id AND ss.status IN ('trial','active','grace_period')
            ORDER BY ss.expires_at DESC
            LIMIT 1
        ) s ON TRUE
        LEFT JOIN rider_subscription_plans plan ON plan.id = s.plan_id
        LEFT JOIN rider_partner_locations loc ON loc.partner_id = p.id
        WHERE p.id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load matcher candidates: %w", err)
	}
	defer rows.Close()
	var out []matcher.PartnerCandidate
	for rows.Next() {
		var c matcher.PartnerCandidate
		if err := rows.Scan(
			&c.PartnerID, &c.UserID, &c.VehicleID, &c.VehicleType, &c.CityID,
			&c.PartnerStatus, &c.KYCStatus, &c.VehicleStatus, &c.SubscriptionStatus,
			&c.IsOnline, &c.IsUnlimitedPlan, &c.LeadsUsedThisMonth, &c.LeadAllotment,
			&c.PlanPriorityWeight, &c.Rating, &c.AcceptanceRate, &c.CancellationRate,
			&c.FraudScore, &c.IdleSecs,
		); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
