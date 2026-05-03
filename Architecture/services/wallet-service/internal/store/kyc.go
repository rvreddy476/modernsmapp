package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrKYCNotFound indicates the user has no KYC record.
var ErrKYCNotFound = errors.New("kyc: not found")

// GetKYC returns the user's KYC record, or ErrKYCNotFound.
func (s *Store) GetKYC(ctx context.Context, userID uuid.UUID) (*KYCRecord, error) {
	const q = `
        SELECT user_id, tier, aadhaar_status, digilocker_ref, pan_status, pan_masked,
               address_proof_ref, submitted_at, verified_at, rejection_reason
        FROM wallet.kyc_records WHERE user_id = $1`
	row := s.db.QueryRow(ctx, q, userID)
	var rec KYCRecord
	var tier string
	if err := row.Scan(
		&rec.UserID, &tier, &rec.AadhaarStatus, &rec.DigiLockerRef,
		&rec.PANStatus, &rec.PANMasked, &rec.AddressProofRef,
		&rec.SubmittedAt, &rec.VerifiedAt, &rec.RejectionReason,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKYCNotFound
		}
		return nil, fmt.Errorf("get kyc: %w", err)
	}
	rec.Tier = KYCTier(tier)
	return &rec, nil
}

// UpsertAadhaarVerified records a successful Aadhaar verification. DPDP:
// digilockerRef is the partner-supplied opaque assertion id. The raw Aadhaar
// number is NEVER passed to or stored by this method.
func (s *Store) UpsertAadhaarVerified(ctx context.Context, userID uuid.UUID, digilockerRef string) error {
	const q = `
        INSERT INTO wallet.kyc_records (user_id, tier, aadhaar_status, digilocker_ref, submitted_at, verified_at)
        VALUES ($1, 'full', 'verified', $2, now(), now())
        ON CONFLICT (user_id) DO UPDATE
        SET tier = CASE WHEN wallet.kyc_records.tier = 'enhanced' THEN 'enhanced' ELSE 'full' END,
            aadhaar_status = 'verified',
            digilocker_ref = EXCLUDED.digilocker_ref,
            verified_at = now()`
	if _, err := s.db.Exec(ctx, q, userID, digilockerRef); err != nil {
		return fmt.Errorf("upsert aadhaar verified: %w", err)
	}
	return nil
}

// SetPANStatus records the PAN submission outcome. The masked argument MUST
// be only the last 4 chars of the PAN (callers mask before invoking).
func (s *Store) SetPANStatus(ctx context.Context, userID uuid.UUID, masked, status string) error {
	const q = `
        INSERT INTO wallet.kyc_records (user_id, tier, pan_masked, pan_status, submitted_at)
        VALUES ($1, 'minimal', $2, $3, now())
        ON CONFLICT (user_id) DO UPDATE
        SET pan_masked = EXCLUDED.pan_masked,
            pan_status = EXCLUDED.pan_status,
            submitted_at = now()`
	if _, err := s.db.Exec(ctx, q, userID, masked, status); err != nil {
		return fmt.Errorf("set pan status: %w", err)
	}
	return nil
}

// MarkAadhaarPending stores the "submission started, awaiting partner
// callback" state. Called when a DigiLocker authorize URL is generated.
func (s *Store) MarkAadhaarPending(ctx context.Context, userID uuid.UUID) error {
	const q = `
        INSERT INTO wallet.kyc_records (user_id, tier, aadhaar_status, submitted_at)
        VALUES ($1, 'minimal', 'pending', now())
        ON CONFLICT (user_id) DO UPDATE
        SET aadhaar_status = 'pending',
            submitted_at = now()`
	if _, err := s.db.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("mark aadhaar pending: %w", err)
	}
	return nil
}

// SetRejection records a verification failure with reason.
func (s *Store) SetRejection(ctx context.Context, userID uuid.UUID, reason string) error {
	const q = `
        INSERT INTO wallet.kyc_records (user_id, tier, aadhaar_status, rejection_reason, submitted_at)
        VALUES ($1, 'minimal', 'failed', $2, now())
        ON CONFLICT (user_id) DO UPDATE
        SET aadhaar_status = 'failed',
            rejection_reason = EXCLUDED.rejection_reason`
	if _, err := s.db.Exec(ctx, q, userID, reason); err != nil {
		return fmt.Errorf("set rejection: %w", err)
	}
	return nil
}

// CurrentTier returns the stored tier (defaulting to 'minimal' if absent).
// Useful when service-layer code needs the tier alone without the full row.
func (s *Store) CurrentTier(ctx context.Context, userID uuid.UUID) (KYCTier, error) {
	const q = `SELECT tier FROM wallet.kyc_records WHERE user_id = $1`
	var tier string
	if err := s.db.QueryRow(ctx, q, userID).Scan(&tier); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return KYCMinimal, nil
		}
		return "", err
	}
	return KYCTier(tier), nil
}

// SubmittedRecently returns true if the user submitted KYC within the
// duration. Used to rate-limit retries.
func (s *Store) SubmittedRecently(ctx context.Context, userID uuid.UUID, within time.Duration) (bool, error) {
	const q = `SELECT submitted_at FROM wallet.kyc_records WHERE user_id = $1`
	var submitted *time.Time
	if err := s.db.QueryRow(ctx, q, userID).Scan(&submitted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if submitted == nil {
		return false, nil
	}
	return time.Since(*submitted) < within, nil
}
