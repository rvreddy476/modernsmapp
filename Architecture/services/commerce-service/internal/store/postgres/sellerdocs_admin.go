package postgres

// A seller's KYC documents, as the admin console's reviewer sees them.
//
// Read-only, and narrow on purpose. `seller_documents` also holds the sealed
// document numbers (migration 035) and the media id of each upload; NEITHER
// leaves commerce on this path. The reviewer gets the type, the status and
// the date, plus whether there is an image to look at; the image itself is
// fetched by id inside commerce, from the row this file returns, and streamed
// as bytes — see internal/media/imagebytes.go.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrSellerDocumentNotFound means no document with that id belongs to that
// seller. One error for "no such document" and "someone else's document", so
// the route cannot be used to learn which document ids exist.
var ErrSellerDocumentNotFound = errors.New("commerce: no such document for this seller")

// ErrSellerNotFound means no seller row has that id.
var ErrSellerNotFound = errors.New("commerce: seller not found")

// AdminSellerDocument is one KYC row for the reviewer. MediaID is for
// commerce's own use and is never serialised.
type AdminSellerDocument struct {
	ID                 uuid.UUID
	DocumentType       string
	VerificationStatus string
	UploadedAt         time.Time
	MediaID            *uuid.UUID
}

// HasImage reports whether there is an upload to show.
func (d AdminSellerDocument) HasImage() bool {
	return d.MediaID != nil && *d.MediaID != uuid.Nil
}

// ListSellerDocumentsForAdmin returns every document of one seller, in a
// stable order: by type, then upload time. ErrSellerNotFound when the seller
// does not exist, so "no documents yet" and "no such seller" stay distinct.
func (s *Store) ListSellerDocumentsForAdmin(ctx context.Context, sellerID uuid.UUID) ([]AdminSellerDocument, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sellers WHERE id = $1)`, sellerID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrSellerNotFound
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, document_type, verification_status, uploaded_at, media_id
		  FROM seller_documents
		 WHERE seller_id = $1
		 ORDER BY document_type ASC, uploaded_at ASC, id ASC`, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminSellerDocument{}
	for rows.Next() {
		var d AdminSellerDocument
		if err := rows.Scan(&d.ID, &d.DocumentType, &d.VerificationStatus, &d.UploadedAt, &d.MediaID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SellerDocumentForAdmin reads ONE document, and only if it is this seller's.
//
// The seller id is part of the WHERE, not checked afterwards: a document id
// from another seller's list must be indistinguishable from an id that does
// not exist.
func (s *Store) SellerDocumentForAdmin(ctx context.Context, sellerID, documentID uuid.UUID) (*AdminSellerDocument, error) {
	var d AdminSellerDocument
	err := s.db.QueryRow(ctx, `
		SELECT id, document_type, verification_status, uploaded_at, media_id
		  FROM seller_documents
		 WHERE id = $1 AND seller_id = $2`, documentID, sellerID).
		Scan(&d.ID, &d.DocumentType, &d.VerificationStatus, &d.UploadedAt, &d.MediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSellerDocumentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
