package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrSafetyIncidentNotFound is returned when GetSafetyIncident finds no row.
var ErrSafetyIncidentNotFound = errors.New("safety_incident: not found")

// ErrShareTokenNotFound is returned when LookupShareToken misses.
var ErrShareTokenNotFound = errors.New("share_token: not found")

// ErrShareTokenExpired is returned when a token row is found but expired.
var ErrShareTokenExpired = errors.New("share_token: expired")

// ErrTrustedContactNotFound is returned when GetTrustedContact misses.
var ErrTrustedContactNotFound = errors.New("trusted_contact: not found")

// allowedSafetyKinds is the closed set rider_safety_incidents.kind accepts.
var allowedSafetyKinds = map[string]bool{
	"sos_triggered":       true,
	"route_anomaly":       true,
	"partner_no_show":     true,
	"long_idle_in_progress": true,
}

// allowedSafetySeverities is the CHECK constraint set.
var allowedSafetySeverities = map[string]bool{
	"low":      true,
	"medium":   true,
	"high":     true,
	"critical": true,
}

// IsValidSafetyKind exposes the kind whitelist to the service layer.
func IsValidSafetyKind(kind string) bool { return allowedSafetyKinds[kind] }

// IsValidSafetySeverity exposes the severity whitelist to the service layer.
func IsValidSafetySeverity(s string) bool { return allowedSafetySeverities[s] }

// SafetyIncident is one row in rider_safety_incidents.
type SafetyIncident struct {
	ID              uuid.UUID  `json:"id"`
	RideID          *uuid.UUID `json:"ride_id,omitempty"`
	CustomerID      *uuid.UUID `json:"customer_id,omitempty"`
	PartnerID       *uuid.UUID `json:"partner_id,omitempty"`
	Kind            string     `json:"kind"`
	Severity        string     `json:"severity"`
	Metadata        []byte     `json:"metadata,omitempty"`
	Status          string     `json:"status"`
	AcknowledgedBy  *uuid.UUID `json:"acknowledged_by,omitempty"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedBy      *uuid.UUID `json:"resolved_by,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CreateSafetyIncidentInput captures the fields the service supplies.
type CreateSafetyIncidentInput struct {
	RideID     *uuid.UUID
	CustomerID *uuid.UUID
	PartnerID  *uuid.UUID
	Kind       string
	Severity   string
	Metadata   []byte
}

// CreateSafetyIncident inserts an incident row in `open` status.
func (s *Store) CreateSafetyIncident(ctx context.Context, in CreateSafetyIncidentInput) (*SafetyIncident, error) {
	if in.Severity == "" {
		in.Severity = "medium"
	}
	if len(in.Metadata) == 0 {
		in.Metadata = []byte(`{}`)
	}
	const q = `
        INSERT INTO rider_safety_incidents (ride_id, customer_id, partner_id, kind, severity, metadata, status)
        VALUES ($1, $2, $3, $4, $5, $6, 'open')
        RETURNING id, ride_id, customer_id, partner_id, kind, severity, metadata, status,
                  acknowledged_by, acknowledged_at, resolved_by, resolved_at, created_at`
	row := s.db.QueryRow(ctx, q, in.RideID, in.CustomerID, in.PartnerID, in.Kind, in.Severity, in.Metadata)
	return scanSafetyIncident(row)
}

// GetSafetyIncident returns one incident by id.
func (s *Store) GetSafetyIncident(ctx context.Context, id uuid.UUID) (*SafetyIncident, error) {
	const q = `
        SELECT id, ride_id, customer_id, partner_id, kind, severity, metadata, status,
               acknowledged_by, acknowledged_at, resolved_by, resolved_at, created_at
        FROM rider_safety_incidents
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	i, err := scanSafetyIncident(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSafetyIncidentNotFound
		}
		return nil, err
	}
	return i, nil
}

// ListSafetyIncidents returns incidents filtered by status. status="" => all.
func (s *Store) ListSafetyIncidents(ctx context.Context, status string, limit, offset int) ([]SafetyIncident, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
        SELECT id, ride_id, customer_id, partner_id, kind, severity, metadata, status,
               acknowledged_by, acknowledged_at, resolved_by, resolved_at, created_at
        FROM rider_safety_incidents
        WHERE ($1::text IS NULL OR status = $1)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	rows, err := s.db.Query(ctx, q, statusPtr, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list safety incidents: %w", err)
	}
	defer rows.Close()
	var out []SafetyIncident
	for rows.Next() {
		i, err := scanSafetyIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// AcknowledgeSafetyIncident moves status open -> acknowledged.
func (s *Store) AcknowledgeSafetyIncident(ctx context.Context, id, adminID uuid.UUID) (*SafetyIncident, error) {
	const q = `
        UPDATE rider_safety_incidents
        SET status          = 'acknowledged',
            acknowledged_by = $2,
            acknowledged_at = NOW()
        WHERE id = $1 AND status = 'open'
        RETURNING id, ride_id, customer_id, partner_id, kind, severity, metadata, status,
                  acknowledged_by, acknowledged_at, resolved_by, resolved_at, created_at`
	row := s.db.QueryRow(ctx, q, id, adminID)
	i, err := scanSafetyIncident(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSafetyIncidentNotFound
		}
		return nil, err
	}
	return i, nil
}

// ResolveSafetyIncident moves status to resolved. The note is appended into
// metadata under the "resolution" key so the audit trail stays JSONB-shaped.
func (s *Store) ResolveSafetyIncident(ctx context.Context, id, adminID uuid.UUID, note string) (*SafetyIncident, error) {
	const q = `
        UPDATE rider_safety_incidents
        SET status      = 'resolved',
            resolved_by = $2,
            resolved_at = NOW(),
            metadata    = jsonb_set(metadata, '{resolution}', to_jsonb($3::text), true)
        WHERE id = $1 AND status IN ('open','acknowledged')
        RETURNING id, ride_id, customer_id, partner_id, kind, severity, metadata, status,
                  acknowledged_by, acknowledged_at, resolved_by, resolved_at, created_at`
	row := s.db.QueryRow(ctx, q, id, adminID, note)
	i, err := scanSafetyIncident(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSafetyIncidentNotFound
		}
		return nil, err
	}
	return i, nil
}

// HasOpenIncidentForRideKind returns true when an incident with status in
// (open, acknowledged) already exists for the (ride, kind) pair. Used by
// the stale-ride sweeper to avoid creating duplicate "partner_no_show"
// incidents on every cron tick.
func (s *Store) HasOpenIncidentForRideKind(ctx context.Context, rideID uuid.UUID, kind string) (bool, error) {
	const q = `
        SELECT EXISTS (
            SELECT 1 FROM rider_safety_incidents
            WHERE ride_id = $1 AND kind = $2 AND status IN ('open','acknowledged')
        )`
	var exists bool
	if err := s.db.QueryRow(ctx, q, rideID, kind).Scan(&exists); err != nil {
		return false, fmt.Errorf("has open incident: %w", err)
	}
	return exists, nil
}

// CountOpenSafetyIncidents returns the number of incidents not yet resolved.
// Used by the admin dashboard counter.
func (s *Store) CountOpenSafetyIncidents(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*)::int FROM rider_safety_incidents WHERE status IN ('open','acknowledged')`
	var n int
	if err := s.db.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count open safety incidents: %w", err)
	}
	return n, nil
}

func scanSafetyIncident(row pgx.Row) (*SafetyIncident, error) {
	var i SafetyIncident
	if err := row.Scan(
		&i.ID, &i.RideID, &i.CustomerID, &i.PartnerID, &i.Kind, &i.Severity, &i.Metadata, &i.Status,
		&i.AcknowledgedBy, &i.AcknowledgedAt, &i.ResolvedBy, &i.ResolvedAt, &i.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &i, nil
}

// --- Share tokens ---------------------------------------------------------

// ShareToken is one row in rider_share_tokens.
type ShareToken struct {
	Token        string     `json:"token"`
	RideID       uuid.UUID  `json:"ride_id"`
	CustomerID   uuid.UUID  `json:"customer_id"`
	ExpiresAt    time.Time  `json:"expires_at"`
	ViewCount    int        `json:"view_count"`
	LastViewedAt *time.Time `json:"last_viewed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// CreateShareToken inserts a share-token row.
func (s *Store) CreateShareToken(ctx context.Context, token string, rideID, customerID uuid.UUID, expiresAt time.Time) (*ShareToken, error) {
	const q = `
        INSERT INTO rider_share_tokens (token, ride_id, customer_id, expires_at)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (token) DO NOTHING
        RETURNING token, ride_id, customer_id, expires_at, view_count, last_viewed_at, created_at`
	row := s.db.QueryRow(ctx, q, token, rideID, customerID, expiresAt)
	t, err := scanShareToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Conflict — token already present, return existing.
			return s.LookupShareToken(ctx, token)
		}
		return nil, err
	}
	return t, nil
}

// LookupShareToken returns the share-token row for the given token. Does
// NOT increment view_count — callers that want to track views must call
// MarkShareTokenViewed in addition.
func (s *Store) LookupShareToken(ctx context.Context, token string) (*ShareToken, error) {
	const q = `
        SELECT token, ride_id, customer_id, expires_at, view_count, last_viewed_at, created_at
        FROM rider_share_tokens
        WHERE token = $1`
	row := s.db.QueryRow(ctx, q, token)
	t, err := scanShareToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrShareTokenNotFound
		}
		return nil, err
	}
	if t.ExpiresAt.Before(time.Now().UTC()) {
		return t, ErrShareTokenExpired
	}
	return t, nil
}

// MarkShareTokenViewed bumps view_count + stamps last_viewed_at = now().
func (s *Store) MarkShareTokenViewed(ctx context.Context, token string) error {
	const q = `
        UPDATE rider_share_tokens
        SET view_count     = view_count + 1,
            last_viewed_at = NOW()
        WHERE token = $1`
	tag, err := s.db.Exec(ctx, q, token)
	if err != nil {
		return fmt.Errorf("mark share token viewed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrShareTokenNotFound
	}
	return nil
}

func scanShareToken(row pgx.Row) (*ShareToken, error) {
	var t ShareToken
	if err := row.Scan(&t.Token, &t.RideID, &t.CustomerID, &t.ExpiresAt, &t.ViewCount, &t.LastViewedAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// --- Trusted contact ------------------------------------------------------

// TrustedContact is one row in rider_trusted_contacts.
type TrustedContact struct {
	UserID               uuid.UUID `json:"user_id"`
	ContactName          string    `json:"contact_name"`
	ContactPhone         string    `json:"contact_phone"`
	ContactRelationship  *string   `json:"contact_relationship,omitempty"`
	ShareLocationOnSOS   bool      `json:"share_location_on_sos"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// UpsertTrustedContactInput is the input shape for UpsertTrustedContact.
type UpsertTrustedContactInput struct {
	UserID              uuid.UUID
	ContactName         string
	ContactPhone        string
	ContactRelationship *string
	ShareLocationOnSOS  bool
}

// UpsertTrustedContact inserts-or-updates the trusted-contact row for the
// user. Each user has at most one contact (PRIMARY KEY is user_id).
func (s *Store) UpsertTrustedContact(ctx context.Context, in UpsertTrustedContactInput) (*TrustedContact, error) {
	const q = `
        INSERT INTO rider_trusted_contacts (user_id, contact_name, contact_phone, contact_relationship, share_location_on_sos)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (user_id) DO UPDATE SET
            contact_name          = EXCLUDED.contact_name,
            contact_phone         = EXCLUDED.contact_phone,
            contact_relationship  = EXCLUDED.contact_relationship,
            share_location_on_sos = EXCLUDED.share_location_on_sos,
            updated_at            = NOW()
        RETURNING user_id, contact_name, contact_phone, contact_relationship, share_location_on_sos, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.UserID, in.ContactName, in.ContactPhone, in.ContactRelationship, in.ShareLocationOnSOS)
	return scanTrustedContact(row)
}

// GetTrustedContact returns the trusted contact for the user.
func (s *Store) GetTrustedContact(ctx context.Context, userID uuid.UUID) (*TrustedContact, error) {
	const q = `
        SELECT user_id, contact_name, contact_phone, contact_relationship, share_location_on_sos, created_at, updated_at
        FROM rider_trusted_contacts
        WHERE user_id = $1`
	row := s.db.QueryRow(ctx, q, userID)
	t, err := scanTrustedContact(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTrustedContactNotFound
		}
		return nil, err
	}
	return t, nil
}

func scanTrustedContact(row pgx.Row) (*TrustedContact, error) {
	var t TrustedContact
	if err := row.Scan(&t.UserID, &t.ContactName, &t.ContactPhone, &t.ContactRelationship, &t.ShareLocationOnSOS, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
