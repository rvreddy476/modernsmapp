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
	"bytes"
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
	// BackViewable: the back of a voter ID or passport was uploaded and can
	// be opened with ?side=back (migration 045).
	BackViewable bool `json:"back_viewable"`
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
			BackViewable:       d.HasBackImage(),
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
// image to show; media.ErrMediaUnavailable, media.ErrNotAnImage,
// media.ErrImageTooLarge, media.ErrNotADocument and media.ErrDocumentTooLarge
// from the fetch (openKYCMedia: a document through document-bytes, a legacy
// image through image-bytes).
//
// Logs one line per open with the seller, the document and the acting admin —
// ids only, nothing a document says.
func (s *Service) AdminOpenSellerDocumentImage(ctx context.Context, sellerID, documentID, actor uuid.UUID) (*SellerDocumentImage, error) {
	return s.AdminOpenSellerDocumentSide(ctx, sellerID, documentID, actor, false)
}

// documentImageMediaID picks the upload to open: the front, or with back the
// back of a two-sided document. Read from the row, never from the request.
func documentImageMediaID(doc *postgres.AdminSellerDocument, back bool) (uuid.UUID, error) {
	if back {
		if !doc.HasBackImage() {
			return uuid.Nil, ErrDocumentImageUnavailable
		}
		return *doc.BackMediaID, nil
	}
	if !doc.HasImage() {
		return uuid.Nil, ErrDocumentImageUnavailable
	}
	return *doc.MediaID, nil
}

// AdminOpenSellerDocumentSide is AdminOpenSellerDocumentImage for either
// side: back=true opens the back of a voter ID or passport (migration 045).
func (s *Service) AdminOpenSellerDocumentSide(ctx context.Context, sellerID, documentID, actor uuid.UUID, back bool) (*SellerDocumentImage, error) {
	if actor == uuid.Nil {
		return nil, postgres.ErrActorRequired
	}
	doc, err := s.store.SellerDocumentForAdmin(ctx, sellerID, documentID)
	if err != nil {
		return nil, err
	}
	mediaID, err := documentImageMediaID(doc, back)
	if err != nil {
		return nil, err
	}
	body, contentType, err := s.openKYCMedia(ctx, mediaID)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			return nil, ErrDocumentImageUnavailable
		}
		return nil, fmt.Errorf("seller document image: %w", err)
	}
	slog.InfoContext(ctx, "commerce: KYC document image opened for an admin",
		"seller_id", sellerID, "document_id", documentID, "document_type", doc.DocumentType,
		"back", back, "actor", actor, "content_type", contentType)
	return &SellerDocumentImage{Body: body, ContentType: contentType, DocumentType: doc.DocumentType}, nil
}

// kycMediaSource names the media-service read a KYC upload is opened with.
type kycMediaSource int

const (
	kycFromDocumentBytes kycMediaSource = iota // a private document: PDF, JPEG or PNG, as uploaded
	kycFromImageBytes                          // a legacy image upload: re-encoded, EXIF stripped
)

// kycSourceFor picks the read for an asset's file type. Only the two kinds a
// KYC row can hold are opened; anything else is not a document to show.
func kycSourceFor(fileType string) (kycMediaSource, bool) {
	switch fileType {
	case string(media.KindDocument):
		return kycFromDocumentBytes, true
	case string(media.KindImage):
		return kycFromImageBytes, true
	}
	return 0, false
}

// openKYCMedia opens one KYC upload for the reviewer: a document through
// media-service's document-bytes route, a legacy image through image-bytes.
// The kind comes from media-service's own record, never from the request.
func (s *Service) openKYCMedia(ctx context.Context, mediaID uuid.UUID) (io.ReadCloser, string, error) {
	if s.media == nil {
		return nil, "", fmt.Errorf("%w: no media-service client is configured", media.ErrMediaUnavailable)
	}
	asset, err := s.media.Get(ctx, mediaID)
	if err != nil {
		return nil, "", err
	}
	source, ok := kycSourceFor(asset.FileType)
	if !ok {
		return nil, "", media.ErrMediaNotFound
	}
	if source == kycFromImageBytes {
		return s.media.FetchImageBytes(ctx, mediaID)
	}
	data, contentType, err := s.media.FetchDocumentBytes(ctx, mediaID)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), contentType, nil
}
