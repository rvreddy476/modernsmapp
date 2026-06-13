package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PageDocument is a page-scoped verification document (spec §5.3).
type PageDocument struct {
	ID               uuid.UUID  `json:"id"`
	PageID           uuid.UUID  `json:"page_id"`
	DocumentType     string     `json:"document_type"`
	DocumentURL      string     `json:"document_url"`
	Status           string     `json:"status"`
	ReviewedByUserID *uuid.UUID `json:"reviewed_by_user_id,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	RejectionReason  string     `json:"rejection_reason,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// AddPageDocument inserts a pending verification document for a page.
func (s *Store) AddPageDocument(ctx context.Context, d *PageDocument) error {
	d.ID = uuid.New()
	d.CreatedAt = time.Now()
	if d.Status == "" {
		d.Status = "pending"
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO page_verification_documents (id, page_id, document_type, document_url, status, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		d.ID, d.PageID, d.DocumentType, d.DocumentURL, d.Status, d.CreatedAt)
	return err
}

// ListPageDocuments returns all documents for a page.
func (s *Store) ListPageDocuments(ctx context.Context, pageID uuid.UUID) ([]PageDocument, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, page_id, document_type, document_url, status,
		        reviewed_by_user_id, reviewed_at, COALESCE(rejection_reason,''), created_at
		 FROM page_verification_documents WHERE page_id=$1 ORDER BY created_at`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []PageDocument
	for rows.Next() {
		var d PageDocument
		if err := rows.Scan(&d.ID, &d.PageID, &d.DocumentType, &d.DocumentURL, &d.Status,
			&d.ReviewedByUserID, &d.ReviewedAt, &d.RejectionReason, &d.CreatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// UploadedDocTypes returns the set of document types already uploaded for a page
// (status pending or approved), used by submit-review to verify required docs.
func (s *Store) UploadedDocTypes(ctx context.Context, pageID uuid.UUID) (map[string]bool, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT document_type FROM page_verification_documents
		 WHERE page_id=$1 AND status IN ('pending','approved')`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[t] = true
	}
	return out, rows.Err()
}

// SetPageDocStatus reviews a single document (admin, spec §6.16).
func (s *Store) SetPageDocStatus(ctx context.Context, docID, reviewerID uuid.UUID, status, reason string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE page_verification_documents
		 SET status=$2, reviewed_by_user_id=$3, reviewed_at=NOW(), rejection_reason=$4
		 WHERE id=$1`, docID, status, reviewerID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPageNotFound
	}
	return nil
}
