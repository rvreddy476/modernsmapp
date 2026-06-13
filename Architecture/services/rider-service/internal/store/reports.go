package store

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

// MatchingHealthRow is per-city / per-vehicle matching latency.
type MatchingHealthRow struct {
	CityID                 string   `json:"city_id"`
	VehicleType            string   `json:"vehicle_type"`
	RidesTotal             int      `json:"rides_total"`
	NoCandidateCount       int      `json:"no_candidate_count"`
	AvgTimeToFirstOfferSec *float64 `json:"avg_time_to_first_offer_seconds,omitempty"`
}

func (s *Store) ReportMatchingHealth(ctx context.Context, w ReportWindow) ([]MatchingHealthRow, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		WITH base AS (
			SELECT
				COALESCE(r.city_id::text, '') AS city_id,
				r.vehicle_type,
				r.id AS ride_id,
				(SELECT MIN(created_at) FROM rider_ride_offers WHERE ride_id = r.id) AS first_offer
			FROM rider_rides r
			WHERE r.created_at BETWEEN $1 AND $2
		)
		SELECT
			city_id,
			vehicle_type,
			COUNT(*)::int AS rides_total,
			COUNT(*) FILTER (WHERE first_offer IS NULL)::int AS no_candidate_count,
			AVG(EXTRACT(EPOCH FROM (first_offer - (SELECT created_at FROM rider_rides WHERE id = ride_id))))::float8
		FROM base
		GROUP BY city_id, vehicle_type
		ORDER BY rides_total DESC
		LIMIT 200
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("matching health: %w", err)
	}
	defer rows.Close()
	var out []MatchingHealthRow
	for rows.Next() {
		var r MatchingHealthRow
		if err := rows.Scan(&r.CityID, &r.VehicleType, &r.RidesTotal,
			&r.NoCandidateCount, &r.AvgTimeToFirstOfferSec); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PartnerQualityRow is per-partner reject + no-show + acceptance.
type PartnerQualityRow struct {
	PartnerID       string  `json:"partner_id"`
	FullName        string  `json:"full_name"`
	OffersReceived  int     `json:"offers_received"`
	OffersAccepted  int     `json:"offers_accepted"`
	OffersRejected  int     `json:"offers_rejected"`
	OffersExpired   int     `json:"offers_expired"`
	AcceptancePct   float64 `json:"acceptance_pct"`
	NoShowCount     int     `json:"no_show_count_30d"`
	Rating30d       float64 `json:"avg_rating_30d"`
}

func (s *Store) ReportPartnerQuality(ctx context.Context, w ReportWindow) ([]PartnerQualityRow, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		SELECT
			p.id::text, COALESCE(p.full_name, ''),
			COALESCE(off.received, 0)::int,
			COALESCE(off.accepted, 0)::int,
			COALESCE(off.rejected, 0)::int,
			COALESCE(off.expired,  0)::int,
			ROUND(100.0 * COALESCE(off.accepted, 0) / NULLIF(COALESCE(off.received, 0), 0), 2)::float8 AS accept_pct,
			COALESCE(p.no_show_count_30d, 0)::int,
			COALESCE(rt.avg_rating, 0)::float8
		FROM rider_partners p
		LEFT JOIN (
			SELECT
				partner_id,
				COUNT(*) AS received,
				COUNT(*) FILTER (WHERE status = 'accepted') AS accepted,
				COUNT(*) FILTER (WHERE status = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE status = 'expired')  AS expired
			FROM rider_ride_offers
			WHERE created_at BETWEEN $1 AND $2
			GROUP BY partner_id
		) off ON off.partner_id = p.id
		LEFT JOIN (
			SELECT partner_id, AVG(rating)::numeric(3,2) AS avg_rating
			FROM rider_rides
			WHERE rating IS NOT NULL
			  AND completed_at BETWEEN $1 AND $2
			  AND rating_visibility = 'public'
			GROUP BY partner_id
		) rt ON rt.partner_id = p.id
		WHERE COALESCE(off.received, 0) > 0
		ORDER BY accept_pct ASC NULLS LAST
		LIMIT 200
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("partner quality: %w", err)
	}
	defer rows.Close()
	var out []PartnerQualityRow
	for rows.Next() {
		var r PartnerQualityRow
		if err := rows.Scan(&r.PartnerID, &r.FullName, &r.OffersReceived,
			&r.OffersAccepted, &r.OffersRejected, &r.OffersExpired,
			&r.AcceptancePct, &r.NoShowCount, &r.Rating30d); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SupplyDemandRow is per-city, per-hour supply (online partners) vs
// demand (ride requests). Used by the ops dashboard's heat-map.
type SupplyDemandRow struct {
	CityID         string  `json:"city_id"`
	HourBucket     string  `json:"hour_bucket"`
	RideRequests   int     `json:"ride_requests"`
	OnlinePartners int     `json:"online_partners_avg"`
}

func (s *Store) ReportSupplyDemand(ctx context.Context, w ReportWindow) ([]SupplyDemandRow, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		SELECT
			COALESCE(city_id::text, '') AS city_id,
			date_trunc('hour', created_at)::text AS hour_bucket,
			COUNT(*)::int AS ride_requests,
			0::int AS online_partners_avg
		FROM rider_rides
		WHERE created_at BETWEEN $1 AND $2
		GROUP BY city_id, date_trunc('hour', created_at)
		ORDER BY hour_bucket
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("supply demand: %w", err)
	}
	defer rows.Close()
	var out []SupplyDemandRow
	for rows.Next() {
		var r SupplyDemandRow
		if err := rows.Scan(&r.CityID, &r.HourBucket, &r.RideRequests, &r.OnlinePartners); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SafetyIncidentReportRow groups safety incidents by kind + severity.
type SafetyIncidentReportRow struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

func (s *Store) ReportSafetyIncidents(ctx context.Context, w ReportWindow) ([]SafetyIncidentReportRow, error) {
	w.defaults()
	rows, err := s.db.Query(ctx, `
		SELECT kind, severity, COUNT(*)::int
		FROM rider_safety_incidents
		WHERE created_at BETWEEN $1 AND $2
		GROUP BY kind, severity
		ORDER BY count DESC
	`, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("safety report: %w", err)
	}
	defer rows.Close()
	var out []SafetyIncidentReportRow
	for rows.Next() {
		var r SafetyIncidentReportRow
		if err := rows.Scan(&r.Kind, &r.Severity, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PartnerComplianceRow flags partners with expired docs or vehicles.
type PartnerComplianceRow struct {
	PartnerID       string `json:"partner_id"`
	FullName        string `json:"full_name"`
	City            string `json:"city"`
	ExpiredDocs     int    `json:"expired_docs"`
	ExpiredVehicleDocs int `json:"expired_vehicle_docs"`
	OldestExpiry    string `json:"oldest_expiry,omitempty"`
}

func (s *Store) ReportPartnerCompliance(ctx context.Context, city string) ([]PartnerComplianceRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			p.id::text,
			COALESCE(p.full_name, ''),
			COALESCE(p.city, ''),
			COALESCE((SELECT COUNT(*) FROM rider_partner_documents
				WHERE partner_id = p.id AND status = 'approved'
				  AND expires_at IS NOT NULL AND expires_at <= NOW()), 0)::int,
			COALESCE((SELECT COUNT(*) FROM rider_vehicle_documents vd
				JOIN rider_vehicles v ON v.id = vd.vehicle_id
				WHERE v.partner_id = p.id AND vd.status = 'approved'
				  AND vd.expires_at IS NOT NULL AND vd.expires_at <= NOW()), 0)::int,
			COALESCE((SELECT MIN(expires_at)::text FROM rider_partner_documents
				WHERE partner_id = p.id AND status = 'approved' AND expires_at IS NOT NULL), '')
		FROM rider_partners p
		WHERE p.status IN ('approved')
		  AND ($1 = '' OR p.city = $1)
		ORDER BY (
			COALESCE((SELECT COUNT(*) FROM rider_partner_documents WHERE partner_id = p.id AND expires_at <= NOW()), 0)
			+ COALESCE((SELECT COUNT(*) FROM rider_vehicle_documents vd JOIN rider_vehicles v ON v.id = vd.vehicle_id WHERE v.partner_id = p.id AND vd.expires_at <= NOW()), 0)
		) DESC
		LIMIT 200
	`, city)
	if err != nil {
		return nil, fmt.Errorf("partner compliance: %w", err)
	}
	defer rows.Close()
	var out []PartnerComplianceRow
	for rows.Next() {
		var r PartnerComplianceRow
		if err := rows.Scan(&r.PartnerID, &r.FullName, &r.City,
			&r.ExpiredDocs, &r.ExpiredVehicleDocs, &r.OldestExpiry); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
