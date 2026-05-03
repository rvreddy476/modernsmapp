// Package store provides PostgreSQL access for rider-service (Mopedu).
//
// The store wraps a pgxpool with per-aggregate methods (cities, zones,
// partners, vehicles, documents, subscriptions, fare-rules, idempotency).
// Service-layer transactions reach for s.DB() directly when an atomic
// multi-table change is needed (e.g. activating a subscription writes both
// `rider_partner_subscriptions` and `rider_subscription_payments` in one tx).
package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgxpool with per-aggregate methods.
type Store struct {
	db *pgxpool.Pool
}

// New returns a Store backed by the given pool.
func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

// DB exposes the underlying pool for service-layer transactions.
func (s *Store) DB() *pgxpool.Pool { return s.db }

// --- Domain types ----------------------------------------------------------

// City is one row in rider_cities.
type City struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	State        *string   `json:"state,omitempty"`
	Country      string    `json:"country"`
	CurrencyCode string    `json:"currency_code"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Zone is one row in rider_zones.
type Zone struct {
	ID        uuid.UUID `json:"id"`
	CityID    uuid.UUID `json:"city_id"`
	Name      string    `json:"name"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Partner is one row in rider_partners.
type Partner struct {
	ID                  uuid.UUID  `json:"id"`
	UserID              uuid.UUID  `json:"user_id"`
	PartnerType         string     `json:"partner_type"`
	FleetOwnerID        *uuid.UUID `json:"fleet_owner_id,omitempty"`
	FullName            string     `json:"full_name"`
	Phone               string     `json:"phone"`
	Email               *string    `json:"email,omitempty"`
	ProfilePhotoURL     *string    `json:"profile_photo_url,omitempty"`
	CityID              *uuid.UUID `json:"city_id,omitempty"`
	Status              string     `json:"status"`
	KYCStatus           string     `json:"kyc_status"`
	BankStatus          string     `json:"bank_status"`
	Rating              float64    `json:"rating"`
	TotalRidesCompleted int        `json:"total_rides_completed"`
	TotalRidesCancelled int        `json:"total_rides_cancelled"`
	AcceptanceRate      float64    `json:"acceptance_rate"`
	CancellationRate    float64    `json:"cancellation_rate"`
	FraudScore          float64    `json:"fraud_score"`
	IsOnline            bool       `json:"is_online"`
	ApprovedAt          *time.Time `json:"approved_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// PartnerDocument is one row in rider_partner_documents.
type PartnerDocument struct {
	ID              uuid.UUID  `json:"id"`
	PartnerID       uuid.UUID  `json:"partner_id"`
	DocumentType    string     `json:"document_type"`
	DocumentNumber  *string    `json:"document_number,omitempty"`
	FileURL         string     `json:"file_url"`
	Status          string     `json:"status"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// AadhaarVerification mirrors a row in rider_partner_aadhaar_verifications.
//
// DPDP Act compliant: NEVER carries the raw Aadhaar number — only the partner
// DigiLocker assertion id and a hashed document-type label.
type AadhaarVerification struct {
	PartnerID     uuid.UUID `json:"partner_id"`
	DigiLockerRef string    `json:"digilocker_ref"`
	DocTypeHash   string    `json:"doc_type_hash"`
	IssuedAt      time.Time `json:"issued_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// Vehicle is one row in rider_vehicles.
type Vehicle struct {
	ID                 uuid.UUID `json:"id"`
	PartnerID          uuid.UUID `json:"partner_id"`
	VehicleType        string    `json:"vehicle_type"`
	RegistrationNumber string    `json:"registration_number"`
	Brand              *string   `json:"brand,omitempty"`
	Model              *string   `json:"model,omitempty"`
	Color              *string   `json:"color,omitempty"`
	ManufactureYear    *int      `json:"manufacture_year,omitempty"`
	SeatCount          *int      `json:"seat_count,omitempty"`
	FuelType           *string   `json:"fuel_type,omitempty"`
	IsEV               bool      `json:"is_ev"`
	Status             string    `json:"status"`
	IsActive           bool      `json:"is_active"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// VehicleDocument is one row in rider_vehicle_documents.
type VehicleDocument struct {
	ID              uuid.UUID  `json:"id"`
	VehicleID       uuid.UUID  `json:"vehicle_id"`
	DocumentType    string     `json:"document_type"`
	DocumentNumber  *string    `json:"document_number,omitempty"`
	FileURL         string     `json:"file_url"`
	Status          string     `json:"status"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// SubscriptionPlan is one row in rider_subscription_plans.
type SubscriptionPlan struct {
	ID                uuid.UUID `json:"id"`
	Code              string    `json:"code"`
	Name              string    `json:"name"`
	Description       *string   `json:"description,omitempty"`
	PriceAmount       float64   `json:"price_amount"`
	CurrencyCode      string    `json:"currency_code"`
	BillingPeriodDays int       `json:"billing_period_days"`
	LeadLimit         *int      `json:"lead_limit,omitempty"`
	FairUseLimit      *int      `json:"fair_use_limit,omitempty"`
	PriorityWeight    int       `json:"priority_weight"`
	IsUnlimited       bool      `json:"is_unlimited"`
	IsFleetPlan       bool      `json:"is_fleet_plan"`
	MaxDrivers        *int      `json:"max_drivers,omitempty"`
	GracePeriodDays   int       `json:"grace_period_days"`
	IsActive          bool      `json:"is_active"`
}

// PartnerSubscription is one row in rider_partner_subscriptions.
type PartnerSubscription struct {
	ID            uuid.UUID  `json:"id"`
	PartnerID     uuid.UUID  `json:"partner_id"`
	PlanID        uuid.UUID  `json:"plan_id"`
	Status        string     `json:"status"`
	StartsAt      time.Time  `json:"starts_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	GraceEndsAt   *time.Time `json:"grace_ends_at,omitempty"`
	LeadsUsed     int        `json:"leads_used"`
	FairUseUsed   int        `json:"fair_use_used"`
	AutoRenew     bool       `json:"auto_renew"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// SubscriptionPayment is one row in rider_subscription_payments.
type SubscriptionPayment struct {
	ID               uuid.UUID  `json:"id"`
	PartnerID        uuid.UUID  `json:"partner_id"`
	SubscriptionID   *uuid.UUID `json:"subscription_id,omitempty"`
	PlanID           uuid.UUID  `json:"plan_id"`
	Amount           float64    `json:"amount"`
	CurrencyCode     string     `json:"currency_code"`
	PaymentMethod    string     `json:"payment_method"`
	PaymentReference *string    `json:"payment_reference,omitempty"`
	PaymentProofURL  *string    `json:"payment_proof_url,omitempty"`
	WalletTxnID      *uuid.UUID `json:"wallet_txn_id,omitempty"`
	Status           string     `json:"status"`
	RejectionReason  *string    `json:"rejection_reason,omitempty"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// FareRule is one row in rider_fare_rules.
type FareRule struct {
	ID               uuid.UUID `json:"id"`
	CityID           uuid.UUID `json:"city_id"`
	VehicleType      string    `json:"vehicle_type"`
	BaseFare         float64   `json:"base_fare"`
	PerKMFare        float64   `json:"per_km_fare"`
	PerMinuteFare    float64   `json:"per_minute_fare"`
	MinimumFare      float64   `json:"minimum_fare"`
	PlatformFee      float64   `json:"platform_fee"`
	NightMultiplier  float64   `json:"night_multiplier"`
	PeakMultiplier   float64   `json:"peak_multiplier"`
	CancellationFee  float64   `json:"cancellation_fee"`
	IsActive         bool      `json:"is_active"`
	StartsAt         time.Time `json:"starts_at"`
}

// Ride is one row in rider_rides.
type Ride struct {
	ID                   uuid.UUID  `json:"id"`
	CustomerUserID       uuid.UUID  `json:"customer_user_id"`
	PartnerID            *uuid.UUID `json:"partner_id,omitempty"`
	VehicleID            *uuid.UUID `json:"vehicle_id,omitempty"`
	CityID               *uuid.UUID `json:"city_id,omitempty"`
	VehicleType          string     `json:"vehicle_type"`
	Status               string     `json:"status"`
	PickupAddress        string     `json:"pickup_address"`
	PickupLat            float64    `json:"pickup_lat"`
	PickupLng            float64    `json:"pickup_lng"`
	DropAddress          string     `json:"drop_address"`
	DropLat              float64    `json:"drop_lat"`
	DropLng              float64    `json:"drop_lng"`
	EstimatedDistanceKM  *float64   `json:"estimated_distance_km,omitempty"`
	EstimatedDurationMin *float64   `json:"estimated_duration_min,omitempty"`
	EstimatedFare        *float64   `json:"estimated_fare,omitempty"`
	PaymentMethod        *string    `json:"payment_method,omitempty"`
	OTPExpiresAt         *time.Time `json:"otp_expires_at,omitempty"`
	RequestedAt          time.Time  `json:"requested_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// RideStatusHistory is one row in rider_ride_status_history.
type RideStatusHistory struct {
	ID          uuid.UUID  `json:"id"`
	RideID      uuid.UUID  `json:"ride_id"`
	FromStatus  *string    `json:"from_status,omitempty"`
	ToStatus    string     `json:"to_status"`
	ActorKind   string     `json:"actor_kind"`
	ActorUserID *uuid.UUID `json:"actor_user_id,omitempty"`
	Reason      *string    `json:"reason,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// RideOffer is one row in rider_ride_offers.
type RideOffer struct {
	ID         uuid.UUID  `json:"id"`
	RideID     uuid.UUID  `json:"ride_id"`
	PartnerID  uuid.UUID  `json:"partner_id"`
	Score      float64    `json:"score"`
	DistanceKM *float64   `json:"distance_km,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	Status     string     `json:"status"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// PartnerLocation is one row in rider_partner_locations.
type PartnerLocation struct {
	PartnerID    uuid.UUID `json:"partner_id"`
	LastLat      float64   `json:"last_lat"`
	LastLng      float64   `json:"last_lng"`
	LastGeohash  string    `json:"last_geohash"`
	LastSpeedMPS *float64  `json:"last_speed_mps,omitempty"`
	LastHeading  *float64  `json:"last_heading,omitempty"`
	IsOnline     bool      `json:"is_online"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RidePayment is one row in rider_ride_payments.
type RidePayment struct {
	ID            uuid.UUID  `json:"id"`
	RideID        uuid.UUID  `json:"ride_id"`
	PartnerID     uuid.UUID  `json:"partner_id"`
	AmountPaise   int64      `json:"amount_paise"`
	PaymentMethod string     `json:"payment_method"`
	Status        string     `json:"status"`
	WalletTxnID   *uuid.UUID `json:"wallet_txn_id,omitempty"`
	UPITxnRef     *string    `json:"upi_txn_ref,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
}

// IdempotencyRecord deduplicates a payment-touching API call. Mirrors
// wallet-service's wallet.idempotency table shape.
type IdempotencyRecord struct {
	Key          string     `json:"key"`
	UserID       uuid.UUID  `json:"user_id"`
	Operation    string     `json:"operation"`
	ResourceID   *uuid.UUID `json:"resource_id,omitempty"`
	ResponseBody []byte     `json:"response_body,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
}
