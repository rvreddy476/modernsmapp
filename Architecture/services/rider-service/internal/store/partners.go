package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPartnerNotFound is returned when GetPartner / GetPartnerByUserID find
// no matching row.
var ErrPartnerNotFound = errors.New("partner: not found")

// CreatePartnerInput is the input shape for CreatePartner.
type CreatePartnerInput struct {
	UserID      uuid.UUID
	PartnerType string
	FullName    string
	Phone       string
	Email       *string
	CityID      *uuid.UUID
}

// CreatePartner inserts a new partner profile in `draft` status.
//
// The unique index on (user_id) WHERE deleted_at IS NULL prevents two active
// partner rows for the same AtPost user — the service layer translates the
// resulting unique-violation into a friendly "already exists" error.
func (s *Store) CreatePartner(ctx context.Context, in CreatePartnerInput) (*Partner, error) {
	const q = `
        INSERT INTO rider_partners (user_id, partner_type, full_name, phone, email, city_id, status, kyc_status)
        VALUES ($1, $2, $3, $4, $5, $6, 'draft', 'draft')
        RETURNING id, user_id, partner_type, fleet_owner_id, full_name, phone, email, profile_photo_url,
                  city_id, status, kyc_status, bank_status, rating, total_rides_completed, total_rides_cancelled,
                  acceptance_rate, cancellation_rate, fraud_score, is_online, approved_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.UserID, in.PartnerType, in.FullName, in.Phone, in.Email, in.CityID)
	return scanPartner(row)
}

// GetPartner returns the partner row by id.
func (s *Store) GetPartner(ctx context.Context, id uuid.UUID) (*Partner, error) {
	const q = `
        SELECT id, user_id, partner_type, fleet_owner_id, full_name, phone, email, profile_photo_url,
               city_id, status, kyc_status, bank_status, rating, total_rides_completed, total_rides_cancelled,
               acceptance_rate, cancellation_rate, fraud_score, is_online, approved_at, created_at, updated_at
        FROM rider_partners
        WHERE id = $1 AND deleted_at IS NULL`
	row := s.db.QueryRow(ctx, q, id)
	p, err := scanPartner(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPartnerNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetPartnerByUserID returns the active partner row for the AtPost user, or
// ErrPartnerNotFound.
func (s *Store) GetPartnerByUserID(ctx context.Context, userID uuid.UUID) (*Partner, error) {
	const q = `
        SELECT id, user_id, partner_type, fleet_owner_id, full_name, phone, email, profile_photo_url,
               city_id, status, kyc_status, bank_status, rating, total_rides_completed, total_rides_cancelled,
               acceptance_rate, cancellation_rate, fraud_score, is_online, approved_at, created_at, updated_at
        FROM rider_partners
        WHERE user_id = $1 AND deleted_at IS NULL
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, userID)
	p, err := scanPartner(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPartnerNotFound
		}
		return nil, err
	}
	return p, nil
}

// UpdatePartnerProfileInput allows partial updates from PATCH /partners/me.
// Nil fields are skipped.
type UpdatePartnerProfileInput struct {
	FullName        *string
	Email           *string
	ProfilePhotoURL *string
	CityID          *uuid.UUID
}

// UpdatePartnerProfile applies a partial update.
func (s *Store) UpdatePartnerProfile(ctx context.Context, partnerID uuid.UUID, in UpdatePartnerProfileInput) (*Partner, error) {
	const q = `
        UPDATE rider_partners SET
            full_name         = COALESCE($2, full_name),
            email             = COALESCE($3, email),
            profile_photo_url = COALESCE($4, profile_photo_url),
            city_id           = COALESCE($5, city_id),
            updated_at        = NOW()
        WHERE id = $1 AND deleted_at IS NULL
        RETURNING id, user_id, partner_type, fleet_owner_id, full_name, phone, email, profile_photo_url,
                  city_id, status, kyc_status, bank_status, rating, total_rides_completed, total_rides_cancelled,
                  acceptance_rate, cancellation_rate, fraud_score, is_online, approved_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, partnerID, in.FullName, in.Email, in.ProfilePhotoURL, in.CityID)
	p, err := scanPartner(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPartnerNotFound
		}
		return nil, err
	}
	return p, nil
}

// UpdatePartnerStatus changes the partner status (admin-only path; exposed
// here because the service layer reuses it during onboarding too).
func (s *Store) UpdatePartnerStatus(ctx context.Context, partnerID uuid.UUID, status string) error {
	const q = `UPDATE rider_partners SET status = $2::rider_partner_status, updated_at = NOW() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, partnerID, status)
	if err != nil {
		return fmt.Errorf("update partner status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPartnerNotFound
	}
	return nil
}

// UpdatePartnerKYCStatus moves the partner KYC status forward.
func (s *Store) UpdatePartnerKYCStatus(ctx context.Context, partnerID uuid.UUID, status string) error {
	const q = `UPDATE rider_partners SET kyc_status = $2::rider_verification_status, updated_at = NOW() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, partnerID, status)
	if err != nil {
		return fmt.Errorf("update partner kyc status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPartnerNotFound
	}
	return nil
}

// RecordAadhaarVerification persists the DigiLocker assertion. DPDP-compliant:
// no Aadhaar number is accepted by this method — only the partner-supplied
// opaque reference + a hashed document-type label.
func (s *Store) RecordAadhaarVerification(ctx context.Context, partnerID uuid.UUID, ref, docHash string, issuedAt int64) error {
	const q = `
        INSERT INTO rider_partner_aadhaar_verifications (partner_id, digilocker_ref, doc_type_hash, issued_at)
        VALUES ($1, $2, $3, to_timestamp($4))
        ON CONFLICT (partner_id) DO UPDATE SET
            digilocker_ref = EXCLUDED.digilocker_ref,
            doc_type_hash  = EXCLUDED.doc_type_hash,
            issued_at      = EXCLUDED.issued_at`
	if _, err := s.db.Exec(ctx, q, partnerID, ref, docHash, issuedAt); err != nil {
		return fmt.Errorf("record aadhaar verification: %w", err)
	}
	return nil
}

// GetAadhaarVerification fetches the previously stored DigiLocker assertion.
func (s *Store) GetAadhaarVerification(ctx context.Context, partnerID uuid.UUID) (*AadhaarVerification, error) {
	const q = `
        SELECT partner_id, digilocker_ref, doc_type_hash, issued_at, created_at
        FROM rider_partner_aadhaar_verifications
        WHERE partner_id = $1`
	row := s.db.QueryRow(ctx, q, partnerID)
	var v AadhaarVerification
	if err := row.Scan(&v.PartnerID, &v.DigiLockerRef, &v.DocTypeHash, &v.IssuedAt, &v.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPartnerNotFound
		}
		return nil, fmt.Errorf("get aadhaar verification: %w", err)
	}
	return &v, nil
}

// scanPartner is the shared row scanner used by Get / Create / Update.
func scanPartner(row pgx.Row) (*Partner, error) {
	var p Partner
	if err := row.Scan(
		&p.ID, &p.UserID, &p.PartnerType, &p.FleetOwnerID, &p.FullName, &p.Phone, &p.Email, &p.ProfilePhotoURL,
		&p.CityID, &p.Status, &p.KYCStatus, &p.BankStatus, &p.Rating, &p.TotalRidesCompleted, &p.TotalRidesCancelled,
		&p.AcceptanceRate, &p.CancellationRate, &p.FraudScore, &p.IsOnline, &p.ApprovedAt, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}
