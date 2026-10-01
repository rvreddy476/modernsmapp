package service

// The admin console's view of a seller's KYC documents.
//
// Founder, 1 Oct 2026: approval stays manual, and the reviewer must be able to
// SEE the documents — in the browser, inside the console, never as a
// download. This is commerce's half: the list (no media id, no document
// number, no URL) and the image, as bytes, for one document that belongs to
// the seller named in the path.
//
// admin-service holds the permission check (commerce:kyc.verify), the step-up
// and the per-view audit row; commerce re-checks the permission on the signed
// token (admin_token.go) and decides WHICH media asset to read from its own
// row, never from the request.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// AdminSellerDocumentView is one row of GET …/sellers/:sellerId/documents.
// The whole wire shape: there is deliberately no media id, document number or
// URL on it.
type AdminSellerDocumentView struct {
	ID                 uuid.UUID `json:"id"`
	DocumentType       string    `json:"document_type"`
	VerificationStatus string    `json:"verification_status"`
	UploadedAt         time.Time `json:"uploaded_at"`
	Viewable           bool      `json:"viewable"`
}

// ErrDocumentImageUnavailable means the document has no image to show: no
// media id on the row, or media-service has no ready image for it.
var ErrDocumentImageUnavailable = errors.New("commerce: this document has no viewable image")

// AdminListSellerDocuments lists one seller's KYC documents for the reviewer.
func (s *Service) AdminListSellerDocuments(ctx context.Context, sellerID uuid.UUID) ([]AdminSellerDocumentView, error) {
	docs, err := s.store.ListSellerDocumentsForAdmin(ctx, sellerID)
	if err != nil {
		return nil, err
	}
	out := make([]AdminSellerDocumentView, 0, len(docs))
	for _, d := range docs {
		out = append(out, AdminSellerDocumentView{
			ID:                 d.ID,
			DocumentType:       d.DocumentType,
			VerificationStatus: d.VerificationStatus,
			UploadedAt:         d.UploadedAt,
			Viewable:           d.HasImage(),
		})
	}
	return out, nil
}

// SellerDocumentImage is an open image stream. The caller closes Body.
type SellerDocumentImage struct {
	Body         io.ReadCloser
	ContentType  string
	DocumentType string
}

// AdminOpenSellerDocumentImage opens the image of one document of one seller.
//
// Errors: postgres.ErrSellerDocumentNotFound when the document is not this
// seller's (or does not exist); ErrDocumentImageUnavailable when there is no
// image to show; media.ErrMediaUnavailable, media.ErrNotAnImage and
// media.ErrImageTooLarge from the fetch.
//
// Logs one line per open with the seller, the document and the acting admin —
// ids only, nothing a document says.
func (s *Service) AdminOpenSellerDocumentImage(ctx context.Context, sellerID, documentID, actor uuid.UUID) (*SellerDocumentImage, error) {
	if actor == uuid.Nil {
		return nil, postgres.ErrActorRequired
	}
	doc, err := s.store.SellerDocumentForAdmin(ctx, sellerID, documentID)
	if err != nil {
		return nil, err
	}
	if !doc.HasImage() {
		return nil, ErrDocumentImageUnavailable
	}
	body, contentType, err := s.media.FetchImageBytes(ctx, *doc.MediaID)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			return nil, ErrDocumentImageUnavailable
		}
		return nil, fmt.Errorf("seller document image: %w", err)
	}
	slog.InfoContext(ctx, "commerce: KYC document image opened for an admin",
		"seller_id", sellerID, "document_id", documentID, "document_type", doc.DocumentType, "actor", actor)
	return &SellerDocumentImage{Body: body, ContentType: contentType, DocumentType: doc.DocumentType}, nil
}
