package store

// Government-verified onboarding (migration 005): documents rider-service
// fetched from the partner's DigiLocker are recorded with source digilocker
// and verified automatically; a manually uploaded document keeps the admin
// review path. Every automatic verification and approval writes a
// rider_admin_audit_logs row under the fixed system actor.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Document sources and the automatic verifier label.
const (
	DocSourceDigiLocker = "digilocker"
	DocSourceUpload     = "upload"
	VerifiedByAuto      = "auto"
)

// UpsertDigiLockerDocumentInput records one document fetched from
// DigiLocker for the partner, already verified.
type UpsertDigiLockerDocumentInput struct {
	PartnerID      uuid.UUID
	DocumentType   string
	DocumentNumber *string
	FileURL        string
	ExpiresAt      *time.Time
	PhotoMediaID   *uuid.UUID
	DigiLockerRef  string
}

// UpsertDigiLockerDocument inserts (or refreshes the latest) partner
// document of the type with source digilocker, status approved,
// verified_by_actor auto, plus the audit row, in one transaction. A
// pending/rejected UPLOADED document of the same type is superseded (left
// as is; the approval evaluator reads the latest approved one).
func (s *Store) UpsertDigiLockerDocument(ctx context.Context, in UpsertDigiLockerDocumentInput) (*PartnerDocument, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Refresh an existing DigiLocker row of the type rather than piling up.
	row := tx.QueryRow(ctx, `
        UPDATE rider_partner_documents
        SET document_number = $3, file_url = $4, expires_at = $5, photo_media_id = $6, digilocker_ref = $7,
            status = 'approved', rejection_reason = NULL, verified_by_actor = 'auto', verified_at = NOW(),
            verified_by = $8, updated_at = NOW()
        WHERE id = (SELECT id FROM rider_partner_documents
                    WHERE partner_id = $1 AND document_type = $2::rider_document_type AND source = 'digilocker'
                    ORDER BY created_at DESC LIMIT 1)
        RETURNING `+partnerDocColumns, in.PartnerID, in.DocumentType, in.DocumentNumber, in.FileURL, in.ExpiresAt,
		in.PhotoMediaID, in.DigiLockerRef, SystemActorID)
	d, err := scanPartnerDoc(row)
	if errors.Is(err, pgx.ErrNoRows) {
		row = tx.QueryRow(ctx, `
            INSERT INTO rider_partner_documents (partner_id, document_type, document_number, file_url, status, expires_at,
                                                 source, verified_by_actor, verified_at, verified_by, photo_media_id, digilocker_ref)
            VALUES ($1, $2::rider_document_type, $3, $4, 'approved', $5, 'digilocker', 'auto', NOW(), $8, $6, $7)
            RETURNING `+partnerDocColumns, in.PartnerID, in.DocumentType, in.DocumentNumber, in.FileURL, in.ExpiresAt,
			in.PhotoMediaID, in.DigiLockerRef, SystemActorID)
		d, err = scanPartnerDoc(row)
	}
	if err != nil {
		return nil, fmt.Errorf("upsert digilocker document: %w", err)
	}
	if err := recordSystemAuditTx(ctx, tx, "document.auto_verify", "document", d.ID, map[string]any{
		"actor": "system", "reason": "digilocker", "partner_id": in.PartnerID.String(), "document_type": in.DocumentType,
	}); err != nil {
		return nil, err
	}
	return d, tx.Commit(ctx)
}

// CreatePartnerDocumentWithMedia is CreatePartnerDocument plus the
// media-service id of the uploaded file (the selfie the face check reads).
func (s *Store) CreatePartnerDocumentWithMedia(ctx context.Context, in CreatePartnerDocumentInput, mediaID *uuid.UUID) (*PartnerDocument, error) {
	row := s.db.QueryRow(ctx, `
        INSERT INTO rider_partner_documents (partner_id, document_type, document_number, file_url, status, expires_at, source, media_id)
        VALUES ($1, $2::rider_document_type, $3, $4, 'pending', $5, 'upload', $6)
        RETURNING `+partnerDocColumns, in.PartnerID, in.DocumentType, in.DocumentNumber, in.FileURL, in.ExpiresAt, mediaID)
	return scanPartnerDoc(row)
}

// AutoVerifyPartnerDocument marks an uploaded document approved by an
// automatic check (the selfie face compare) with the audit row. It only
// moves a pending row.
func (s *Store) AutoVerifyPartnerDocument(ctx context.Context, id uuid.UUID, reason, detail string) (*PartnerDocument, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	d, err := scanPartnerDoc(tx.QueryRow(ctx, `
        UPDATE rider_partner_documents
        SET status = 'approved', rejection_reason = NULL, verified_by_actor = 'auto', verified_at = NOW(), verified_by = $2,
            auto_check_detail = NULLIF($3, ''), updated_at = NOW()
        WHERE id = $1 AND status = 'pending'
        RETURNING `+partnerDocColumns, id, SystemActorID, detail))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDocumentNotFound
		}
		return nil, fmt.Errorf("auto verify document: %w", err)
	}
	if err := recordSystemAuditTx(ctx, tx, "document.auto_verify", "document", d.ID, map[string]any{
		"actor": "system", "reason": reason, "detail": detail, "partner_id": d.PartnerID.String(), "document_type": d.DocumentType,
	}); err != nil {
		return nil, err
	}
	return d, tx.Commit(ctx)
}

// SetDocumentAutoCheckDetail records why an automatic check left the
// document pending (below threshold, media-service unavailable, ...).
func (s *Store) SetDocumentAutoCheckDetail(ctx context.Context, id uuid.UUID, detail string) error {
	_, err := s.db.Exec(ctx, `UPDATE rider_partner_documents SET auto_check_detail = $2, updated_at = NOW() WHERE id = $1`, id, detail)
	return err
}

// SetPartnerDocumentStatusBy is the admin path with the actor recorded
// (verified_by_actor = the admin's id).
func (s *Store) SetPartnerDocumentStatusBy(ctx context.Context, id uuid.UUID, status string, reason *string, adminID uuid.UUID) (*PartnerDocument, error) {
	d, err := scanPartnerDoc(s.db.QueryRow(ctx, `
        UPDATE rider_partner_documents
        SET status = $2::rider_verification_status, rejection_reason = $3, verified_by_actor = $4, verified_by = $5,
            verified_at = CASE WHEN $2 = 'approved' THEN NOW() ELSE verified_at END, updated_at = NOW()
        WHERE id = $1
        RETURNING `+partnerDocColumns, id, status, reason, adminID.String(), adminID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}
	return d, nil
}

// --- Vehicles ------------------------------------------------------------

// UpsertDigiLockerVehicleRC records the vehicle's RC fetched from DigiLocker
// (rider_vehicle_documents, source digilocker, approved) and approves the
// vehicle with verified_by_actor auto, plus the audit rows, in one
// transaction.
func (s *Store) UpsertDigiLockerVehicleRC(ctx context.Context, vehicleID uuid.UUID, fileURL string, expiresAt *time.Time, ref string) (*VehicleDocument, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `
        UPDATE rider_vehicle_documents
        SET file_url = $2, expires_at = $3, digilocker_ref = $4, status = 'approved', rejection_reason = NULL,
            verified_by_actor = 'auto', verified_at = NOW(), updated_at = NOW()
        WHERE id = (SELECT id FROM rider_vehicle_documents
                    WHERE vehicle_id = $1 AND document_type = 'vehicle_rc' AND source = 'digilocker'
                    ORDER BY created_at DESC LIMIT 1)
        RETURNING `+vehicleDocColumns, vehicleID, fileURL, expiresAt, ref)
	d, err := scanVehicleDoc(row)
	if errors.Is(err, pgx.ErrNoRows) {
		row = tx.QueryRow(ctx, `
            INSERT INTO rider_vehicle_documents (vehicle_id, document_type, file_url, status, expires_at, source, verified_by_actor, verified_at, digilocker_ref)
            VALUES ($1, 'vehicle_rc', $2, 'approved', $3, 'digilocker', 'auto', NOW(), $4)
            RETURNING `+vehicleDocColumns, vehicleID, fileURL, expiresAt, ref)
		d, err = scanVehicleDoc(row)
	}
	if err != nil {
		return nil, fmt.Errorf("upsert digilocker rc: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        UPDATE rider_vehicles SET status = 'approved', verified_by_actor = 'auto', verified_at = NOW(), updated_at = NOW()
        WHERE id = $1 AND status <> 'approved'`, vehicleID); err != nil {
		return nil, fmt.Errorf("auto approve vehicle: %w", err)
	}
	if err := recordSystemAuditTx(ctx, tx, "vehicle.auto_verify", "vehicle", vehicleID, map[string]any{
		"actor": "system", "reason": "digilocker", "document_id": d.ID.String(),
	}); err != nil {
		return nil, err
	}
	return d, tx.Commit(ctx)
}

// SetVehicleStatusBy is the admin path with the actor recorded.
func (s *Store) SetVehicleStatusBy(ctx context.Context, id uuid.UUID, status string, adminID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE rider_vehicles
        SET status = $2::rider_verification_status, verified_by_actor = $3,
            verified_at = CASE WHEN $2 = 'approved' THEN NOW() ELSE verified_at END, updated_at = NOW()
        WHERE id = $1`, id, status, adminID.String())
	if err != nil {
		return fmt.Errorf("set vehicle status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVehicleNotFoundAdmin
	}
	return nil
}

// --- Approval evaluation reads --------------------------------------------

// DocumentState is the latest row of one document type for the evaluator.
type DocumentState struct {
	ID     uuid.UUID
	Type   string
	Status string
	Source string
}

// LatestPartnerDocuments returns, per document type, the partner's most
// recent document (an approved row wins over a later pending upload of the
// same type, so a re-upload never un-verifies a DigiLocker document).
func (s *Store) LatestPartnerDocuments(ctx context.Context, partnerID uuid.UUID) (map[string]DocumentState, error) {
	rows, err := s.db.Query(ctx, `
        SELECT DISTINCT ON (document_type) id, document_type::text, status::text, source
        FROM rider_partner_documents
        WHERE partner_id = $1
        ORDER BY document_type, (status = 'approved') DESC, created_at DESC`, partnerID)
	if err != nil {
		return nil, fmt.Errorf("latest partner documents: %w", err)
	}
	defer rows.Close()
	out := map[string]DocumentState{}
	for rows.Next() {
		var d DocumentState
		if err := rows.Scan(&d.ID, &d.Type, &d.Status, &d.Source); err != nil {
			return nil, err
		}
		out[d.Type] = d
	}
	return out, rows.Err()
}

// VehicleState is one active vehicle with its latest RC document.
type VehicleState struct {
	ID                 uuid.UUID
	RegistrationNumber string
	Status             string
	RCStatus           string
	RCSource           string
}

// PartnerVehicleStates lists the partner's active vehicles with their
// latest RC document (approved wins).
func (s *Store) PartnerVehicleStates(ctx context.Context, partnerID uuid.UUID) ([]VehicleState, error) {
	rows, err := s.db.Query(ctx, `
        SELECT v.id, v.registration_number, v.status::text,
               COALESCE(d.status::text, ''), COALESCE(d.source, '')
        FROM rider_vehicles v
        LEFT JOIN LATERAL (
            SELECT status, source FROM rider_vehicle_documents
            WHERE vehicle_id = v.id AND document_type = 'vehicle_rc'
            ORDER BY (status = 'approved') DESC, created_at DESC LIMIT 1
        ) d ON TRUE
        WHERE v.partner_id = $1 AND v.deleted_at IS NULL AND v.is_active
        ORDER BY v.created_at ASC`, partnerID)
	if err != nil {
		return nil, fmt.Errorf("partner vehicle states: %w", err)
	}
	defer rows.Close()
	var out []VehicleState
	for rows.Next() {
		var v VehicleState
		if err := rows.Scan(&v.ID, &v.RegistrationNumber, &v.Status, &v.RCStatus, &v.RCSource); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetPartnerDocumentByType returns the partner's latest document of the
// type (approved first), or ErrDocumentNotFound.
func (s *Store) GetPartnerDocumentByType(ctx context.Context, partnerID uuid.UUID, docType string) (*PartnerDocument, error) {
	d, err := scanPartnerDoc(s.db.QueryRow(ctx, `
        SELECT `+partnerDocColumns+`
        FROM rider_partner_documents
        WHERE partner_id = $1 AND document_type = $2::rider_document_type
        ORDER BY (status = 'approved') DESC, created_at DESC LIMIT 1`, partnerID, docType))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}
	return d, nil
}

// AutoApprovePartner approves the partner (status approved, kyc approved,
// the identity role intent) with the audit row, in one transaction. A
// partner already approved is a no-op (false).
func (s *Store) AutoApprovePartner(ctx context.Context, partnerID uuid.UUID, detail map[string]any) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
        UPDATE rider_partners
        SET status = 'approved', kyc_status = 'approved', approved_at = NOW(), updated_at = NOW()
        WHERE id = $1 AND deleted_at IS NULL AND status IN ('draft','pending_verification')
        RETURNING user_id`, partnerID).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("auto approve partner: %w", err)
	}
	if op, ok := riderRoleForStatus("approved"); ok {
		if err := s.enqueueRoleIntentTx(ctx, tx, op, userID, "rider partner approved automatically"); err != nil {
			return false, err
		}
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["actor"] = "system"
	if err := recordSystemAuditTx(ctx, tx, "partner.auto_approve", "partner", partnerID, detail); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// UnderReviewPending returns the pending set the last under_review event
// carried for the partner (nil when none was recorded).
func (s *Store) UnderReviewPending(ctx context.Context, partnerID uuid.UUID) ([]string, bool, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `
        SELECT new_value FROM rider_admin_audit_logs
        WHERE action = 'partner.under_review' AND entity_type = 'partner' AND entity_id = $1
        ORDER BY created_at DESC LIMIT 1`, partnerID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var v struct {
		Pending []string `json:"pending"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, nil
	}
	if v.Pending == nil {
		v.Pending = []string{}
	}
	return v.Pending, true, nil
}

// RecordUnderReview writes the audit row that remembers the pending set the
// under_review event was published for.
func (s *Store) RecordUnderReview(ctx context.Context, partnerID uuid.UUID, pending []string) error {
	body, _ := json.Marshal(map[string]any{"actor": "system", "pending": pending})
	_, err := s.RecordAudit(ctx, RecordAuditInput{AdminUserID: SystemActorID, Action: "partner.under_review", EntityType: "partner", EntityID: &partnerID, NewValue: body})
	return err
}

// recordSystemAuditTx writes one audit row under the system actor.
func recordSystemAuditTx(ctx context.Context, tx pgx.Tx, action, entityType string, entityID uuid.UUID, detail map[string]any) error {
	body, _ := json.Marshal(detail)
	id := entityID
	_, err := RecordAuditTx(ctx, tx, RecordAuditInput{AdminUserID: SystemActorID, Action: action, EntityType: entityType, EntityID: &id, NewValue: body})
	return err
}

// SystemActorID is the fixed actor automatic verifications, approvals and
// refunds are recorded under. Same value as payments.SystemActorID (the
// store cannot import payments' constant without a cycle in tests).
var SystemActorID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/actor/mopedu-system"))

const partnerDocColumns = `id, partner_id, document_type, document_number, file_url, status, rejection_reason, expires_at, created_at, updated_at, source, verified_by_actor, verified_at, media_id, photo_media_id, auto_check_detail`
const vehicleDocColumns = `id, vehicle_id, document_type, document_number, file_url, status, rejection_reason, expires_at, created_at, updated_at, source, verified_by_actor, verified_at`
