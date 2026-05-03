package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrVehicleNotFound is returned when GetVehicle finds no row.
var ErrVehicleNotFoundAdmin = errors.New("vehicle: not found")

// AdminDashboardCounts is the response shape for the admin dashboard.
type AdminDashboardCounts struct {
	TotalCustomers              int   `json:"total_customers"`
	TotalPartners               int   `json:"total_partners"`
	ActivePartners              int   `json:"active_partners"`
	PendingKYC                  int   `json:"pending_kyc"`
	PendingVehicle              int   `json:"pending_vehicle"`
	PendingPayment              int   `json:"pending_payment"`
	RidesToday                  int   `json:"rides_today"`
	CompletedToday              int   `json:"completed_today"`
	CancelledToday              int   `json:"cancelled_today"`
	PartnerSubRevenueTodayPaise int64 `json:"partner_subscription_revenue_today_paise"`
	ExpiringSubscriptions7Days  int   `json:"expiring_subscriptions_7d"`
	SuspendedPartners           int   `json:"suspended_partners"`
	OpenComplaints              int   `json:"open_complaints"`
	OpenSafetyIncidents         int   `json:"sos_incidents_open"`
}

// AdminDashboardCounts gathers all dashboard counters in one round-trip.
//
// Each counter is a separate query for clarity; the queries are tiny index
// lookups and the dashboard is admin-only so we accept the n round-trips.
// In production this could be a single CTE if the admin team complains.
func (s *Store) AdminDashboardCounts(ctx context.Context) (*AdminDashboardCounts, error) {
	out := &AdminDashboardCounts{}

	if err := s.db.QueryRow(ctx, `SELECT COUNT(DISTINCT customer_user_id)::int FROM rider_rides`).Scan(&out.TotalCustomers); err != nil {
		return nil, fmt.Errorf("count customers: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_partners WHERE deleted_at IS NULL`).Scan(&out.TotalPartners); err != nil {
		return nil, fmt.Errorf("count partners: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_partners WHERE status = 'approved' AND deleted_at IS NULL`).Scan(&out.ActivePartners); err != nil {
		return nil, fmt.Errorf("count active partners: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_partner_documents WHERE status = 'pending'`).Scan(&out.PendingKYC); err != nil {
		return nil, fmt.Errorf("count pending kyc: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_vehicles WHERE status = 'pending'`).Scan(&out.PendingVehicle); err != nil {
		return nil, fmt.Errorf("count pending vehicles: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_subscription_payments WHERE status IN ('pending','submitted')`).Scan(&out.PendingPayment); err != nil {
		return nil, fmt.Errorf("count pending payments: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_rides WHERE created_at >= date_trunc('day', NOW())`).Scan(&out.RidesToday); err != nil {
		return nil, fmt.Errorf("count rides today: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_rides WHERE status = 'completed' AND completed_at >= date_trunc('day', NOW())`).Scan(&out.CompletedToday); err != nil {
		return nil, fmt.Errorf("count completed today: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_rides WHERE status LIKE 'cancelled_%' AND cancelled_at >= date_trunc('day', NOW())`).Scan(&out.CancelledToday); err != nil {
		return nil, fmt.Errorf("count cancelled today: %w", err)
	}
	if err := s.db.QueryRow(ctx, `
        SELECT COALESCE(SUM(amount * 100)::bigint, 0)
        FROM rider_subscription_payments
        WHERE status = 'verified' AND verified_at >= date_trunc('day', NOW())`).Scan(&out.PartnerSubRevenueTodayPaise); err != nil {
		return nil, fmt.Errorf("sum subscription revenue today: %w", err)
	}
	if err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM rider_partner_subscriptions
        WHERE status IN ('active','grace_period') AND expires_at BETWEEN NOW() AND NOW() + INTERVAL '7 days'`).Scan(&out.ExpiringSubscriptions7Days); err != nil {
		return nil, fmt.Errorf("count expiring subs: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM rider_partners WHERE status IN ('suspended','blocked') AND deleted_at IS NULL`).Scan(&out.SuspendedPartners); err != nil {
		return nil, fmt.Errorf("count suspended partners: %w", err)
	}
	open, err := s.CountOpenComplaints(ctx)
	if err != nil {
		return nil, err
	}
	out.OpenComplaints = open
	sos, err := s.CountOpenSafetyIncidents(ctx)
	if err != nil {
		return nil, err
	}
	out.OpenSafetyIncidents = sos
	return out, nil
}

// PartnerListFilter is the filter used by the admin partner listing.
type PartnerListFilter struct {
	Status string
	Query  string
	Limit  int
	Offset int
}

// ListPartners returns partners filtered by status (empty = all) + free-text
// search (matches full_name / phone / email LIKE).
func (s *Store) ListPartners(ctx context.Context, f PartnerListFilter) ([]Partner, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	q := `
        SELECT id, user_id, partner_type, fleet_owner_id, full_name, phone, email, profile_photo_url,
               city_id, status, kyc_status, bank_status, rating, total_rides_completed, total_rides_cancelled,
               acceptance_rate, cancellation_rate, fraud_score, is_online, approved_at, created_at, updated_at
        FROM rider_partners
        WHERE deleted_at IS NULL
          AND ($1::text IS NULL OR status = $1::rider_partner_status)
          AND ($2::text IS NULL OR
               full_name ILIKE '%' || $2 || '%' OR
               phone ILIKE '%' || $2 || '%' OR
               COALESCE(email, '') ILIKE '%' || $2 || '%')
        ORDER BY created_at DESC
        LIMIT $3 OFFSET $4`
	var statusPtr, queryPtr *string
	if f.Status != "" {
		statusPtr = &f.Status
	}
	if f.Query != "" {
		queryPtr = &f.Query
	}
	rows, err := s.db.Query(ctx, q, statusPtr, queryPtr, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list partners: %w", err)
	}
	defer rows.Close()
	var out []Partner
	for rows.Next() {
		p, err := scanPartner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// SetPartnerApprovedAt stamps approved_at = now() and status = approved.
// Used by the admin approval flow.
func (s *Store) SetPartnerApprovedAt(ctx context.Context, partnerID uuid.UUID) error {
	const q = `
        UPDATE rider_partners
        SET status      = 'approved',
            kyc_status  = 'approved',
            approved_at = NOW(),
            updated_at  = NOW()
        WHERE id = $1 AND deleted_at IS NULL`
	tag, err := s.db.Exec(ctx, q, partnerID)
	if err != nil {
		return fmt.Errorf("approve partner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPartnerNotFound
	}
	return nil
}

// --- Document admin queries ------------------------------------------------

// ListPartnerDocumentsByStatus returns docs in the given status across all
// partners. Used by the admin verification queue.
func (s *Store) ListPartnerDocumentsByStatus(ctx context.Context, status string, limit, offset int) ([]PartnerDocument, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
        SELECT id, partner_id, document_type, document_number, file_url, status,
               rejection_reason, expires_at, created_at, updated_at
        FROM rider_partner_documents
        WHERE ($1::text IS NULL OR status = $1::rider_verification_status)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	rows, err := s.db.Query(ctx, q, statusPtr, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()
	var out []PartnerDocument
	for rows.Next() {
		var d PartnerDocument
		if err := rows.Scan(&d.ID, &d.PartnerID, &d.DocumentType, &d.DocumentNumber, &d.FileURL, &d.Status, &d.RejectionReason, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetPartnerDocument returns one partner document by id.
func (s *Store) GetPartnerDocument(ctx context.Context, id uuid.UUID) (*PartnerDocument, error) {
	const q = `
        SELECT id, partner_id, document_type, document_number, file_url, status,
               rejection_reason, expires_at, created_at, updated_at
        FROM rider_partner_documents
        WHERE id = $1`
	var d PartnerDocument
	row := s.db.QueryRow(ctx, q, id)
	if err := row.Scan(&d.ID, &d.PartnerID, &d.DocumentType, &d.DocumentNumber, &d.FileURL, &d.Status, &d.RejectionReason, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}
	return &d, nil
}

// SetPartnerDocumentStatus updates the document status (verified / rejected)
// and stores the rejection_reason on rejection.
func (s *Store) SetPartnerDocumentStatus(ctx context.Context, id uuid.UUID, status string, reason *string) (*PartnerDocument, error) {
	const q = `
        UPDATE rider_partner_documents
        SET status           = $2::rider_verification_status,
            rejection_reason = $3,
            updated_at       = NOW()
        WHERE id = $1
        RETURNING id, partner_id, document_type, document_number, file_url, status,
                  rejection_reason, expires_at, created_at, updated_at`
	var d PartnerDocument
	row := s.db.QueryRow(ctx, q, id, status, reason)
	if err := row.Scan(&d.ID, &d.PartnerID, &d.DocumentType, &d.DocumentNumber, &d.FileURL, &d.Status, &d.RejectionReason, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}
	return &d, nil
}

// --- Vehicle admin queries -------------------------------------------------

// ListVehiclesByStatus returns vehicles in the given status. status=""=all.
func (s *Store) ListVehiclesByStatus(ctx context.Context, status string, limit, offset int) ([]Vehicle, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
        SELECT id, partner_id, vehicle_type, registration_number, brand, model, color,
               manufacture_year, seat_count, fuel_type, is_ev, status, is_active, created_at, updated_at
        FROM rider_vehicles
        WHERE ($1::text IS NULL OR status = $1::rider_verification_status)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	rows, err := s.db.Query(ctx, q, statusPtr, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	defer rows.Close()
	var out []Vehicle
	for rows.Next() {
		var v Vehicle
		if err := rows.Scan(&v.ID, &v.PartnerID, &v.VehicleType, &v.RegistrationNumber, &v.Brand, &v.Model, &v.Color, &v.ManufactureYear, &v.SeatCount, &v.FuelType, &v.IsEV, &v.Status, &v.IsActive, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetVehicleStatus updates the vehicle row's status.
func (s *Store) SetVehicleStatus(ctx context.Context, id uuid.UUID, status string) error {
	const q = `
        UPDATE rider_vehicles
        SET status     = $2::rider_verification_status,
            updated_at = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, status)
	if err != nil {
		return fmt.Errorf("set vehicle status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVehicleNotFoundAdmin
	}
	return nil
}

// --- Subscription payment admin queries ------------------------------------

// ListSubscriptionPaymentsByStatus returns payments filtered by status.
func (s *Store) ListSubscriptionPaymentsByStatus(ctx context.Context, status string, limit, offset int) ([]SubscriptionPayment, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
        SELECT id, partner_id, subscription_id, plan_id, amount, currency_code, payment_method,
               payment_reference, payment_proof_url, wallet_txn_id, status, rejection_reason,
               verified_at, created_at, updated_at
        FROM rider_subscription_payments
        WHERE ($1::text IS NULL OR status = $1::rider_payment_status)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	rows, err := s.db.Query(ctx, q, statusPtr, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list subscription payments: %w", err)
	}
	defer rows.Close()
	var out []SubscriptionPayment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// --- Rides admin queries ---------------------------------------------------

// RideListFilter is the filter for the admin ride listing.
type RideListFilter struct {
	Status string
	Query  string
	Limit  int
	Offset int
	Since  *time.Time
}

// ListRidesAdmin returns rides with optional status / query / since filter.
func (s *Store) ListRidesAdmin(ctx context.Context, f RideListFilter) ([]Ride, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	q := `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
        FROM rider_rides
        WHERE ($1::text IS NULL OR status = $1::rider_ride_status)
          AND ($2::text IS NULL OR pickup_address ILIKE '%' || $2 || '%' OR drop_address ILIKE '%' || $2 || '%')
          AND ($3::timestamptz IS NULL OR created_at >= $3)
        ORDER BY created_at DESC
        LIMIT $4 OFFSET $5`
	var statusPtr, queryPtr *string
	if f.Status != "" {
		statusPtr = &f.Status
	}
	if f.Query != "" {
		queryPtr = &f.Query
	}
	rows, err := s.db.Query(ctx, q, statusPtr, queryPtr, f.Since, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list rides admin: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListLiveRides returns rides currently in non-terminal states. Used by the
// admin live map.
func (s *Store) ListLiveRides(ctx context.Context, limit int) ([]Ride, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
        FROM rider_rides
        WHERE status IN ('requested','searching_partner','partner_assigned','partner_arriving','arrived','otp_verified','in_progress')
        ORDER BY requested_at DESC
        LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list live rides: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// --- City / zone / fare-rule admin CRUD ------------------------------------

// UpdateCityInput captures the patchable fields on rider_cities.
type UpdateCityInput struct {
	Name         *string
	State        *string
	Country      *string
	CurrencyCode *string
	IsActive     *bool
}

// UpdateCity applies a partial update to a city row.
func (s *Store) UpdateCity(ctx context.Context, id uuid.UUID, in UpdateCityInput) (*City, error) {
	const q = `
        UPDATE rider_cities SET
            name          = COALESCE($2, name),
            state         = COALESCE($3, state),
            country       = COALESCE($4, country),
            currency_code = COALESCE($5, currency_code),
            is_active     = COALESCE($6, is_active),
            updated_at    = NOW()
        WHERE id = $1
        RETURNING id, name, state, country, currency_code, is_active, created_at, updated_at`
	var c City
	row := s.db.QueryRow(ctx, q, id, in.Name, in.State, in.Country, in.CurrencyCode, in.IsActive)
	if err := row.Scan(&c.ID, &c.Name, &c.State, &c.Country, &c.CurrencyCode, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCityNotFound
		}
		return nil, err
	}
	return &c, nil
}

// CreateZoneInput captures the input for CreateZone.
type CreateZoneInput struct {
	CityID      uuid.UUID
	Name        string
	BoundaryWKT string // POLYGON((lng lat,...)) — caller-validated.
}

// CreateZone inserts a zone with the given boundary polygon.
func (s *Store) CreateZone(ctx context.Context, in CreateZoneInput) (*Zone, error) {
	const q = `
        INSERT INTO rider_zones (city_id, name, boundary, is_active)
        VALUES ($1, $2, ST_GeogFromText('SRID=4326;' || $3), TRUE)
        RETURNING id, city_id, name, is_active, created_at, updated_at`
	var z Zone
	row := s.db.QueryRow(ctx, q, in.CityID, in.Name, in.BoundaryWKT)
	if err := row.Scan(&z.ID, &z.CityID, &z.Name, &z.IsActive, &z.CreatedAt, &z.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create zone: %w", err)
	}
	return &z, nil
}

// UpdateZoneInput captures the patchable fields on a zone row.
type UpdateZoneInput struct {
	Name        *string
	BoundaryWKT *string
	IsActive    *bool
}

// UpdateZone applies a partial update.
func (s *Store) UpdateZone(ctx context.Context, id uuid.UUID, in UpdateZoneInput) (*Zone, error) {
	const q = `
        UPDATE rider_zones SET
            name       = COALESCE($2, name),
            boundary   = CASE WHEN $3::text IS NULL THEN boundary
                              ELSE ST_GeogFromText('SRID=4326;' || $3) END,
            is_active  = COALESCE($4, is_active),
            updated_at = NOW()
        WHERE id = $1
        RETURNING id, city_id, name, is_active, created_at, updated_at`
	var z Zone
	row := s.db.QueryRow(ctx, q, id, in.Name, in.BoundaryWKT, in.IsActive)
	if err := row.Scan(&z.ID, &z.CityID, &z.Name, &z.IsActive, &z.CreatedAt, &z.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("zone: not found")
		}
		return nil, err
	}
	return &z, nil
}

// CreateFareRuleInput captures the input for CreateFareRule.
type CreateFareRuleInput struct {
	CityID          uuid.UUID
	VehicleType     string
	BaseFare        float64
	PerKMFare       float64
	PerMinuteFare   float64
	MinimumFare     float64
	PlatformFee     float64
	NightMultiplier float64
	PeakMultiplier  float64
	CancellationFee float64
}

// CreateFareRule inserts a new fare rule. starts_at = now(); the active rule
// for (city, vehicle_type) is the most recent.
func (s *Store) CreateFareRule(ctx context.Context, in CreateFareRuleInput) (*FareRule, error) {
	const q = `
        INSERT INTO rider_fare_rules (
            city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare,
            platform_fee, night_multiplier, peak_multiplier, cancellation_fee, is_active, starts_at
        ) VALUES (
            $1, $2::rider_vehicle_type, $3, $4, $5, $6,
            $7, $8, $9, $10, TRUE, NOW()
        )
        RETURNING id, city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare,
                  platform_fee, night_multiplier, peak_multiplier, cancellation_fee, is_active, starts_at`
	row := s.db.QueryRow(ctx, q,
		in.CityID, in.VehicleType, in.BaseFare, in.PerKMFare, in.PerMinuteFare, in.MinimumFare,
		in.PlatformFee, in.NightMultiplier, in.PeakMultiplier, in.CancellationFee,
	)
	return scanFareRule(row)
}

// UpdateFareRuleInput captures the patchable fields on a fare rule row.
type UpdateFareRuleInput struct {
	BaseFare        *float64
	PerKMFare       *float64
	PerMinuteFare   *float64
	MinimumFare     *float64
	PlatformFee     *float64
	NightMultiplier *float64
	PeakMultiplier  *float64
	CancellationFee *float64
	IsActive        *bool
}

// UpdateFareRule applies a partial update.
func (s *Store) UpdateFareRule(ctx context.Context, id uuid.UUID, in UpdateFareRuleInput) (*FareRule, error) {
	const q = `
        UPDATE rider_fare_rules SET
            base_fare        = COALESCE($2, base_fare),
            per_km_fare      = COALESCE($3, per_km_fare),
            per_minute_fare  = COALESCE($4, per_minute_fare),
            minimum_fare     = COALESCE($5, minimum_fare),
            platform_fee     = COALESCE($6, platform_fee),
            night_multiplier = COALESCE($7, night_multiplier),
            peak_multiplier  = COALESCE($8, peak_multiplier),
            cancellation_fee = COALESCE($9, cancellation_fee),
            is_active        = COALESCE($10, is_active)
        WHERE id = $1
        RETURNING id, city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare,
                  platform_fee, night_multiplier, peak_multiplier, cancellation_fee, is_active, starts_at`
	row := s.db.QueryRow(ctx, q, id,
		in.BaseFare, in.PerKMFare, in.PerMinuteFare, in.MinimumFare,
		in.PlatformFee, in.NightMultiplier, in.PeakMultiplier, in.CancellationFee, in.IsActive,
	)
	r, err := scanFareRule(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFareRuleNotFound
		}
		return nil, err
	}
	return r, nil
}
