package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrDocumentNotFound is returned when GetPartnerDocument or
// GetVehicleDocument find no row.
var ErrDocumentNotFound = errors.New("document: not found")

// CreatePartnerDocumentInput is the input for CreatePartnerDocument.
type CreatePartnerDocumentInput struct {
	PartnerID      uuid.UUID
	DocumentType   string
	DocumentNumber *string
	FileURL        string
	ExpiresAt      *time.Time
}

// CreatePartnerDocument inserts a new partner KYC document in `pending`
// status. The service layer ensures DPDP — for document_type='aadhaar', the
// document_number column never receives the raw Aadhaar; only the DigiLocker
// reference is stored (in rider_partner_aadhaar_verifications).
func (s *Store) CreatePartnerDocument(ctx context.Context, in CreatePartnerDocumentInput) (*PartnerDocument, error) {
	const q = `
        INSERT INTO rider_partner_documents (partner_id, document_type, document_number, file_url, status, expires_at)
        VALUES ($1, $2::rider_document_type, $3, $4, 'pending', $5)
        RETURNING id, partner_id, document_type, document_number, file_url, status, rejection_reason, expires_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.PartnerID, in.DocumentType, in.DocumentNumber, in.FileURL, in.ExpiresAt)
	return scanPartnerDoc(row)
}

// ListPartnerDocuments returns every doc the partner has uploaded.
func (s *Store) ListPartnerDocuments(ctx context.Context, partnerID uuid.UUID) ([]PartnerDocument, error) {
	const q = `
        SELECT id, partner_id, document_type, document_number, file_url, status, rejection_reason, expires_at, created_at, updated_at
        FROM rider_partner_documents
        WHERE partner_id = $1
        ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, q, partnerID)
	if err != nil {
		return nil, fmt.Errorf("list partner docs: %w", err)
	}
	defer rows.Close()
	var out []PartnerDocument
	for rows.Next() {
		d, err := scanPartnerDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// CreateVehicleDocumentInput is the input for CreateVehicleDocument.
type CreateVehicleDocumentInput struct {
	VehicleID      uuid.UUID
	DocumentType   string
	DocumentNumber *string
	FileURL        string
	ExpiresAt      *time.Time
}

// CreateVehicleDocument inserts a new vehicle document in `pending` status.
func (s *Store) CreateVehicleDocument(ctx context.Context, in CreateVehicleDocumentInput) (*VehicleDocument, error) {
	const q = `
        INSERT INTO rider_vehicle_documents (vehicle_id, document_type, document_number, file_url, status, expires_at)
        VALUES ($1, $2::rider_document_type, $3, $4, 'pending', $5)
        RETURNING id, vehicle_id, document_type, document_number, file_url, status, rejection_reason, expires_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q, in.VehicleID, in.DocumentType, in.DocumentNumber, in.FileURL, in.ExpiresAt)
	return scanVehicleDoc(row)
}

// ListVehicleDocuments returns every doc the vehicle has uploaded.
func (s *Store) ListVehicleDocuments(ctx context.Context, vehicleID uuid.UUID) ([]VehicleDocument, error) {
	const q = `
        SELECT id, vehicle_id, document_type, document_number, file_url, status, rejection_reason, expires_at, created_at, updated_at
        FROM rider_vehicle_documents
        WHERE vehicle_id = $1
        ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, q, vehicleID)
	if err != nil {
		return nil, fmt.Errorf("list vehicle docs: %w", err)
	}
	defer rows.Close()
	var out []VehicleDocument
	for rows.Next() {
		d, err := scanVehicleDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func scanPartnerDoc(row pgx.Row) (*PartnerDocument, error) {
	var d PartnerDocument
	if err := row.Scan(&d.ID, &d.PartnerID, &d.DocumentType, &d.DocumentNumber, &d.FileURL, &d.Status, &d.RejectionReason, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

func scanVehicleDoc(row pgx.Row) (*VehicleDocument, error) {
	var d VehicleDocument
	if err := row.Scan(&d.ID, &d.VehicleID, &d.DocumentType, &d.DocumentNumber, &d.FileURL, &d.Status, &d.RejectionReason, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}
