package service

// Private documents (31 Oct 2026): the `document` file type, for seller KYC
// evidence — a PAN card, a GST certificate, a cancelled cheque — which is
// often a PDF.
//
// A document is not media. It is uploaded by one person for one reviewer and
// is never shown to anyone else, so the whole kind is built around NOT being
// delivered:
//
//   - Allowed types: application/pdf, image/jpeg and image/png, nothing else
//     (allowedDocumentMIME). The declared type is checked at init and the
//     first bytes are checked against it at confirm (DocumentMagicMatches):
//     `%PDF-` for a PDF, a real JPEG or PNG signature for an image.
//   - At most MaxDocumentSizeBytes; simple upload only (the resumable path
//     refuses the kind).
//   - No transcoding, no renditions, no scan, no blurhash: the original is the
//     only object. Confirm moves it straight to ready (MarkDocumentReady).
//   - Stored in the same private bucket under the uploader's protected
//     prefix ("user/<uploader>/<id>/original"), so purge and reclamation keep
//     working, but NO viewer route ever signs or streams it: every read path
//     a client reaches refuses a document (ViewerReadDenies) — to its own
//     uploader too. The only way out is the internal document-bytes route,
//     which requires a commerce-service token (http/document_bytes_handler.go).
//   - A separate upload budget (MaxDocumentUploadsPerDay per user), so KYC
//     uploads neither spend nor are blocked by the 10-per-hour media limit.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FileTypeDocument is the file_type of a private document.
const FileTypeDocument = "document"

const (
	// MaxDocumentSizeBytes caps one document (10 MiB): a phone photo of a
	// card or a scanned multi-page PDF fits; a video renamed .pdf does not.
	MaxDocumentSizeBytes int64 = 10 * 1024 * 1024

	// MaxDocumentUploadsPerDay is the per-user document upload budget, kept
	// apart from MaxUploadsPerHour/MaxUploadsPerDay. A full KYC set with
	// fronts, backs and a retake or two is well under it.
	MaxDocumentUploadsPerDay = 20
)

// Document content types, exactly as the upload declares and the bytes
// route answers them.
const (
	DocumentMIMEPDF  = "application/pdf"
	DocumentMIMEJPEG = "image/jpeg"
	DocumentMIMEPNG  = "image/png"
)

// allowedDocumentMIME is the closed allow-list for documents. WebP, HEIC and
// GIF are images media-service accepts elsewhere, but not here: the reviewer's
// console and the magic-byte check both know these three.
var allowedDocumentMIME = map[string]bool{
	DocumentMIMEPDF:  true,
	DocumentMIMEJPEG: true,
	DocumentMIMEPNG:  true,
}

var (
	pdfMagic  = []byte("%PDF-")
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
)

// normaliseMIME is a Content-Type reduced to its lower-case media type.
func normaliseMIME(v string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(v, ";")[0]))
}

// validateDocumentUpload is the init-time check for a document: the general
// subtype only, an allowed type, a non-empty file of at most
// MaxDocumentSizeBytes. The messages carry the phrases the init handler maps
// ("file too large", "invalid document type").
func validateDocumentUpload(mediaSubtype, mimeType string, size int64) error {
	if mediaSubtype != "" && mediaSubtype != "general" {
		return fmt.Errorf("a document has no media_subtype, got: %s", mediaSubtype)
	}
	if !allowedDocumentMIME[normaliseMIME(mimeType)] {
		return fmt.Errorf("invalid document type: %s (allowed: application/pdf, image/jpeg, image/png)", normaliseMIME(mimeType))
	}
	if size <= 0 {
		return fmt.Errorf("a document cannot be empty")
	}
	if size > MaxDocumentSizeBytes {
		return fmt.Errorf("file too large: document of %d bytes (max %d)", size, MaxDocumentSizeBytes)
	}
	return nil
}

// SniffDocumentType names the document type the bytes ARE, from their first
// bytes, or "" when they are none of the three.
func SniffDocumentType(head []byte) string {
	switch {
	case bytes.HasPrefix(head, pdfMagic):
		return DocumentMIMEPDF
	case bytes.HasPrefix(head, jpegMagic):
		return DocumentMIMEJPEG
	case bytes.HasPrefix(head, pngMagic):
		return DocumentMIMEPNG
	}
	return ""
}

// DocumentMagicMatches: the bytes are the type the upload declared. A PNG
// declared as a PDF is refused as surely as an HTML page is.
func DocumentMagicMatches(declaredMIME string, head []byte) bool {
	declared := normaliseMIME(declaredMIME)
	return allowedDocumentMIME[declared] && SniffDocumentType(head) == declared
}

// IsDocument reports whether m is a private document.
func IsDocument(m *postgres.MediaAsset) bool {
	return m != nil && m.FileType == FileTypeDocument
}

// ViewerReadDenies is the scope rule every viewer read path applies: a
// document is refused to everyone, its uploader included (no signed URL, no
// redirect, no stream, no caption, no download), and a dating-scoped asset to
// everyone but its uploader (DatingScopeDenies).
//
// The keyed record read (MediaForService) does not apply it: commerce reads a
// document's record there to verify ownership and readiness, and the record
// carries no URL.
func ViewerReadDenies(m *postgres.MediaAsset, viewerID uuid.UUID) bool {
	return IsDocument(m) || DatingScopeDenies(m, viewerID)
}

// checkDocumentUploadLimit is the limiter a document init calls instead of
// checkUploadLimit.
func (s *Service) checkDocumentUploadLimit(ctx context.Context, userID uuid.UUID) error {
	if s.documentUploadLimit != nil {
		return s.documentUploadLimit(ctx, userID)
	}
	return s.CheckDocumentUploadRateLimit(ctx, userID)
}

// documentDayKey is the Redis counter of one user's document uploads. Its own
// key: a document never spends, and is never blocked by, the media budget.
func documentDayKey(userID uuid.UUID) string {
	return fmt.Sprintf("upload:rate:%s:document:day", userID)
}

// CheckDocumentUploadRateLimit allows MaxDocumentUploadsPerDay document
// uploads per user per rolling day. Same mechanics (and the same fail-open on
// a Redis outage) as CheckUploadRateLimit.
func (s *Service) CheckDocumentUploadRateLimit(ctx context.Context, userID uuid.UUID) error {
	if s.rdb == nil {
		return nil
	}
	if !uploadAllow(ctx, s.rdb, documentDayKey(userID), int64(MaxDocumentUploadsPerDay), 24*time.Hour) {
		slog.Warn("document upload rate limit exceeded (daily)", "user_id", userID)
		return fmt.Errorf("upload rate limit exceeded: max %d documents per day", MaxDocumentUploadsPerDay)
	}
	return nil
}

// ─── Reading a document back, for commerce only ─────────────────────────

// ErrDocumentNotFound is the single not-found answer of DocumentBytes.
var ErrDocumentNotFound = errors.New("media: document not available")

// DocumentBytes is one document's bytes and the type they were verified as.
type DocumentBytes struct {
	Bytes       []byte
	ContentType string
}

// documentRecords / documentObjects are the store slices the reader needs.
type documentRecords interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
}

type documentObjects interface {
	OpenObject(ctx context.Context, objectKey string) (io.ReadCloser, blob.ObjectInfo, error)
}

// DocumentReader backs GET /v1/media/internal/:mediaId/document-bytes.
type DocumentReader struct {
	records documentRecords
	objects documentObjects
}

// NewDocumentReader builds a reader over a record store and an object store.
func NewDocumentReader(records documentRecords, objects documentObjects) *DocumentReader {
	return &DocumentReader{records: records, objects: objects}
}

// DocumentReader returns the reader over this service's stores.
func (s *Service) DocumentReader() *DocumentReader {
	return NewDocumentReader(s.pgStore, s.blobStore)
}

// DocumentBytes returns the stored bytes of one ready document, unchanged.
//
// Served only when every one of these holds; anything else is
// ErrDocumentNotFound, one answer, so the route never says why:
//   - the row is a document, ready, and not rejected by moderation;
//   - its key is the uploader's protected original key (never public/);
//   - the object is the one confirmed: same size as declared, same ETag as
//     pinned at confirm, at most MaxDocumentSizeBytes;
//   - its first bytes are still the declared type (re-checked on every read:
//     the uploader's presigned PUT outlives confirm by minutes).
//
// A store fault is returned as an error (an outage, not a missing document).
func (r *DocumentReader) DocumentBytes(ctx context.Context, mediaID uuid.UUID) (*DocumentBytes, error) {
	m, err := r.records.GetMedia(ctx, mediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("document bytes: read media: %w", err)
	}
	if !IsDocument(m) || m.ProcessingStatus != "ready" || m.ModerationStatus == "rejected" {
		return nil, ErrDocumentNotFound
	}
	if m.StorageKey != fmt.Sprintf("user/%s/%s/original", m.UploaderID, m.ID) || m.OriginalETag == "" {
		return nil, ErrDocumentNotFound
	}
	rc, info, err := r.objects.OpenObject(ctx, m.StorageKey)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("document bytes: open object: %w", err)
	}
	defer rc.Close()
	if info.Size > MaxDocumentSizeBytes || info.Size != m.FileSizeBytes || info.ETag != m.OriginalETag {
		return nil, ErrDocumentNotFound
	}
	data, err := io.ReadAll(io.LimitReader(rc, MaxDocumentSizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("document bytes: read object: %w", err)
	}
	if int64(len(data)) != m.FileSizeBytes || int64(len(data)) > MaxDocumentSizeBytes {
		return nil, ErrDocumentNotFound
	}
	if !DocumentMagicMatches(m.MimeType, data) {
		return nil, ErrDocumentNotFound
	}
	return &DocumentBytes{Bytes: data, ContentType: SniffDocumentType(data)}, nil
}
